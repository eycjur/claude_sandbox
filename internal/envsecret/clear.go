package envsecret

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"agentsb/internal/runlog"
)

// Clear は sbx に登録済みのシークレットをすべて削除し、同期ハッシュも消す。
func Clear() error {
	out, err := listSecrets()
	if err != nil {
		return err
	}
	services, customs := parseSecretList(out)
	if len(services) == 0 && len(customs) == 0 {
		fmt.Fprintln(os.Stderr, "agentsb: no sbx secrets to remove")
		_ = clearSyncHash()
		return nil
	}

	var errs []string
	for _, s := range services {
		args := []string{"secret", "rm", "-f"}
		logged := []string{"secret", "rm", "-f"}
		if s.Global {
			args = append(args, "-g", s.Name)
			logged = append(logged, "-g", s.Name)
		} else {
			args = append(args, s.Scope, s.Name)
			logged = append(logged, s.Scope, s.Name)
		}
		if err := runSbx(args, logged); err != nil {
			errs = append(errs, fmt.Sprintf("rm %s: %v", strings.Join(logged, " "), err))
		}
	}
	for _, c := range customs {
		var args []string
		if c.Global {
			args = []string{"secret", "rm", "-g", "-f", "--placeholder", c.Placeholder}
		} else {
			args = []string{"secret", "rm", c.Scope, "-f", "--placeholder", c.Placeholder}
		}
		if err := runSbx(args, args); err != nil {
			errs = append(errs, fmt.Sprintf("rm placeholder %s: %v", c.Placeholder, err))
		}
	}
	if err := clearSyncHash(); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("secrets clear finished with errors: %s", strings.Join(errs, "; "))
	}
	fmt.Fprintf(os.Stderr, "agentsb: removed %d service + %d custom secret(s)\n", len(services), len(customs))
	return nil
}

type listedService struct {
	Global bool
	Scope  string // sandbox name when !Global
	Name   string
}

type listedCustom struct {
	Global      bool
	Scope       string
	Placeholder string
}

func listSecrets() (string, error) {
	runlog.Info("sbx secret ls")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sbx", "secret", "ls")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return "", fmt.Errorf("sbx secret ls: %w: %s", err, detail)
		}
		return "", fmt.Errorf("sbx secret ls: %w", err)
	}
	return string(out), nil
}

func clearSyncHash() error {
	path, err := syncHashPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// parseSecretList は `sbx secret ls` のテキスト出力から削除対象を拾う。
func parseSecretList(out string) (services []listedService, customs []listedCustom) {
	lines := strings.Split(out, "\n")
	section := "service" // service | custom
	var customHeader string
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if strings.EqualFold(trim, "CUSTOM SECRETS") {
			section = "custom"
			customHeader = ""
			continue
		}
		if section == "service" {
			if strings.HasPrefix(trim, "SCOPE") && strings.Contains(line, "TYPE") {
				continue
			}
			fields := strings.Fields(line)
			// SCOPE TYPE NAME SECRET…
			if len(fields) < 3 || fields[1] != "service" {
				continue
			}
			scope, name := fields[0], fields[2]
			if scope == "(global)" {
				services = append(services, listedService{Global: true, Name: name})
			} else {
				services = append(services, listedService{Scope: scope, Name: name})
			}
			continue
		}
		// custom
		if strings.HasPrefix(trim, "SCOPE") && strings.Contains(line, "PLACEHOLDER") {
			customHeader = line
			continue
		}
		if customHeader == "" {
			continue
		}
		ph := columnAt(customHeader, line, "PLACEHOLDER", "SECRET")
		if ph == "" {
			continue
		}
		scope := columnAt(customHeader, line, "SCOPE", "TARGETS")
		if scope == "(global)" || scope == "" {
			customs = append(customs, listedCustom{Global: true, Placeholder: ph})
		} else {
			customs = append(customs, listedCustom{Scope: scope, Placeholder: ph})
		}
	}
	return services, customs
}

// columnAt はヘッダ上の from〜to 列に対応する行の部分文字列を返す。
func columnAt(header, line, from, to string) string {
	start := strings.Index(header, from)
	if start < 0 {
		return ""
	}
	end := len(line)
	if to != "" {
		if i := strings.Index(header, to); i > start {
			end = i
		}
	}
	if start >= len(line) {
		return ""
	}
	if end > len(line) {
		end = len(line)
	}
	return strings.TrimSpace(line[start:end])
}
