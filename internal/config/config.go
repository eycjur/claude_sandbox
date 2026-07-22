package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config は config.toml 全体の構造。
type Config struct {
	Dotfiles DotfilesConfig `toml:"dotfiles"`
}

// DotfilesConfig はサンドボックス作成時に適用する dotfiles の設定。
type DotfilesConfig struct {
	Repository     string `toml:"repository"`
	TargetPath     string `toml:"target_path"`
	InstallCommand string `toml:"install_command"`
}

// Root はデータディレクトリ ~/.agentsb を返す（home / build / logs など）。
func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".agentsb"), nil
}

// ConfigDir は設定ディレクトリを返す。
// $XDG_CONFIG_HOME/agentsb、未設定なら ~/.config/agentsb。
func ConfigDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "agentsb"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "agentsb"), nil
}

// GlobalPath は設定ファイルパス（~/.config/agentsb/config.toml）を返す。
func GlobalPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// Load はデフォルト値の上に config.toml の内容を重ねて返す。ファイルが無くてもよい。
func Load() (Config, error) {
	cfg := Config{}
	path, err := GlobalPath()
	if err != nil {
		return cfg, err
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	} else if err != nil {
		return cfg, err
	}

	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, fmt.Errorf("invalid config %s: %w", path, err)
	}
	// repository だけ書いた場合でも動くよう、install の既定名を補う。
	if cfg.Dotfiles.Repository != "" && cfg.Dotfiles.InstallCommand == "" {
		cfg.Dotfiles.InstallCommand = "install.sh"
	}
	return cfg, nil
}
