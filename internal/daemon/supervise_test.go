package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRestartHelpers(t *testing.T) {
	for attempt, want := range []time.Duration{
		2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, time.Minute, time.Minute,
	} {
		if got := NextRestartDelay(2*time.Second, attempt); got != want {
			t.Fatalf("delay(%d) = %s, want %s", attempt, got, want)
		}
	}

	now := time.Now()
	attempts := []time.Time{
		now.Add(-11 * time.Minute),
		now.Add(-5 * time.Minute),
		now.Add(-time.Minute),
	}
	if got := AttemptsInWindow(attempts, now, 10*time.Minute); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
	if got := len(PruneAttempts(attempts, now, 10*time.Minute)); got != 2 {
		t.Fatalf("pruned = %d, want 2", got)
	}
}

func TestRunSupervisorRestartsThenExhausts(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "count")
	script := fmt.Sprintf("echo x >> %q; exit 0", countPath)
	var stderr bytes.Buffer

	err := RunSupervisor(context.Background(), SupervisorConfig{
		Name: "test", Kind: KindServer, Host: "127.0.0.1", Port: 8080,
		Bin: "/bin/sh", Args: []string{"-c", script},
		Policy: RestartPolicy{MaxAttempts: 2, Window: time.Minute, Backoff: time.Millisecond},
		Stdout: io.Discard, Stderr: &stderr,
	})
	if !errors.Is(err, ErrSupervisorExhausted) {
		t.Fatalf("err = %v, want ErrSupervisorExhausted", err)
	}
	data, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatal(err)
	}
	runs := strings.Count(strings.TrimSpace(string(data)), "\n") + 1
	if runs != 3 {
		t.Fatalf("child runs = %d, want 3 (initial + 2 restarts)", runs)
	}
	if !strings.Contains(stderr.String(), "giving up after 2") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunSupervisorZeroAttemptsGivesUpAfterFirstExit(t *testing.T) {
	countPath := filepath.Join(t.TempDir(), "count")
	script := fmt.Sprintf("echo x >> %q; exit 0", countPath)
	var stderr bytes.Buffer

	err := RunSupervisor(context.Background(), SupervisorConfig{
		Name: "test", Bin: "/bin/sh", Args: []string{"-c", script},
		Policy: RestartPolicy{MaxAttempts: 0, Window: time.Minute, Backoff: time.Millisecond},
		Stdout: io.Discard, Stderr: &stderr,
	})
	if !errors.Is(err, ErrSupervisorExhausted) {
		t.Fatalf("err = %v, want ErrSupervisorExhausted", err)
	}
	data, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatal(err)
	}
	if runs := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; runs != 1 {
		t.Fatalf("child runs = %d, want 1", runs)
	}
}

func TestRunSupervisorCancelStopsChild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunSupervisor(ctx, SupervisorConfig{
			Name: "test", Bin: "/bin/sleep", Args: []string{"30"},
			Policy: RestartPolicy{MaxAttempts: 3, Window: time.Minute, Backoff: time.Millisecond},
			Stdout: io.Discard, Stderr: io.Discard,
		})
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancel returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop its child")
	}
}

func TestSupervisedStopSignalsSupervisor(t *testing.T) {
	m := testManager(t)
	childFile := filepath.Join(t.TempDir(), "child.pid")
	script := "trap 'kill $child 2>/dev/null; wait $child 2>/dev/null; exit 0' TERM\n" +
		"sleep 30 &\nchild=$!\necho $child > " + childFile + "\nwait $child"
	state, err := m.Start(Spec{
		Name: "sup", Kind: KindServer, Supervised: true, Bin: "/bin/sh",
		Host: "127.0.0.1", AutoPort: true,
		Args: func(int) []string { return []string{"-c", script} },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop("sup", time.Second)

	childPID := waitPIDFile(t, childFile)
	childTicks, err := StartTicks(childPID)
	if err != nil {
		t.Fatal(err)
	}
	if pgid, err := syscall.Getpgid(childPID); err != nil || pgid != state.PGID {
		t.Fatalf("child pgid = %d (%v), want %d", pgid, err, state.PGID)
	}

	inst, err := m.Status("sup")
	if err != nil {
		t.Fatal(err)
	}
	if !inst.Alive || !inst.SupervisorAlive {
		t.Fatalf("status = %+v", inst)
	}

	if err := m.Stop("sup", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if Alive(childPID, childTicks) {
		t.Fatal("child still alive after supervised stop")
	}
	if _, err := m.Status("sup"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("state not removed: %v", err)
	}
}

func TestSupervisedOrphanIsAliveAndStoppable(t *testing.T) {
	m := testManager(t)
	childFile := filepath.Join(t.TempDir(), "child.pid")
	// The supervisor exits immediately and leaves its background child in the
	// recorded process group.
	script := "sleep 30 &\necho $! > " + childFile
	state, err := m.Start(Spec{
		Name: "orphan", Kind: KindServer, Supervised: true, Bin: "/bin/sh",
		Host: "127.0.0.1", AutoPort: true,
		Args: func(int) []string { return []string{"-c", script} },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Stop("orphan", time.Second)

	childPID := waitPIDFile(t, childFile)
	childTicks, err := StartTicks(childPID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for Alive(state.PID, state.StartTicks) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	inst, err := m.Status("orphan")
	if err != nil {
		t.Fatal(err)
	}
	if inst.SupervisorAlive {
		t.Fatalf("supervisor should be dead: %+v", inst)
	}
	if !inst.Alive {
		t.Fatalf("orphaned child should keep the instance alive: %+v", inst)
	}

	if _, err := m.Start(Spec{
		Name: "orphan", Supervised: true, Bin: "/bin/sh",
		Args: func(int) []string { return []string{"-c", "sleep 30"} },
	}); err == nil {
		t.Fatal("start must refuse a supervised state whose group is still alive")
	}

	if err := m.Stop("orphan", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if Alive(childPID, childTicks) {
		t.Fatal("orphan still alive after stop")
	}
	if _, err := m.Status("orphan"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("state not removed: %v", err)
	}
}

func waitPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid file %s was not written", path)
	return 0
}
