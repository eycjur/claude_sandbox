// Package sandbox は Docker Sandboxes の `sbx` CLI をラップする。
// サンドボックスの作成・exec・停止・削除・ファイルコピーと、
// テンプレート（サンドボックス用イメージ）のロード・削除を担当する。
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentsb/internal/runlog"
)

const (
	// HomeDir はサンドボックス側の agent ユーザーの home ディレクトリ。
	// docker/sandbox-templates 系イメージは root と agent の 2 ユーザーを持つ。
	HomeDir = "/home/agent"

	// NamePrefix は agentsb が管理するサンドボックス名のプレフィックス。
	NamePrefix = "agentsb-"
)

// cliTimeout は即応するはずの `sbx` サブコマンド1回あたりの上限。
// sbx が無応答になっても agentsb 側が無限に固まらないようにするための保険。
const cliTimeout = 30 * time.Second

// slowTimeout はサンドボックスの作成やテンプレートのロードなど、
// microVM の準備や大きなデータ転送を伴う操作の上限。
const slowTimeout = 10 * time.Minute

// CheckCLI は `sbx` CLI が使えることを確認する。
// 常駐サービスの起動管理は不要で、PATH の確認だけでよい。
func CheckCLI() error {
	if _, err := exec.LookPath("sbx"); err != nil {
		return fmt.Errorf(
			"the `sbx` CLI is not available — install Docker Sandboxes: " +
				"`brew install docker/tap/sbx` (https://docs.docker.com/ai/sandboxes/)",
		)
	}
	return nil
}

// runCLI は `sbx` サブコマンドを実行し、失敗時は stderr をエラーに含める。
// 成功時の stdout を返す。呼び出し内容は runlog に残す。
func runCLI(timeout time.Duration, args ...string) ([]byte, error) {
	runlog.Info("sbx %s", strings.Join(args, " "))
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sbx", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			runlog.Warn("sbx %s timed out after %s", strings.Join(args, " "), timeout)
			return out, fmt.Errorf("sbx %s: timed out after %s", strings.Join(args, " "), timeout)
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(string(out))
		}
		if detail != "" {
			runlog.Warn("sbx %s failed: %v: %s", strings.Join(args, " "), err, detail)
			return out, fmt.Errorf("%w: %s", err, detail)
		}
		runlog.Warn("sbx %s failed: %v", strings.Join(args, " "), err)
		return out, err
	}
	return out, nil
}

// Create はサンドボックスを作成する。ワークスペース（ホストのディレクトリ）は
// sbx がサンドボックス内の同じパスへマウントする。エージェント引数はテンプレート
// の拡張元バリアントと一致させる必要があり、agentsb のテンプレートは
// docker/sandbox-templates:shell を拡張するため `shell` 固定。
// リソースは指定せず sbx のデフォルト（CPU: ホストの全 CPU、メモリ: ホストの
// 50%・上限 32 GiB）に任せる。
func Create(name, template, workspace string) error {
	if _, err := runCLI(slowTimeout,
		"create", "--name", name, "--template", template, "shell", workspace,
	); err != nil {
		return fmt.Errorf("sbx create: %w", err)
	}
	runlog.Info("created sandbox %s template=%s workspace=%s", name, template, workspace)
	return nil
}

// ExecArgs は起動済みサンドボックスでセッションを開始する `sbx exec` の引数列を
// 返す。実行ユーザーはイメージの USER（agent）がそのまま使われ、作業ディレクトリ
// は -w で指定する（sbx はワークスペースをホストと同じパスにマウントする）。
func ExecArgs(name, workdir string, tty bool, command []string) []string {
	args := []string{"exec", "-i"}
	if tty {
		args = append(args, "-t")
	}
	args = append(args, "-w", workdir, name)
	return append(args, command...)
}

