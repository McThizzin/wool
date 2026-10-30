package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"wool/internal/config"
)

// Kind distinguishes the two launch modes.
type Kind string

const (
	KindServer Kind = "server"
	KindRouter Kind = "router"
)

// ErrNotRunning means there is no state file for the requested instance.
var ErrNotRunning = errors.New("not running")

// State is persisted next to the log so a later TUI or CLI invocation can
// reconnect to a process it did not spawn.
type State struct {
	Name       string    `json:"name"`
	Kind       Kind      `json:"kind"`
	Preset     string    `json:"preset,omitempty"`
	Model      string    `json:"model,omitempty"`
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	PID        int       `json:"pid"`
	PGID       int       `json:"pgid"`
	StartTicks uint64    `json:"start_ticks"`
	StartedAt  time.Time `json:"started_at"`
	LogPath    string    `json:"log_path"`
	Bin        string    `json:"bin"`
	Args       []string  `json:"args"`
}

// Instance is a state plus a live check.
type Instance struct {
	State
	Alive bool `json:"alive"`
}

// URL is the HTTP endpoint for this instance.
func (s State) URL() string {
	return fmt.Sprintf("http://%s:%d", displayHost(s.Host), s.Port)
}

func displayHost(host string) string {
	h := strings.TrimSpace(strings.Split(host, ",")[0])
	switch h {
	case "", "0.0.0.0", "*":
		return "127.0.0.1"
	case "::", "[::]":
		return "[::1]"
	}
	if strings.Contains(h, ":") && !strings.HasPrefix(h, "[") {
		return "[" + h + "]"
	}
	return h
}

func statePath(dir, name string) string {
	return filepath.Join(dir, name+".json")
}

// Load reads one state file without checking whether the process is alive.
func Load(dir, name string) (State, error) {
	var s State
	b, err := os.ReadFile(statePath(dir, name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, ErrNotRunning
		}
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("read state %s: %w", name, err)
	}
	return s, nil
}

func saveState(dir string, s State) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, statePath(dir, s.Name))
}

func removeState(dir, name string) error {
	err := os.Remove(statePath(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// procStat parses the fields of /proc/<pid>/stat that survive a reused PID:
// state character and start time in clock ticks.
type procStat struct {
	state      byte
	startTicks uint64
}

func readProcStat(pid int) (procStat, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return procStat{}, err
	}
	return parseProcStat(string(b))
}

func parseProcStat(s string) (procStat, error) {
	// comm can contain spaces and parentheses, so split after the last ')'.
	end := strings.LastIndexByte(s, ')')
	if end < 0 || end+2 > len(s) {
		return procStat{}, fmt.Errorf("malformed stat")
	}
	fields := strings.Fields(s[end+1:])
	// fields[0] is state (field 3); starttime is field 22, fields[19].
	if len(fields) < 20 {
		return procStat{}, fmt.Errorf("stat has %d fields", len(fields))
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return procStat{}, err
	}
	return procStat{state: fields[0][0], startTicks: ticks}, nil
}

// StartTicks returns the process start time, used to validate a saved PID.
func StartTicks(pid int) (uint64, error) {
	st, err := readProcStat(pid)
	if err != nil {
		return 0, err
	}
	return st.startTicks, nil
}

// Alive reports whether pid still exists and started at the recorded tick.
// A matching start time prevents signaling a recycled PID.
func Alive(pid int, startTicks uint64) bool {
	if pid <= 0 {
		return false
	}
	st, err := readProcStat(pid)
	if err != nil {
		return false
	}
	if st.state == 'Z' || st.state == 'X' {
		return false
	}
	return startTicks == 0 || st.startTicks == startTicks
}

// ValidateName rejects names that could escape the state directory.
func ValidateName(name string) error {
	if name == "" || len(name) > 64 || strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid instance name %q", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("invalid instance name %q", name)
		}
	}
	return nil
}

// killGroup sends a signal to the whole session/group, falling back to the
// single process if the group is unknown.
func killGroup(s State, sig syscall.Signal) error {
	if s.PGID > 0 {
		if err := syscall.Kill(-s.PGID, sig); err == nil {
			return nil
		} else if !errors.Is(err, syscall.ESRCH) {
			return err
		}
	}
	return syscall.Kill(s.PID, sig)
}

// LogTail returns the last n lines of the instance log.
func LogTail(path string, n int) ([]string, error) {
	if n <= 0 {
		n = 100
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const maxRead = 512 << 10
	start := int64(0)
	if info.Size() > maxRead {
		start = info.Size() - maxRead
		// Drop a partial first line after seeking into the middle.
		if _, err := f.Seek(start, 0); err != nil {
			return nil, err
		}
		buf := make([]byte, 1)
		for {
			if _, err := f.Read(buf); err != nil {
				break
			}
			if buf[0] == '\n' {
				break
			}
		}
	} else if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	data := make([]byte, 0, 64<<10)
	tmp := make([]byte, 32<<10)
	for {
		nRead, err := f.Read(tmp)
		if nRead > 0 {
			data = append(data, tmp[:nRead]...)
		}
		if err != nil {
			break
		}
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// StateDir is exposed for callers that need to list files directly.
func StateDir() string { return config.ServersDir() }
