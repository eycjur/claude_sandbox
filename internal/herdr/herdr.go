// Package herdr は herdr（エージェント用ターミナルマルチプレクサ）との連携。
//
// 連携は 2 段構え:
//
//  1. pane の表示名を `pane report-metadata --display-agent` で報告する（このパッケージ）。
//  2. エージェントの状態（working/blocked/idle）と完了は herdr 自身の検出に任せる。
//     herdr はホストのプロセスツリーから pane のエージェントを識別し、その識別を
//     前提に画面内容から状態を検出する。サンドボックス内のエージェントはホスト
//     から見えないため、`sbx exec` プロセスの argv[0] をエージェント名に書き
//     換えて herdr の検出を有効にする（書き換えは run.go の execSession で行う）。
//     手動で report-agent する方式だと開始/終了の 2 点しか報告できず、herdr の
//     リアルタイム検出（blocked 通知など）を覆い隠してしまう。
//
// 報告の失敗はすべて非致命的な警告 — run は herdr なしでも成立する。
package herdr

import (
	"fmt"
	"os"
	"os/exec"
)

// Env は herdr が pane 内のプロセスに渡す環境変数の内容。
type Env struct {
	PaneID  string
	BinPath string
}

// Detect は herdr の pane 内で動いているかを環境変数から判定し、
// pane 外なら nil を返す。
func Detect() *Env {
	if os.Getenv("HERDR_ENV") != "1" {
		return nil
	}
	paneID := os.Getenv("HERDR_PANE_ID")
	if paneID == "" {
		return nil
	}
	return &Env{
		PaneID:  paneID,
		BinPath: os.Getenv("HERDR_BIN_PATH"),
	}
}

// Announce はコンテナ起動前に pane の表示名を "agentsb" として報告する。
// --agent は渡さない: herdr 側のガードがホストのプロセスツリー由来のラベルとの
// 一致を要求するため、表示名だけを更新する。
func (e *Env) Announce() {
	e.run("pane", "report-metadata", e.PaneID,
		"--source", "user:agentsb",
		"--display-agent", "agentsb",
	)
}

// run は herdr CLI を実行する。失敗は警告のみ。
func (e *Env) run(args ...string) {
	bin := e.BinPath
	if bin == "" {
		bin = "herdr"
	}
	if err := exec.Command(bin, args...).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "agentsb: warning: herdr report failed: %v\n", err)
	}
}
