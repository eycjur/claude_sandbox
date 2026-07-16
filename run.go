package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"agentsb/internal/config"
	"agentsb/internal/container"
	"agentsb/internal/dotfiles"
	"agentsb/internal/herdr"
	"agentsb/internal/home"
	"agentsb/internal/image"
	"agentsb/internal/runlog"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// runRun はサンドボックスの状態を問わず「入れる状態」まで進めてセッションを開く:
// イメージが無ければビルド、コンテナが無ければ作成、停止中なら起動、
// 起動済みなら exec するだけ。セッション終了後に認証情報をベース home へ同期する。
func runRun(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if runCPUs > 0 {
		cfg.Container.CPUs = runCPUs
	}
	if runMemory != "" {
		cfg.Container.Memory = runMemory
	}
	logConfig(cfg)
	if err := container.EnsureRunning(); err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot get working directory: %w", err)
	}

	// herdr の pane 内なら表示名を報告し、状態検出用に argv[0] へ埋める
	// エージェント名を決めておく（詳細は internal/herdr のパッケージコメント）。
	herdrEnv := herdr.Detect()
	var herdrAgent string
	if herdrEnv != nil {
		// agentsb は Claude Code 専用サンドボックスとして固定で報告する。
		herdrAgent = "claude"
		herdrEnv.Announce(herdrAgent)
	}

	uid, gid := container.HostIDs()
	runName := container.RunName(cwd)
	runlog.Info("run cwd=%s name=%s uid=%d gid=%d", cwd, runName, uid, gid)

	info, err := container.Get(runName)
	if err != nil {
		return err
	}
	if info == nil {
		runlog.Info("sandbox %s does not exist yet", runName)
	} else {
		runlog.Info("sandbox %s state=%s image=%s ip=%s", info.Name, info.State, info.Image, info.IP)
	}

	// agentsb の更新でイメージ定義が変わっていたら、コンテナだけ作り直す。
	// 認証情報は ~/.agentsb/home に別途永続化されているため保持されるが、
	// それ以外のコンテナ層の変更（apt install など）は消える。
	if info != nil && !strings.HasSuffix(info.Image, image.Tag(uid, gid)) {
		runlog.Info("image definition changed; recreating sandbox %s (was %s, want tag ending %s)",
			runName, info.Image, image.Tag(uid, gid))
		fmt.Fprintf(os.Stderr, "agentsb: image definition changed, recreating sandbox %s…\n", runName)
		if info.State == container.StateRunning {
			if err := container.Stop(runName); err != nil {
				return fmt.Errorf("stop %s: %w", runName, err)
			}
		}
		if err := container.Delete(runName); err != nil {
			return err
		}
		info = nil
	}

	// 認証情報ファイルをコンテナとやり取りする（詳細は internal/home のコメント）。
	credFiles, err := home.EnsureCredentialFiles()
	if err != nil {
		return fmt.Errorf("cannot prepare credential files: %w", err)
	}

	created := info == nil
	if info == nil {
		imageTag, err := image.EnsureBuilt(uid, gid, false)
		if err != nil {
			return err
		}
		spec := container.CreateSpec{
			Name:  runName,
			Image: imageTag,
			Mounts: []container.Mount{
				{Host: cwd, Dest: container.Workdir},
			},
			CPUs:   cfg.Container.CPUs,
			Memory: cfg.Container.Memory,
			UID:    uid,
			GID:    gid,
		}
		if err := container.Create(spec); err != nil {
			return err
		}
		info = &container.ContainerInfo{Name: runName}
	} else if runCPUs > 0 || runMemory != "" {
		fmt.Fprintln(os.Stderr, "agentsb: warning: --cpus/--memory apply only when the sandbox is created — `agentsb rm` first to apply them")
	}

	justStarted := info.State != container.StateRunning
	if justStarted {
		runlog.Info("starting sandbox %s", runName)
		if err := container.Start(runName); err != nil {
			return err
		}
	}

	// `container cp` は稼働中のコンテナにしか使えないため、Start の後に注入する
	// （詳細は internal/home のコメント）。
	if created {
		if err := home.InjectCredentials(runName, credFiles, uid, gid); err != nil {
			return fmt.Errorf("cannot inject credentials: %w", err)
		}
	}

	// セッションはログインシェル固定。エージェントはシェル内から手動で起動する。
	// [dotfiles] が設定されていれば、サンドボックスの起動時（新規作成・再開時）
	// のみ clone/インストールを済ませてからシェルへ exec する起動スクリプトで
	// 包む（詳細は internal/dotfiles）。既に動いているサンドボックスへ追加の
	// 端末で入るだけの場合は、毎回のclone/pullが冗長なためスキップする。
	command := []string{"zsh", "-l"}
	if justStarted && cfg.Dotfiles.Repository != "" {
		command = dotfiles.Command(
			cfg.Dotfiles.Repository,
			cfg.Dotfiles.TargetPath,
			cfg.Dotfiles.InstallCommand,
			command,
		)
		runlog.Info("session will bootstrap dotfiles then exec %v", []string{"zsh", "-l"})
	} else if cfg.Dotfiles.Repository != "" {
		runlog.Info("session command: %v (dotfiles bootstrap skipped: sandbox already running)", command)
	} else {
		runlog.Info("session command: %v (dotfiles disabled)", command)
	}

	runlog.Info("exec session herdrAgent=%q command=%v", herdrAgent, command)
	code, err := execSession(runName, herdrAgent, command)
	runlog.Info("session finished exit=%d err=%v", code, err)

	// セッションの終わり方によらず、認証情報の同期は必ず行う。コンテナは
	// `rm` まで維持される。完了の herdr への報告は不要: exec プロセスの終了後、
	// herdr が自前で検出する。
	if syncErr := home.ExtractCredentials(runName, credFiles); syncErr != nil {
		runlog.Warn("could not sync credentials: %v", syncErr)
		fmt.Fprintf(os.Stderr, "agentsb: warning: could not sync credentials: %v\n", syncErr)
	} else {
		runlog.Info("synced credentials for %s", runName)
	}

	if err != nil {
		return err
	}
	exitCode = code
	return nil
}

