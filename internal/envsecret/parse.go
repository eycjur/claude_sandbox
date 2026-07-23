// Package envsecret は secrets.toml を sbx global へプロキシ注入する。
// 組み込みは secret set -g、それ以外は set-custom -g。内容が同じなら set をスキップする。
// 取得元は config.toml の [secrets]（file または 1Password Secure Note）。
package envsecret

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"

	"agentsb/internal/config"
)

const fileName = "secrets.toml"

// builtinByEnv は sbx 組み込みサービス名。set-custom ではなく secret set -g で登録する。
var builtinByEnv = map[string]string{
	"OPENAI_API_KEY":     "openai",
	"ANTHROPIC_API_KEY":  "anthropic",
	"GOOGLE_API_KEY":     "google",
	"GEMINI_API_KEY":     "google",
	"GROQ_API_KEY":       "groq",
	"MISTRAL_API_KEY":    "mistral",
	"OPENROUTER_API_KEY": "openrouter",
	"XAI_API_KEY":        "xai",
	"GITHUB_TOKEN":       "github",
	"GH_TOKEN":           "github",
}

type file struct {
	Secret []Secret `toml:"secret"`
}

// Secret はプロキシ置換する 1 シークレット。
type Secret struct {
	Name    string   `toml:"name"`
	Value   string   `toml:"value"`
	Domains []string `toml:"domains"` // カスタム必須。組み込みは不要
}

// Path は ~/.config/agentsb/secrets.toml を返す。
func Path() (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// loadSource は設定に従いシークレット本文と表示用ラベルを返す。
// 無ければ (nil, "", nil)。
func loadSource() (secrets []Secret, label string, raw []byte, err error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", nil, err
	}
	src := strings.ToLower(strings.TrimSpace(cfg.Secrets.Source))
	if src == "" {
		return loadFile()
	}
	if src != "1password" {
		return nil, "", nil, fmt.Errorf("config [secrets]: source must be \"1password\" (got %q)", cfg.Secrets.Source)
	}
	ref := strings.TrimSpace(cfg.Secrets.Ref)
	if ref == "" {
		return nil, "", nil, fmt.Errorf("config [secrets]: source=%q requires ref (e.g. op://Vault/Item/notesPlain)", cfg.Secrets.Source)
	}
	raw, err = readOnePassword(ref)
	if err != nil {
		return nil, "", nil, err
	}
	secrets, err = parseSecrets(raw, ref)
	if err != nil {
		return nil, "", nil, err
	}
	return secrets, ref, raw, nil
}

func loadFile() ([]Secret, string, []byte, error) {
	path, err := Path()
	if err != nil {
		return nil, "", nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, path, nil, nil
		}
		return nil, "", nil, err
	}
	secrets, err := parseSecrets(data, path)
	if err != nil {
		return nil, "", nil, err
	}
	return secrets, path, data, nil
}

func readOnePassword(ref string) ([]byte, error) {
	if _, err := exec.LookPath("op"); err != nil {
		return nil, fmt.Errorf("1Password CLI (op) not found in PATH")
	}
	cmd := exec.Command("op", "read", ref)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("op read %s: %w: %s", ref, err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("op read %s: %w", ref, err)
	}
	return out, nil
}

func parseSecrets(data []byte, label string) ([]Secret, error) {
	var f file
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("invalid secrets from %s: %w", label, err)
	}
	for i := range f.Secret {
		s := &f.Secret[i]
		if s.Name == "" {
			return nil, fmt.Errorf("%s: secret[%d]: name is required", label, i)
		}
		if s.Value == "" {
			return nil, fmt.Errorf("%s: secret[%d] (%s): value is required", label, i, s.Name)
		}
		_, builtin := builtinByEnv[s.Name]
		if !builtin && len(s.Domains) == 0 {
			return nil, fmt.Errorf("%s: secret[%d] (%s): domains is required", label, i, s.Name)
		}
		for j, d := range s.Domains {
			d = strings.TrimSpace(d)
			if d == "" {
				return nil, fmt.Errorf("%s: secret[%d] (%s): empty domain", label, i, s.Name)
			}
			s.Domains[j] = d
		}
	}
	return f.Secret, nil
}

// placeholderFor は env 名から決定的なプレースホルダを返す（DEEPL_API_KEY → sbx-cs-DEEPLAPIKEY）。
func placeholderFor(env string) string {
	var b strings.Builder
	b.WriteString("sbx-cs-")
	for _, r := range env {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// execEnv はカスタムシークレット用の KEY=placeholder（sbx exec -e 向け）。
func execEnv(secrets []Secret) []string {
	var out []string
	for _, s := range secrets {
		if _, ok := builtinByEnv[s.Name]; ok {
			continue
		}
		out = append(out, s.Name+"="+placeholderFor(s.Name))
	}
	return out
}
