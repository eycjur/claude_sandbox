// agentsb は Docker Sandboxes（sbx）上で AI コーディングエージェントを
// 使い捨てサンドボックスとして起動する CLI。
// このファイルにはコマンド定義をまとめてある。複雑なコマンドの実装は
// run.go に、実処理は internal/ 配下にある。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"agentsb/internal/config"
	"agentsb/internal/image"
	"agentsb/internal/runlog"
	"agentsb/internal/sandbox"

	"github.com/spf13/cobra"
)

func main() {
	os.Exit(execute())
}

// execute は CLI を実行し、プロセスの終了ステータスを返す。
func execute() int {
	rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", false, "mirror diagnostic logs to stderr")
	pruneCmd.Flags().BoolVarP(&pruneYes, "yes", "y", false, "skip confirmation prompt")
	rootCmd.AddCommand(runCmd, buildCmd, lsCmd, stopCmd, rmCmd, pruneCmd, openCmd)
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		runlog.SetVerbose(verboseFlag)
		runlog.Open()
		runlog.Info("command %s", strings.Join(os.Args[1:], " "))
	}
	defer runlog.Close()
	if err := rootCmd.Execute(); err != nil {
		runlog.Warn("command failed: %v", err)
		fmt.Fprintln(os.Stderr, "agentsb:", err)
		return 1
	}
	return exitCode
}

// exitCode は、コマンドが Go のエラーなしに完了したときに伝播させる
// プロセス終了ステータス（例: サンドボックス内エージェントの exit code）。
var exitCode int

// verboseFlag は -v / --verbose。
var verboseFlag bool

// rootCmd は agentsb のルートコマンド。
var rootCmd = &cobra.Command{
	Use:           "agentsb",
	Short:         "Run AI coding agents in Docker Sandboxes",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// runCmd は agentsb run コマンド。エージェント用サンドボックスを起動する。
var runCmd = &cobra.Command{
	Use:     "run",
	Aliases: []string{"exec"},
	Short:   "Enter the sandbox for the current directory (builds and creates it as needed)",
	Args:    cobra.NoArgs,
	RunE:    runRun,
}

// buildCmd は agentsb build コマンド。テンプレートを強制リビルドして sbx へ
// ロードし直し、古いテンプレートを prune する。
var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Force rebuild the sandbox template (picks up base image or tool updates)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := sandbox.CheckCLI(); err != nil {
			return err
		}
		tag, err := image.EnsureBuilt(true)
		if err != nil {
			return err
		}
		fmt.Printf("built %s\n", tag)
		return nil
	},
}

// lsCmd は agentsb ls コマンド。agentsb のサンドボックスを一覧する。
// カラム構成（状態など）は `sbx ls` の出力に任せ、agentsb 管理分だけを
// フィルタして表示する。
var lsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list", "ps"},
	Short:   "List agentsb sandboxes (including stopped)",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := sandbox.CheckCLI(); err != nil {
			return err
		}
		out, err := sandbox.ListOutput()
		if err != nil {
			return err
		}
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		var rows []string
		for i, line := range lines {
			// 先頭行はヘッダーとして残し、以降は agentsb 管理分のみ表示する。
			if i == 0 || strings.Contains(line, sandbox.NamePrefix) {
				rows = append(rows, line)
			}
		}
		if len(rows) <= 1 {
			fmt.Println("no agentsb sandboxes")
			return nil
		}
		for _, line := range rows {
			fmt.Println(line)
		}
		return nil
	},
}

// targetName は引数で指定された名前を返し、省略時はカレントディレクトリの
// サンドボックス名を返す。`agentsb-` プレフィックスは省略できる。
func targetName(args []string) (string, error) {
	if len(args) == 1 {
		name := args[0]
		if !strings.HasPrefix(name, sandbox.NamePrefix) {
			name = sandbox.NamePrefix + name
		}
		return name, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot get working directory: %w", err)
	}
	return sandbox.RunName(cwd), nil
}

// stopCmd は agentsb stop コマンド。サンドボックスを停止する。
// サンドボックスと home は残るため、次の run で同じ状態から再開できる。
var stopCmd = &cobra.Command{
	Use:   "stop [name]",
	Short: "Stop a running sandbox (state is kept; the next run resumes it; defaults to the current directory's)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := sandbox.CheckCLI(); err != nil {
			return err
		}
		name, err := targetName(args)
		if err != nil {
			return err
		}
		if err := sandbox.Stop(name); err != nil {
			return fmt.Errorf("stop %s: %w", name, err)
		}
		fmt.Printf("stopped %s\n", name)
		return nil
	},
}

