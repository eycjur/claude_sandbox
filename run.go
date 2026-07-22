package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"agentsb/internal/config"
	"agentsb/internal/dotfiles"
	"agentsb/internal/herdr"
	"agentsb/internal/home"
	"agentsb/internal/image"
	"agentsb/internal/runlog"
	"agentsb/internal/sandbox"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// runRun はサンドボックスの状態を問わず「入れる状態」まで進めてセッションを開く:
// テンプレートが無ければビルド、サンドボックスが無ければ作成して、exec で入る。
// 停止中のサンドボックスの再開は sbx に任せる。セッション終了後に認証情報を
// ベース home へ同期する。
func runRun(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	logConfig(cfg)
	if err := sandbox.CheckCLI(); err != nil {
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
		// TODO: herdr はエージェントごとに専用の画面マニフェストで状態を検出して
		// おり、汎用プレースホルダーでは検出できない（herdr.dev/docs/agents/ 参照）。
		// Codex CLI を herdr 側でも正しく検出させるには、コンテナ内での実際の起動を
		// 検知して herdr agent rename 等で動的に切り替える対応が別途必要。
		// それまでは Claude Code 前提の固定値に戻す（Codex 利用時は herdr 側の
		// 状態表示が claude のまま不正確になる既知の制約）。
		herdrAgent = "claude"
		herdrEnv.Announce()
	}

	runName := sandbox.RunName(cwd)
	runlog.Info("run cwd=%s name=%s", cwd, runName)

	exists, err := sandbox.Has(runName)
	if err != nil {
		return err
	}
	runlog.Info("sandbox %s exists=%v", runName, exists)

	// 既存のサンドボックスがあれば、テンプレート定義が変わっていてもそのまま
	// 使い続ける（作り直すと apt install などサンドボックス内の変更が消えるため）。

	// 認証情報ファイルをサンドボックスとやり取りする（詳細は internal/home のコメント）。
	credFiles, err := home.EnsureCredentialFiles()
	if err != nil {
		return fmt.Errorf("cannot prepare credential files: %w", err)
	}

	created := !exists
	if created {
		templateTag, err := image.EnsureBuilt(false)
		if err != nil {
			return err
		}
		if err := sandbox.Create(runName, templateTag, cwd); err != nil {
			return err
		}
		if err := home.InjectCredentials(runName, credFiles); err != nil {
			return fmt.Errorf("cannot inject credentials: %w", err)
		}
	}

	// セッションはログインシェル固定。エージェントはシェル内から手動で起動する。
	// [dotfiles] が設定されていれば、サンドボックスの新規作成時のみ clone/
	// インストールを済ませてからシェルへ exec する起動スクリプトで包む（詳細は
	// internal/dotfiles）。既存サンドボックスへ入るだけの場合は、毎回の
	// clone/pull が冗長なためスキップする
	command := []string{"zsh", "-l"}
	if created && cfg.Dotfiles.Repository != "" {
		command = dotfiles.Command(
			cfg.Dotfiles.Repository,
			cfg.Dotfiles.TargetPath,
			cfg.Dotfiles.InstallCommand,
			command,
		)
		runlog.Info("session will bootstrap dotfiles then exec %v", []string{"zsh", "-l"})
	} else if cfg.Dotfiles.Repository != "" {
		runlog.Info("session command: %v (dotfiles bootstrap skipped: sandbox already exists)", command)
	} else {
		runlog.Info("session command: %v (dotfiles disabled)", command)
	}

	runlog.Info("exec session herdrAgent=%q command=%v", herdrAgent, command)
	code, err := execSession(runName, cwd, herdrAgent, command)
	runlog.Info("session finished exit=%d err=%v", code, err)

	// セッションの終わり方によらず、認証情報の同期は必ず行う。サンドボックスは
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
	path, _ := config.GlobalPath()
	if _, err := os.Stat(path); err != nil {
		runlog.Info("config file missing path=%s (using defaults)", path)
	} else {
		runlog.Info("config file loaded path=%s", path)
	}
	if cfg.Dotfiles.Repository == "" {
		runlog.Info("config dotfiles=disabled (set [dotfiles].repository in %s)", path)
		return
	}
	target := cfg.Dotfiles.TargetPath
	if target == "" {
		target = "~/dotfiles"
	}
	runlog.Info("config dotfiles repository=%s target=%s install=%s",
		cfg.Dotfiles.Repository, target, cfg.Dotfiles.InstallCommand)
}

// execSession はサンドボックスで `sbx exec` を前面実行し、セッションの終了
// コードを返す。
// herdrAgent が空でなければ、この `sbx exec` プロセス自身（ホスト側）の
// argv[0] をその名前に書き換える: herdr はホストのプロセスツリーからエージェ
// ントを識別し、その識別を前提に画面内容から状態を検出するため、サンドボックス
// 内のエージェント名をホスト側プロセスの argv[0] に映しておく。
// tty 接続時は `sbx exec` を新しいプロセスグループにして端末のフォアグラウンド
// グループへ昇格させる（Setpgid+Foreground）。herdr はフォアグラウンドの pgid
// が変化したことを再検出のトリガーにしており、agentsb と同じ pgid のまま子と
// して起動すると（サンドボックスの起動待ちの後にこの子プロセスが現れても）
// pgid が変化せず、argv[0] のヒントを持つプロセスの出現に herdr が気づけない。
func execSession(name, workdir, herdrAgent string, command []string) (int, error) {
	tty := term.IsTerminal(int(os.Stdin.Fd()))
	args := sandbox.ExecArgs(name, workdir, tty, command)
	cmd := exec.Command("sbx", args...)
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