// RunName はカレントディレクトリのパスから決定的なサンドボックス名を生成する。
// 同じディレクトリでは常に同じ名前になるため、同時に起動できる run は
// ディレクトリごとに 1 つ。
func RunName(cwd string) string {
	return NamePrefix + pathKey(cwd)
}

// pathKey はディレクトリ名をサンドボックス名に使える文字（英小文字・数字・
// ハイフン）に正規化する。
func pathKey(path string) string {
	base := filepath.Base(path)
	var b strings.Builder
	for _, r := range strings.ToLower(base) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// Names は全サンドボックスの名前一覧を返す（`sbx ls -q`、停止中も含む）。
func Names() ([]string, error) {
	out, err := runCLI(cliTimeout, "ls", "-q")
	if err != nil {
		return nil, fmt.Errorf("sbx ls: %w", err)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	runlog.Info("sbx ls: %d entries", len(names))
	return names, nil
}

// AgentsbNames は agentsb が管理するサンドボックス名の一覧を返す。
func AgentsbNames() ([]string, error) {
	all, err := Names()
	if err != nil {
		return nil, err
	}
	var result []string
	for _, name := range all {
		if strings.HasPrefix(name, NamePrefix) {
			result = append(result, name)
		}
	}
	return result, nil
}

// Has は指定した名前のサンドボックスが存在するかを返す（停止中も含む）。
func Has(name string) (bool, error) {
	names, err := Names()
	if err != nil {
		return false, err
	}
	for _, n := range names {
		if n == name {
			return true, nil
		}
	}
	return false, nil
}

// ListOutput は `sbx ls` の出力をそのまま返す。`agentsb ls` はこれを
// agentsb- プレフィックスでフィルタして表示する（状態などのカラム構成は
// sbx に任せ、agentsb 側でパースしない）。
func ListOutput() (string, error) {
	out, err := runCLI(cliTimeout, "ls")
	if err != nil {
		return "", fmt.Errorf("sbx ls: %w", err)
	}
	return string(out), nil
}

// Stop は指定した名前のサンドボックスを停止する。
func Stop(name string) error {
	_, err := runCLI(cliTimeout, "stop", name)
	return err
}

// Remove はサンドボックスを削除する。稼働中でも --force で停止込みで消せる。
func Remove(name string) error {
	if _, err := runCLI(slowTimeout, "rm", "--force", name); err != nil {
		return fmt.Errorf("sbx rm: %w", err)
	}
	return nil
}

// PublishPort はサンドボックス内のポートをホストの同じ番号へ公開する。
// microVM のためサンドボックスの IP へ直接は届かず、`agentsb open` は
// これで公開してから localhost へアクセスする。
func PublishPort(name string, port int) error {
	p := strconv.Itoa(port)
	_, err := runCLI(cliTimeout, "ports", name, "--publish", p+":"+p)
	return err
}

// CopyToSandbox はホストのファイルをサンドボックス内へコピーする（`sbx cp`）。
func CopyToSandbox(name, hostPath, sandboxPath string) error {
	_, err := runCLI(cliTimeout, "cp", hostPath, name+":"+sandboxPath)
	return err
}

// CopyFromSandbox はサンドボックス内のファイルをホストへコピーする（`sbx cp`）。
func CopyFromSandbox(name, sandboxPath, hostPath string) error {
	_, err := runCLI(cliTimeout, "cp", name+":"+sandboxPath, hostPath)
	return err
}

// ChownAgent はサンドボックス内の path の所有者を agent に変更する。
// `sbx cp` の書き込みユーザーに依存しないよう、コピー後に呼ぶ。
// `exec` はイメージの既定ユーザー（agent）で動くため sudo を経由する
// （docker/sandbox-templates 系イメージは agent にパスワードなし sudo を付与済み）。
func ChownAgent(name, path string) error {
	_, err := runCLI(cliTimeout, "exec", name, "sudo", "chown", "agent:agent", path)
	return err
}

// ModTime はサンドボックス内のファイルの更新時刻を返す。
func ModTime(name, path string) (time.Time, error) {
	out, err := runCLI(cliTimeout, "exec", name, "stat", "-c", "%Y", path)
	if err != nil {
		return time.Time{}, fmt.Errorf("stat %s: %w", path, err)
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse mtime of %s: %w", path, err)
	}
	return time.Unix(sec, 0), nil
}

// SetModTime はサンドボックス内のファイルの更新時刻を t に設定する。
// InjectCredentials 直後にホスト側の mtime を写すために使う — こうしないと
// cp によるコピー自体がサンドボックス側の mtime をコピー時刻で更新してしまい、
// 以後の「新しい方だけ書き戻す」判定が常に真になってしまう。
func SetModTime(name, path string, t time.Time) error {
	_, err := runCLI(cliTimeout, "exec", name, "touch", "-d", fmt.Sprintf("@%d", t.Unix()), path)
	return err
}

// PathExists はサンドボックス内に指定パスが存在するかを返す。
// cp のエラーメッセージに頼らず `exec ... test -e` で明示的に確認する。
func PathExists(name, path string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel()
	err := exec.CommandContext(ctx, "sbx", "exec", name, "test", "-e", path).Run()
	if err == nil {
		return true, nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return false, fmt.Errorf("sbx exec test -e %s: timed out after %s", path, cliTimeout)
	}
	if _, ok := err.(*exec.ExitError); ok {
		return false, nil
	}
	return false, fmt.Errorf("sbx exec test -e %s: %w", path, err)
}

// LoadTemplate は `docker image save` で書き出した tar をサンドボックス
// ランタイムへロードする。sbx はホストの Docker のイメージストアを共有しない
// ため、ローカルビルドしたテンプレートはレジストリへ push する代わりに
// これでロードする。
func LoadTemplate(tarPath string) error {
	if _, err := runCLI(slowTimeout, "template", "load", tarPath); err != nil {
		return fmt.Errorf("sbx template load: %w", err)
	}
	return nil
}

// TemplateExists は指定タグのテンプレートがロード済みかを返す。
// 判定できない場合は false を返し、呼び出し側の再ビルド（冪等）に倒す。
func TemplateExists(tag string) bool {
	out, err := runCLI(cliTimeout, "template", "ls")
	if err != nil {
		runlog.Warn("sbx template ls failed, assuming %s is absent: %v", tag, err)
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		for _, ref := range templateRefs(line) {
			if ref == tag {
				runlog.Info("template %s exists", tag)
				return true
			}
		}
	}
	runlog.Info("template %s not loaded", tag)
	return false
}

// ListTemplates は basePrefix に一致するロード済みテンプレートのタグ一覧を返す。
func ListTemplates(basePrefix string) ([]string, error) {
	out, err := runCLI(cliTimeout, "template", "ls")
	if err != nil {
		return nil, err
	}
	var result []string
	for _, line := range strings.Split(string(out), "\n") {
		for _, ref := range templateRefs(line) {
			if strings.HasPrefix(ref, basePrefix+":") {
				result = append(result, ref)
			}
		}
	}
	return result, nil
}

// RemoveTemplate はロード済みテンプレートを削除する。
func RemoveTemplate(tag string) error {
	_, err := runCLI(cliTimeout, "template", "rm", tag)
	return err
}

// templateRefs は template ls の1行から name:tag 形式の参照を取り出す。
// 出力形式が「repository:tag ...」のように結合されている場合と、
// 「REPOSITORY TAG ...」のように列が分かれている場合の両方を許容する。
func templateRefs(line string) []string {
	fields := strings.Fields(line)
	var refs []string
	for i, f := range fields {
		if strings.Contains(f, ":") {
			refs = append(refs, f)
		} else if i+1 < len(fields) {
			refs = append(refs, f+":"+fields[i+1])
		}
	}
	return refs
}
