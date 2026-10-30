package daemon

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseProcStat(t *testing.T) {
	// comm deliberately contains a space and parenthesis.
	line := "1234 (llama server (x)) S 1 1234 1234 0 -1 4194560 100 0 0 0 5 3 0 0 20 0 1 0 987654 12345 678 18446744073709551615"
	st, err := parseProcStat(line)
	if err != nil {
		t.Fatal(err)
	}
	if st.state != 'S' || st.startTicks != 987654 {
		t.Fatalf("stat = %+v", st)
	}
	if _, err := parseProcStat("bad"); err == nil {
		t.Fatal("expected malformed error")
	}
}

func TestAliveRejectsRecycledPID(t *testing.T) {
	ticks, err := StartTicks(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if !Alive(os.Getpid(), ticks) {
		t.Fatal("current process should be alive")
	}
	if Alive(os.Getpid(), ticks+1) {
		t.Fatal("mismatched start ticks must not be treated as alive")
	}
	if Alive(0, 0) || Alive(999999, 0) {
		t.Fatal("bogus pid should not be alive")
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"server", "router", "a.b-c_1"} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "../x", ".hidden", "a/b", "with space"} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestChoosePort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port

	if _, err := ChoosePort("127.0.0.1", busy, false); err == nil {
		t.Fatal("expected busy port error")
	}
	p, err := ChoosePort("127.0.0.1", busy, true)
	if err != nil {
		t.Fatal(err)
	}
	if p == busy || p <= 0 {
		t.Fatalf("auto port = %d, busy = %d", p, busy)
	}
	if _, err := ChoosePort("127.0.0.1", 0, true); err != nil {
		t.Fatal(err)
	}
}

func TestLogTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	var b strings.Builder
	for i := 1; i <= 200; i++ {
		b.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := LogTail(path, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[2] != "line 200" || lines[0] != "line 198" {
		t.Fatalf("lines = %v", lines)
	}
}

func testManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	return NewManagerWithDirs(filepath.Join(dir, "state"), filepath.Join(dir, "logs"))
}

func TestStartAndStopGroup(t *testing.T) {
	m := testManager(t)
	childFile := filepath.Join(t.TempDir(), "child.pid")
	script := "sleep 30 & echo $! > " + childFile + "; wait"
	state, err := m.Start(Spec{
		Name: "test", Kind: KindServer, Bin: "/bin/sh",
		Host: "127.0.0.1", AutoPort: true,
		Args: func(port int) []string { return []string{"-c", script} },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop("test", time.Second)
	if state.Port <= 0 || state.PID <= 0 || state.PGID != state.PID {
		t.Fatalf("state = %+v", state)
	}
	if !Alive(state.PID, state.StartTicks) {
		t.Fatal("process should be alive")
	}

	var childPID int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(childFile)
		if err == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatal("child pid was not written")
	}
	childTicks, err := StartTicks(childPID)
	if err != nil {
		t.Fatal(err)
	}

	inst, err := m.Status("test")
	if err != nil || !inst.Alive {
		t.Fatalf("status = %+v, %v", inst, err)
	}
	logs, err := m.Tail("test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 || !strings.Contains(strings.Join(logs, "\n"), "starting server test") {
		t.Fatalf("log header missing: %v", logs)
	}

	if _, err := m.Start(Spec{
		Name: "test", Bin: "/bin/sh",
		Args: func(int) []string { return []string{"-c", "sleep 30"} },
	}); err == nil {
		t.Fatal("second start should fail while running")
	}

	if err := m.Stop("test", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if Alive(state.PID, state.StartTicks) {
		t.Fatal("server still alive")
	}
	if Alive(childPID, childTicks) {
		t.Fatal("child still alive; process group was not killed")
	}
	if _, err := m.Status("test"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("state not removed: %v", err)
	}
}

func TestStaleStateCleanup(t *testing.T) {
	m := testManager(t)
	_, err := m.Start(Spec{
		Name: "short", Bin: "/bin/sh",
		Args: func(int) []string { return []string{"-c", "exit 0"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	inst, err := m.Status("short")
	if err != nil {
		t.Fatal(err)
	}
	if inst.Alive {
		t.Fatal("process should have exited")
	}
	list, err := m.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := m.Stop("short", time.Second); err != nil {
		t.Fatal(err)
	}
	if err := m.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Status("short"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("stale state not cleaned: %v", err)
	}
}

func TestDismissRefusesLiveProcess(t *testing.T) {
	m := testManager(t)
	if _, err := m.Start(Spec{
		Name: "live", Bin: "/bin/sh",
		Args: func(int) []string { return []string{"-c", "sleep 30"} },
	}); err != nil {
		t.Fatal(err)
	}
	defer m.Stop("live", time.Second)
	if err := m.Dismiss("live"); err == nil {
		t.Fatal("dismiss should refuse a live process")
	}
}
