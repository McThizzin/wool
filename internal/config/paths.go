package config

import (
	"os"
	"path/filepath"
	"strings"
)

const appDirName = "wool"

// ConfigDir returns the XDG config directory for wool.
func ConfigDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, appDirName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", appDirName)
}

// DefaultPath is the default config file location.
func DefaultPath() string {
	return filepath.Join(ConfigDir(), "config.toml")
}

// StateDir returns the XDG state directory for wool.
func StateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, appDirName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", appDirName)
}

// CacheDir returns the XDG cache directory for wool.
func CacheDir() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, appDirName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", appDirName)
}

func ServersDir() string { return filepath.Join(StateDir(), "servers") }
func LogsDir() string    { return filepath.Join(StateDir(), "logs") }
func RouterDir() string  { return filepath.Join(StateDir(), "router") }
func RouterINIPath() string {
	return filepath.Join(RouterDir(), "models.ini")
}

// EnsureStateDirs creates the state directories used by the daemon.
func EnsureStateDirs() error {
	for _, dir := range []string{StateDir(), ServersDir(), LogsDir(), RouterDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// ExpandPath expands ~ and ~/ prefixes. It does not resolve symlinks.
func ExpandPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

// AbsPath expands ~ and returns an absolute path when possible.
func AbsPath(path string) string {
	p := ExpandPath(path)
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
