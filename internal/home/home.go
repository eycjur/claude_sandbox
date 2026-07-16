package home

import (
	"fmt"
	"os"
	"path/filepath"

	"agentsb/internal/config"
	"agentsb/internal/container"
)

// credentialSpec は同期対象ファイル 1 つ分の設定。
type credentialSpec struct {
	// relPath は home からの相対パス。
	relPath string
	// syncIfNewer が true の場合、セッション終了時にコンテナ側の mtime が
	// ホスト側より新しいときだけホストへ書き戻す（ホスト側の手動編集を
	// 古いコンテナ側の内容で上書きしないため）。false の場合は無条件で
	// 上書きする（OAuth トークンのような、後勝ちで問題ない値向け）。
	syncIfNewer bool
}

// basePath は ~/.agentsb/home（認証情報を永続化するディレクトリ）を返す。
func basePath() (string, error) {
	root, err := config.Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "home"), nil
}

// CredentialFile は認証情報ファイル 1 つ分の、ホスト側パスとコンテナ側の
// 絶対パスの組。
type CredentialFile struct {
	HostPath      string
	ContainerPath string
	// SyncIfNewer は ExtractCredentials 時の書き戻し条件。詳細は
	// credentialSpec.syncIfNewer を参照。
	SyncIfNewer bool
}

// credentialSpecs は同期対象ファイルの一覧。.claude/.credentials.json は
// セッション中にリフレッシュされる OAuth トークンで後勝ちで問題ない。
// .claude.json（オンボーディングや設定の状態）はホスト側の手動編集を尊重
// したいため、コンテナ側で更新された場合のみ書き戻す。
var credentialSpecs = []credentialSpec{
	{relPath: filepath.Join(".claude", ".credentials.json"), syncIfNewer: false},
	{relPath: ".claude.json", syncIfNewer: true},
}

// EnsureCredentialFiles はコピー先ディレクトリの存在を保証し、コンテナとの
// コピーに使う情報を返す。ホスト側ファイル自体は無ければ作らない — 存在しな
// いなら InjectCredentials 側でコピーをスキップする（空ファイルで上書きしな
// いため）。bind mount ではなく `container cp` を使うのは、コンテナ内の他の
// 状態（イメージに焼き込んだものなど）をマウントで隠さないため。
func EnsureCredentialFiles() ([]CredentialFile, error) {
	base, err := basePath()
	if err != nil {
		return nil, err
	}
	files := make([]CredentialFile, len(credentialSpecs))
	for i, spec := range credentialSpecs {
		p := filepath.Join(base, spec.relPath)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return nil, fmt.Errorf("cannot prepare %s: %w", p, err)
		}
		files[i] = CredentialFile{
			HostPath:      p,
			ContainerPath: filepath.Join(container.HomeDir, spec.relPath),
			SyncIfNewer:   spec.syncIfNewer,
		}
	}
	return files, nil
}

// InjectCredentials はコンテナ起動直後に認証情報ファイルをコンテナへコピーする。
// ホスト側にファイルが無ければ（未オンボーディングなど）そのファイルはスキッ
// プする — 空ファイルでコンテナ側の状態を上書きしないため。`container cp` は
// 稼働中のコンテナにしか使えないため、呼び出し側は `container start` の後に
// これを呼ぶこと。コピー自体はコンテナ側の root が書き込むため、agent が読める
// よう uid/gid を chown で戻す。
// SyncIfNewer なファイルは、コピー直後にコンテナ側の mtime をホスト側の値に
// 揃える。`container cp` はコピー時刻を mtime にするため、揃えておかないと
// セッション中に中身が変わっていなくても ExtractCredentials が「コンテナ側
// の方が新しい」と誤判定してしまう。
func InjectCredentials(runName string, files []CredentialFile, uid, gid int) error {
	for _, f := range files {
		hostInfo, err := os.Stat(f.HostPath)
		if os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("cannot stat %s: %w", f.HostPath, err)
		}
		if err := container.CopyToContainer(runName, f.HostPath, f.ContainerPath); err != nil {
			return fmt.Errorf("cannot inject %s: %w", f.ContainerPath, err)
		}
		if err := container.Chown(runName, f.ContainerPath, uid, gid); err != nil {
			return fmt.Errorf("cannot fix ownership of %s: %w", f.ContainerPath, err)
		}
		if f.SyncIfNewer {
			if err := container.SetModTime(runName, f.ContainerPath, hostInfo.ModTime()); err != nil {
				return fmt.Errorf("cannot align mtime of %s: %w", f.ContainerPath, err)
			}
		}
	}
	return nil
}

// ExtractCredentials はセッション終了後、コンテナ内の認証情報ファイルをホストへ
// 書き戻す。一時ファイル + アトミックな rename を経由するため、並行するセッ
// ションが同時に終了しても書きかけのファイルは生じない。SyncIfNewer でない
// ファイル（OAuth トークンなど）は後勝ち（latest-wins）で無条件に上書きする。
// SyncIfNewer なファイルはコンテナ側の mtime がホスト側より新しい場合だけ
// 書き戻す。
func ExtractCredentials(runName string, files []CredentialFile) error {
	var firstErr error
	for _, f := range files {
		if err := extractOne(runName, f); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func extractOne(runName string, f CredentialFile) error {
	exists, err := container.Exists(runName, f.ContainerPath)
	if err != nil {
		return fmt.Errorf("cannot check %s: %w", f.ContainerPath, err)
	}
	if !exists {
		return nil
	}

	if f.SyncIfNewer {
		newer, err := containerFileIsNewer(runName, f)
		if err != nil {
			return err
		}
		if !newer {
			return nil
		}
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(f.HostPath), ".agentsb-tmp-*")
	if err != nil {
		return err
	}
	tmp := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmp)

	if err := container.CopyFromContainer(runName, f.ContainerPath, tmp); err != nil {
		return fmt.Errorf("cannot extract %s: %w", f.ContainerPath, err)
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, f.HostPath)
}

// containerFileIsNewer はコンテナ側ファイルの mtime がホスト側より新しいかを
// 返す。ホスト側にファイルが無ければ（初回オンボーディングなど）無条件で
// true を返す。
func containerFileIsNewer(runName string, f CredentialFile) (bool, error) {
	hostInfo, err := os.Stat(f.HostPath)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("cannot stat %s: %w", f.HostPath, err)
	}
	containerMtime, err := container.ModTime(runName, f.ContainerPath)
	if err != nil {
		return false, fmt.Errorf("cannot stat container %s: %w", f.ContainerPath, err)
	}
	return containerMtime.After(hostInfo.ModTime()), nil
}
