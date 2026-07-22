// Package image はサンドボックス用テンプレートイメージのビルドとロードを担当
// する。sbx はホストの Docker のイメージストアを共有しないため、ビルドは
// `docker build` → `docker image save` → `sbx template load` の 3 段階で行う
package image

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"agentsb/internal/config"
	"agentsb/internal/runlog"
	"agentsb/internal/sandbox"
)

const imageBase = "agentsb-base"

// containerfile はバイナリに埋め込まれたサンドボックスのイメージ定義。
// これが唯一の正で、ユーザーが編集する想定はない。変更はリポジトリの
// internal/image/Containerfile を編集して agentsb を入れ直す。
//
//go:embed Containerfile
var containerfile string

// Tag は埋め込み Containerfile に対応するテンプレートタグを返す。
// タグには定義内容のハッシュが含まれるため、Containerfile の変更は
// 新しいタグとして表れる（新規ビルド判定に使う）。
func Tag() string {
	sum := sha256.Sum256([]byte(containerfile))
	return fmt.Sprintf("%s:%x", imageBase, sum[:6])
}

// CheckDocker はビルドに必要な `docker` CLI が使えることを確認する。
// docker はテンプレートのビルド時だけ必要で、run など通常の操作では不要。
func CheckDocker() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("the `docker` CLI is required to build the template image — install Docker Desktop or Docker Engine")
	}
	return nil
}

// EnsureBuilt は埋め込み Containerfile に対応するテンプレートタグを返し、
// sbx にロード済みでなければビルドしてロードする。force=true なら無条件で
// リビルドする（ハッシュでは検知できない上流ベースイメージの更新を取り込む）。
func EnsureBuilt(force bool) (string, error) {
	tag := Tag()

	if !force && sandbox.TemplateExists(tag) {
		runlog.Info("template %s already loaded", tag)
		return tag, nil
	}
	if err := CheckDocker(); err != nil {
		return "", err
	}
	cf, err := writeBuildContext()
	if err != nil {
		return "", err
	}
	runlog.Info("building template %s force=%v containerfile=%s", tag, force, cf)
	fmt.Fprintf(os.Stderr, "agentsb: building %s (this may take a few minutes)…\n", tag)
	if err := buildImage(cf, filepath.Dir(cf), tag); err != nil {
		return "", fmt.Errorf("image build failed: %w", err)
	}

	tarPath := filepath.Join(filepath.Dir(cf), "template.tar")
	if err := saveImage(tag, tarPath); err != nil {
		return "", fmt.Errorf("image save failed: %w", err)
	}
	defer os.Remove(tarPath)

	fmt.Fprintf(os.Stderr, "agentsb: loading %s into the sandbox runtime…\n", tag)
	if err := sandbox.LoadTemplate(tarPath); err != nil {
		return "", err
	}
	if force {
		pruneSuperseded(tag)
	}
	return tag, nil
}

// writeBuildContext は埋め込み Containerfile を ~/.agentsb/build/Containerfile
// へ書き出し、そのパスを返す。専用ディレクトリを使うのは、home/
// （認証情報）を決してビルドコンテキストに入れないため。
func writeBuildContext() (string, error) {
	root, err := config.Root()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "build")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "Containerfile")
	if err := os.WriteFile(path, []byte(containerfile), 0644); err != nil {
		return "", fmt.Errorf("cannot write %s: %w", path, err)
	}
	return path, nil
}

// buildImage は Containerfile からホストの Docker でイメージをビルドする。
// ビルドログは stderr へ流す。プラットフォームは指定せずホストネイティブで
// ビルドする（sbx の microVM はホストと同じアーキテクチャ）。
func buildImage(containerfile, contextDir, tag string) error {
	args := []string{"build", "-f", containerfile, "-t", tag, contextDir}
	runlog.Info("docker %s", strings.Join(args, " "))
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		runlog.Warn("docker build failed: %v", err)
		return err
	}
	runlog.Info("built image %s", tag)
	return nil
}

// saveImage はビルド済みイメージを tar へ書き出す（`sbx template load` 用）。
func saveImage(tag, tarPath string) error {
	args := []string{"image", "save", tag, "-o", tarPath}
	runlog.Info("docker %s", strings.Join(args, " "))
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		runlog.Warn("docker image save failed: %v: %s", err, strings.TrimSpace(string(out)))
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeleteAll は agentsb-base の全テンプレートとホスト Docker 側のイメージを
// 削除する。`agentsb prune` から呼ばれる。
func DeleteAll() error {
	var firstErr error
	if tags, err := sandbox.ListTemplates(imageBase); err != nil {
		firstErr = err
	} else {
		for _, tag := range tags {
			if err := sandbox.RemoveTemplate(tag); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("remove template %s: %w", tag, err)
			}
		}
	}
	for _, tag := range dockerListImages(imageBase) {
		if err := dockerRemoveImage(tag); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("remove docker image %s: %w", tag, err)
		}
	}
	return firstErr
}

// pruneSuperseded は current 以外の agentsb-base テンプレート・イメージを
// 削除する。サンドボックスは作成時点でテンプレートから独立する（microVM 側に
// 展開される）ため、既存サンドボックスが使用中かの保護判定は不要。
func pruneSuperseded(current string) {
	tags, err := sandbox.ListTemplates(imageBase)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agentsb: warning: could not list templates to prune: %v\n", err)
	} else {
		for _, tag := range tags {
			if tag == current {
				continue
			}
			if err := sandbox.RemoveTemplate(tag); err != nil {
				fmt.Fprintf(os.Stderr, "agentsb: warning: could not prune template %s: %v\n", tag, err)
			} else {
				fmt.Fprintf(os.Stderr, "agentsb: pruned superseded template %s\n", tag)
			}
		}
	}
	for _, tag := range dockerListImages(imageBase) {
		if tag == current {
			continue
		}
		if err := dockerRemoveImage(tag); err != nil {
			fmt.Fprintf(os.Stderr, "agentsb: warning: could not prune docker image %s: %v\n", tag, err)
		}
	}
}

// dockerListImages はホスト Docker 側の basePrefix イメージのタグ一覧を返す。
// docker が無い・失敗した場合は空を返す（prune の対象が無いだけで害はない）。
func dockerListImages(basePrefix string) []string {
	out, err := exec.Command("docker", "image", "ls", "--format", "{{.Repository}}:{{.Tag}}").Output()
	if err != nil {
		runlog.Warn("docker image ls failed: %v", err)
		return nil
	}
	var result []string
	for _, line := range strings.Split(string(out), "\n") {
		if tag := strings.TrimSpace(line); strings.HasPrefix(tag, basePrefix+":") {
			result = append(result, tag)
		}
	}
	return result
}

// dockerRemoveImage はホスト Docker 側のイメージを削除する。
func dockerRemoveImage(tag string) error {
	runlog.Info("docker image rm %s", tag)
	out, err := exec.Command("docker", "image", "rm", tag).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