// logConfig は読み込んだ設定の要点をログに残す（dotfiles 未設定の取りこぼし防止）。
func logConfig(cfg config.Config) {
	preferred, _ := config.GlobalPath()
	cfgPath := config.LoadedPath()
	if _, err := os.Stat(cfgPath); err != nil {
		runlog.Info("config file missing path=%s (using defaults)", preferred)
	} else {
		runlog.Info("config file loaded path=%s", cfgPath)
		if preferred != "" && cfgPath != preferred {
			runlog.Info("config: using legacy path; move to %s when convenient", preferred)
		}
	}
	runlog.Info("config container cpus=%d memory=%s", cfg.Container.CPUs, cfg.Container.Memory)
	if cfg.Dotfiles.Repository == "" {
		runlog.Info("config dotfiles=disabled (set [dotfiles].repository in %s)", preferred)
		return
	}
	target := cfg.Dotfiles.TargetPath
	if target == "" {
		target = "~/dotfiles"
	}
	runlog.Info("config dotfiles repository=%s target=%s install=%s",
		cfg.Dotfiles.Repository, target, cfg.Dotfiles.InstallCommand)
}

// execSession は稼働中のサンドボックスで `container exec` を前面実行し、
// セッションの終了コードを返す。
// herdrAgent が空でなければ、この `container exec` プロセス自身（ホスト側）の
// argv[0] をその名前に書き換える: herdr はホストのプロセスツリーからエージェ
// ントを識別し、その識別を前提に画面内容から状態を検出するため、コンテナ内の
// エージェント名をホスト側プロセスの argv[0] に映しておく。
// tty 接続時は `container exec` を新しいプロセスグループにして端末の
// フォアグラウンドグループへ昇格させる（Setpgid+Foreground）。herdr は
// フォアグラウンドの pgid が変化したことを再検出のトリガーにしており、
// agentsb と同じ pgid のまま子として起動すると（コンテナの起動待ちの後に
// この子プロセスが現れても）pgid が変化せず、argv[0] のヒントを持つ
// プロセスの出現に herdr が気づけない。
func execSession(name, herdrAgent string, command []string) (int, error) {
	tty := term.IsTerminal(int(os.Stdin.Fd()))
	args := container.ExecArgs(name, tty, command)
	cmd := exec.Command("container", args...)
	if herdrAgent != "" {
		cmd.Args[0] = herdrAgent
	}
	if tty {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Foreground: true,
			Ctty:       int(os.Stdin.Fd()),
		}
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return 1, fmt.Errorf("cannot start session: %w", err)
	}

	// シグナルを自分で受けて子に転送する: agentsb 自身が即死すると、
	// この後の認証情報の同期が走らなくなるため。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		for sig := range sigCh {
			cmd.Process.Signal(sig)
		}
	}()

	err := cmd.Wait()
	signal.Stop(sigCh)
	close(sigCh)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				return 128 + int(status.Signal()), nil
			}
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}
