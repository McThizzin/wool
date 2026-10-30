package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"wool/internal/config"
)

// Spec describes one launch. Args is a builder because the manager may have
// to pick a different port than requested.
type Spec struct {
	Name     string
	Kind     Kind
	Preset   string
	Model    string
	Bin      string
	Host     string
	Port     int
	AutoPort bool
	Env      []string
	Args     func(port int) []string
}

// Manager owns state files and detached processes.
type Manager struct {
	StateDir string
	LogDir   string
	mu       sync.Mutex
}

// NewManager returns a manager rooted at the XDG state directory.
func NewManager() *Manager {
	return &Manager{StateDir: config.ServersDir(), LogDir: config.LogsDir()}
}

// NewManagerWithDirs is for tests and unusual layouts.
func NewManagerWithDirs(stateDir, logDir string) *Manager {
	return &Manager{StateDir: stateDir, LogDir: logDir}
}

// Start launches a detached process and records its identity.
func (m *Manager) Start(spec Spec) (State, error) {
	var zero State
	if err := ValidateName(spec.Name); err != nil {
		return zero, err
	}
	if spec.Bin == "" {
		return zero, fmt.Errorf("no llama-server binary")
	}
	if spec.Args == nil {
		return zero, fmt.Errorf("missing argument builder")
	}
	if spec.Kind == "" {
		spec.Kind = KindServer
	}
	if spec.Host == "" {
		spec.Host = "127.0.0.1"
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if prev, err := Load(m.StateDir, spec.Name); err == nil {
		if Alive(prev.PID, prev.StartTicks) {
			return zero, fmt.Errorf("%s is already running (pid %d, port %d)", spec.Name, prev.PID, prev.Port)
		}
		if err := removeState(m.StateDir, spec.Name); err != nil {
			return zero, err
		}
	} else if !errors.Is(err, ErrNotRunning) {
		return zero, err
	}

	bin, err := exec.LookPath(spec.Bin)
	if err != nil {
		return zero, fmt.Errorf("find %s: %w", spec.Bin, err)
	}
	if err := os.MkdirAll(m.StateDir, 0o700); err != nil {
		return zero, err
	}
	if err := os.MkdirAll(m.LogDir, 0o700); err != nil {
		return zero, err
	}

	port, err := ChoosePort(spec.Host, spec.Port, spec.AutoPort)
	if err != nil {
		return zero, err
	}
	args := spec.Args(port)
	logPath := filepath.Join(m.LogDir, spec.Name+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return zero, err
	}
	header := fmt.Sprintf("=== %s starting %s %s on %s:%d ===\n$ %s\n",
		time.Now().Format(time.RFC3339), spec.Kind, spec.Name, displayHost(spec.Host), port,
		renderCommand(bin, args))
	if _, err := logFile.WriteString(header); err != nil {
		logFile.Close()
		return zero, err
	}

	cmd := exec.Command(bin, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return zero, fmt.Errorf("start %s: %w", bin, err)
	}
	logFile.Close()

	// Reap the child while this process lives; otherwise dead servers become
	// zombies until the TUI exits.
	go func() { _ = cmd.Wait() }()

	pid := cmd.Process.Pid
	pgid, _ := syscall.Getpgid(pid)
	var ticks uint64
	for i := 0; i < 20; i++ {
		ticks, err = StartTicks(pid)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	state := State{
		Name:       spec.Name,
		Kind:       spec.Kind,
		Preset:     spec.Preset,
		Model:      spec.Model,
		Host:       spec.Host,
		Port:       port,
		PID:        pid,
		PGID:       pgid,
		StartTicks: ticks,
		StartedAt:  time.Now(),
		LogPath:    logPath,
		Bin:        bin,
		Args:       args,
	}
	if err := saveState(m.StateDir, state); err != nil {
		_ = killGroup(state, syscall.SIGKILL)
		return zero, fmt.Errorf("record state: %w", err)
	}
	return state, nil
}

// Stop terminates the process group, escalating to SIGKILL after timeout.
func (m *Manager) Stop(name string, timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := Load(m.StateDir, name)
	if err != nil {
		return err
	}
	if !Alive(s.PID, s.StartTicks) {
		return removeState(m.StateDir, name)
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if err := killGroup(s, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal %s: %w", name, err)
	}
	if waitDead(s, timeout) {
		return removeState(m.StateDir, name)
	}
	if err := killGroup(s, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill %s: %w", name, err)
	}
	if waitDead(s, 3*time.Second) {
		return removeState(m.StateDir, name)
	}
	return fmt.Errorf("%s (pid %d) survived SIGKILL", name, s.PID)
}

func waitDead(s State, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !Alive(s.PID, s.StartTicks) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Status returns a state file plus a live check.
func (m *Manager) Status(name string) (Instance, error) {
	s, err := Load(m.StateDir, name)
	if err != nil {
		return Instance{}, err
	}
	return Instance{State: s, Alive: Alive(s.PID, s.StartTicks)}, nil
}

// List returns all recorded instances, newest first.
func (m *Manager) List() ([]Instance, error) {
	entries, err := os.ReadDir(m.StateDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Instance
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		inst, err := m.Status(name)
		if err != nil {
			continue
		}
		out = append(out, inst)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}

// Cleanup removes state files whose processes were reaped or reused.
func (m *Manager) Cleanup() error {
	instances, err := m.List()
	if err != nil {
		return err
	}
	for _, inst := range instances {
		if !inst.Alive {
			_ = removeState(m.StateDir, inst.Name)
		}
	}
	return nil
}

// Dismiss removes the state for an already-dead instance.
func (m *Manager) Dismiss(name string) error {
	inst, err := m.Status(name)
	if err != nil {
		return err
	}
	if inst.Alive {
		return fmt.Errorf("%s is still running", name)
	}
	return removeState(m.StateDir, name)
}

// LogPath returns the log path recorded for an instance.
func (m *Manager) LogPath(name string) (string, error) {
	s, err := Load(m.StateDir, name)
	if err != nil {
		return "", err
	}
	return s.LogPath, nil
}

// Tail returns the last n log lines for an instance.
func (m *Manager) Tail(name string, n int) ([]string, error) {
	path, err := m.LogPath(name)
	if err != nil {
		return nil, err
	}
	return LogTail(path, n)
}

// ChoosePort picks a port. With auto it walks up from the requested port and
// finally asks the kernel for an ephemeral one; without auto a busy port is an
// error.
func ChoosePort(host string, desired int, auto bool) (int, error) {
	h := listenHost(host)
	if desired > 0 && !auto {
		if portFree(h, desired) {
			return desired, nil
		}
		return 0, fmt.Errorf("port %d is already in use", desired)
	}
	if desired > 0 {
		for p := desired; p < desired+50 && p <= 65535; p++ {
			if portFree(h, p) {
				return p, nil
			}
		}
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(h, "0"))
	if err != nil {
		return 0, fmt.Errorf("find free port: %w", err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p, nil
}

func portFree(host string, port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func listenHost(host string) string {
	h := strings.TrimSpace(strings.Split(host, ",")[0])
	switch h {
	case "", "*":
		return "0.0.0.0"
	case "[::]":
		return "::"
	}
	return strings.Trim(h, "[]")
}

func renderCommand(bin string, args []string) string {
	parts := append([]string{bin}, args...)
	for i, p := range parts {
		if strings.ContainsAny(p, " \t\n'\"$`") {
			parts[i] = "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
		}
	}
	return strings.Join(parts, " ")
}