// pruneYes は prune の -y/--yes。確認プロンプトをスキップする。
var pruneYes bool

// pruneCmd は agentsb prune コマンド。agentsb が管理する全サンドボックス・
// 全テンプレート・認証情報・ビルド作業ディレクトリを削除するフルリセット。
// 認証情報が消えるため次回 run では各サンドボックスで再ログインが必要になる。
var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove all agentsb sandboxes, templates, and stored credentials (full reset)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !pruneYes && !confirmPrune() {
			fmt.Println("aborted")
			return nil
		}
		if err := sandbox.CheckCLI(); err != nil {
			return err
		}

		var errs []string
		names, err := sandbox.AgentsbNames()
		if err != nil {
			errs = append(errs, err.Error())
		}
		for _, name := range names {
			if err := sandbox.Remove(name); err != nil {
				errs = append(errs, fmt.Sprintf("remove %s: %v", name, err))
			}
		}
		if err := image.DeleteAll(); err != nil {
			errs = append(errs, err.Error())
		}
		root, err := config.Root()
		if err != nil {
			return err
		}
		if err := os.RemoveAll(root); err != nil {
			errs = append(errs, err.Error())
		}
		if len(errs) > 0 {
			return fmt.Errorf("prune finished with errors: %s", strings.Join(errs, "; "))
		}
		fmt.Println("pruned all agentsb sandboxes, templates, and credentials")
		return nil
	},
}

// confirmPrune は破壊的な操作であることを明示し、標準入力で確認を取る。
func confirmPrune() bool {
	fmt.Print("This removes all agentsb sandboxes, templates, and stored credentials. Continue? [y/N] ")
	var reply string
	fmt.Scanln(&reply)
	return strings.ToLower(strings.TrimSpace(reply)) == "y"
}

// openCmd は agentsb open コマンド。カレントディレクトリのサンドボックスの
// ポートをホストへ公開し、サンドボックス内で動くサーバーをブラウザで開く。
// microVM のためサンドボックスの IP へ直接は届かず、`sbx ports --publish` で
// 明示的に公開して localhost 経由でアクセスする。
var openCmd = &cobra.Command{
	Use:   "open [port]",
	Short: "Publish the sandbox's port and open it in the browser (defaults to port 8000)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := sandbox.CheckCLI(); err != nil {
			return err
		}
		port := 8000
		if len(args) == 1 {
			p, err := strconv.Atoi(args[0])
			if err != nil || p < 1 || p > 65535 {
				return fmt.Errorf("invalid port %q", args[0])
			}
			port = p
		}
		name, err := targetName(nil)
		if err != nil {
			return err
		}
		exists, err := sandbox.Has(name)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("no sandbox for this directory — start one with `agentsb run`")
		}
		// 公開済みのポートを再度 publish するとエラーになる可能性があるため、
		// 失敗は警告に留めてブラウザは開く（初回公開なら成功する）。
		if err := sandbox.PublishPort(name, port); err != nil {
			runlog.Warn("publish port %d failed (may already be published): %v", port, err)
			fmt.Fprintf(os.Stderr, "agentsb: warning: publish port %d: %v\n", port, err)
		}
		url := fmt.Sprintf("http://localhost:%d/", port)
		fmt.Println(url)
		if err := exec.Command("open", url).Run(); err != nil {
			return fmt.Errorf("open %s: %w", url, err)
		}
		return nil
	},
}

// rmCmd は agentsb rm コマンド。サンドボックスを削除する（稼働中でも
// `sbx rm --force` で停止込みで消える）。認証情報は `~/.agentsb/home` に
// 別途永続化されており、他のサンドボックスとも共有しているため、ここでは
// 削除しない。名前を省略するとカレントディレクトリのサンドボックスを対象にする。
var rmCmd = &cobra.Command{
	Use:     "rm [name]",
	Aliases: []string{"delete", "remove"},
	Short:   "Remove a sandbox (defaults to the current directory's)",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := sandbox.CheckCLI(); err != nil {
			return err
		}
		name, err := targetName(args)
		if err != nil {
			return err
		}
		exists, err := sandbox.Has(name)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("no sandbox named %s", name)
		}
		if err := sandbox.Remove(name); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", name)
		return nil
	},
}
