package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var keyName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// SetValue updates a single key inside a TOML table while preserving the rest
// of the file, including comments and ordering. It creates the table if it
// does not exist. value must already be TOML-formatted (for example `32768`
// or `"on"`).
func SetValue(path, table, key, value string) error {
	if !keyName.MatchString(table) {
		return fmt.Errorf("invalid TOML table %q", table)
	}
	if !keyName.MatchString(key) {
		return fmt.Errorf("invalid TOML key %q", key)
	}
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("value for %s cannot contain a newline", key)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	lines := strings.Split(string(data), "\n")
	section := regexp.MustCompile(`^\s*\[` + regexp.QuoteMeta(table) + `\]\s*(?:#.*)?$`)
	anyHeader := regexp.MustCompile(`^\s*\[.*\]\s*(?:#.*)?$`)
	keyLine := regexp.MustCompile(`^(\s*)` + regexp.QuoteMeta(key) + `\s*=`)

	start := -1
	for i, line := range lines {
		if section.MatchString(line) {
			start = i
			break
		}
	}

	if start < 0 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "["+table+"]", key+" = "+value)
	} else {
		end := len(lines)
		for i := start + 1; i < len(lines); i++ {
			if anyHeader.MatchString(lines[i]) {
				end = i
				break
			}
		}
		replaced := false
		for i := start + 1; i < end; i++ {
			if m := keyLine.FindStringSubmatch(lines[i]); m != nil {
				lines[i] = m[1] + key + " = " + value
				replaced = true
				break
			}
		}
		if !replaced {
			insert := start + 1
			for i := start + 1; i < end; i++ {
				if t := strings.TrimSpace(lines[i]); t != "" && !strings.HasPrefix(t, "#") {
					insert = i + 1
				}
			}
			lines = append(lines[:insert], append([]string{key + " = " + value}, lines[insert:]...)...)
		}
	}

	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(string(data), "\n") && strings.HasSuffix(out, "\n") {
		out = strings.TrimSuffix(out, "\n")
	}
	return writeFileAtomic(path, []byte(out), mode)
}

// RemoveValue deletes one scalar key from a TOML table while preserving the
// rest of the file. The table header is left in place even when the table
// becomes empty, so comments around it are not guessed away. Removing a
// missing table or key is a no-op. Multi-line and inline collection values are
// rejected because a line-based remover cannot remove them safely.
func RemoveValue(path, table, key string) error {
	if !keyName.MatchString(table) {
		return fmt.Errorf("invalid TOML table %q", table)
	}
	if !keyName.MatchString(key) {
		return fmt.Errorf("invalid TOML key %q", key)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	lines := strings.Split(string(data), "\n")
	section := regexp.MustCompile(`^\s*\[` + regexp.QuoteMeta(table) + `\]\s*(?:#.*)?$`)
	anyHeader := regexp.MustCompile(`^\s*\[.*\]\s*(?:#.*)?$`)
	keyLine := regexp.MustCompile(`^(\s*)` + regexp.QuoteMeta(key) + `\s*=\s*(.*)$`)

	start := -1
	for i, line := range lines {
		if section.MatchString(line) {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if anyHeader.MatchString(lines[i]) {
			end = i
			break
		}
	}
	for i := start + 1; i < end; i++ {
		m := keyLine.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(m[2]), `"""`) ||
			strings.HasPrefix(strings.TrimSpace(m[2]), `'''`) ||
			strings.HasPrefix(strings.TrimSpace(m[2]), "[") ||
			strings.HasPrefix(strings.TrimSpace(m[2]), "{") {
			return fmt.Errorf("%s.%s is not a scalar value", table, key)
		}
		lines = append(lines[:i], lines[i+1:]...)
		return writeFileAtomic(path, []byte(strings.Join(lines, "\n")), mode)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
