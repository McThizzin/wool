package app

import (
	"strings"
	"testing"
	"time"

	"wool/internal/daemon"
)

func deadServerTick(at time.Time, log []string) tickMsg {
	st := daemon.State{
		Name: "server", Kind: daemon.KindServer, Model: "m",
		Host: "127.0.0.1", Port: 8080, PID: 4242,
		StartedAt: at.Add(-time.Minute),
	}
	return tickMsg{
		at: at, server: daemon.Instance{State: st},
		hasServer: false, serverLog: log,
	}
}

func TestApplyTickDetectsCrashAndSchedulesRestart(t *testing.T) {
	m := testModel(t)
	m.cfg.Server.Restart = "on-failure"
	m.cfg.Server.RestartBackoff = "2s"
	m.cfg.Server.RestartWindow = "10m"
	m.hasServer = true
	m.expectServer = true
	at := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	m.applyTick(deadServerTick(at, []string{
		"=== 2026-11-12T11:59:00Z starting server server on 127.0.0.1:8080 ===",
		"llama_init: CUDA error: out of memory",
	}))

	if m.crash == nil {
		t.Fatal("crash not detected")
	}
	if m.crash.Reason != daemon.CrashOOM {
		t.Fatalf("reason = %s, want oom", m.crash.Reason)
	}
	if len(m.crash.Lines) == 0 || !strings.Contains(strings.Join(m.crash.Lines, "\n"), "out of memory") {
		t.Fatalf("crash lines = %v", m.crash.Lines)
	}
	if m.crashState.PID != 4242 {
		t.Fatalf("crash state = %+v", m.crashState)
	}
	if m.restartAt.IsZero() || !m.restartAt.Equal(at.Add(2*time.Second)) {
		t.Fatalf("restartAt = %s, want %s", m.restartAt, at.Add(2*time.Second))
	}
	if m.expectServer {
		t.Fatal("expectServer must be cleared after a crash")
	}
}

func TestApplyTickIgnoresCleanStopAndMissingState(t *testing.T) {
	at := time.Now()

	t.Run("intentional stop", func(t *testing.T) {
		m := testModel(t)
		m.cfg.Server.Restart = "on-failure"
		m.hasServer, m.expectServer = true, true
		m.expectStop = true
		m.applyTick(deadServerTick(at, []string{"CUDA error: out of memory"}))
		if m.crash != nil {
			t.Fatalf("intentional stop produced a crash: %+v", m.crash)
		}
	})

	t.Run("state removed", func(t *testing.T) {
		m := testModel(t)
		m.cfg.Server.Restart = "on-failure"
		m.hasServer, m.expectServer = true, true
		m.applyTick(tickMsg{at: at})
		if m.crash != nil {
			t.Fatalf("missing state produced a crash: %+v", m.crash)
		}
	})
}

func TestCrashWithoutRestartPolicy(t *testing.T) {
	m := testModel(t)
	m.hasServer, m.expectServer = true, true
	at := time.Now()
	m.applyTick(deadServerTick(at, []string{"error: address already in use"}))
	if m.crash == nil || m.crash.Reason != daemon.CrashPort {
		t.Fatalf("crash = %+v", m.crash)
	}
	if !m.restartAt.IsZero() {
		t.Fatalf("restart scheduled with policy never: %s", m.restartAt)
	}
	if got := m.restartStatus(at); got != "auto-restart disabled" {
		t.Fatalf("status = %q", got)
	}
}

func TestRestartBackoffAndWindow(t *testing.T) {
	m := testModel(t)
	m.cfg.Server.Restart = "on-failure"
	m.cfg.Server.RestartBackoff = "2s"
	m.cfg.Server.RestartWindow = "10m"
	max := 3
	m.cfg.Server.RestartMax = &max

	for attempt, want := range []time.Duration{
		2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, time.Minute, time.Minute,
	} {
		if got := m.nextRestartDelay(attempt); got != want {
			t.Fatalf("delay(%d) = %s, want %s", attempt, got, want)
		}
	}

	now := time.Now()
	m.crash = &daemon.Crash{Reason: daemon.CrashOOM}
	m.crashState = daemon.State{Name: "server"}
	m.restartHist = []time.Time{now.Add(-9 * time.Minute), now.Add(-5 * time.Minute)}
	m.scheduleRestart(now)
	if !m.restartAt.Equal(now.Add(8 * time.Second)) {
		t.Fatalf("restartAt = %s, want %s", m.restartAt, now.Add(8*time.Second))
	}

	m.restartHist = append(m.restartHist, now.Add(-time.Minute))
	m.restartAt = time.Time{}
	m.scheduleRestart(now)
	if !m.restartAt.IsZero() {
		t.Fatalf("restart scheduled after max attempts: %s", m.restartAt)
	}
	if got := m.restartStatus(now); !strings.Contains(got, "stopped after 3") {
		t.Fatalf("status = %q", got)
	}

	// Attempts outside the window do not count.
	m.resetRestartState()
	m.restartHist = []time.Time{now.Add(-11 * time.Minute), now.Add(-10*time.Minute - time.Second)}
	m.scheduleRestart(now)
	if !m.restartAt.Equal(now.Add(2 * time.Second)) {
		t.Fatalf("restartAt = %s, want %s", m.restartAt, now.Add(2*time.Second))
	}
}

func TestAutoRestartCmdRespectsDeadline(t *testing.T) {
	m := testModel(t)
	m.cfg.Server.Restart = "on-failure"
	m.crash = &daemon.Crash{Reason: daemon.CrashOOM}
	m.crashState = daemon.State{Name: "server"}
	m.restartAt = time.Now().Add(time.Minute)
	if cmd := m.autoRestartCmd(); cmd != nil {
		t.Fatal("restart fired before its deadline")
	}
	if len(m.restartHist) != 0 {
		t.Fatalf("history = %v", m.restartHist)
	}

	m.restartAt = time.Now().Add(-time.Second)
	if cmd := m.autoRestartCmd(); cmd == nil {
		t.Fatal("restart did not fire after its deadline")
	}
	if len(m.restartHist) != 1 {
		t.Fatalf("history = %v, want one attempt", m.restartHist)
	}
	if !m.restartAt.IsZero() || !m.expectStop || !m.expectServer {
		t.Fatalf("restartAt = %s expectStop = %v expectServer = %v", m.restartAt, m.expectStop, m.expectServer)
	}
}

func TestDismissCrashKeepsState(t *testing.T) {
	m := testModel(t)
	m.crash = &daemon.Crash{At: time.Now(), Reason: daemon.CrashSignal}
	m.crashState = daemon.State{Name: "server", PID: 42}
	m.restartAt = time.Now().Add(time.Second)
	m.restartHist = []time.Time{time.Now()}
	m.expectServer = true

	m.dismissCrash()

	if m.crash != nil || m.crashState.Name != "" {
		t.Fatalf("crash = %+v state = %+v", m.crash, m.crashState)
	}
	if !m.restartAt.IsZero() || len(m.restartHist) != 0 {
		t.Fatalf("restart state = %s %v", m.restartAt, m.restartHist)
	}
	if m.expectServer {
		t.Fatal("expectServer must be cleared")
	}
	if !strings.Contains(m.toast, "dismissed") {
		t.Fatalf("toast = %q", m.toast)
	}
}

func TestStopServerSuppressesCrash(t *testing.T) {
	m := testModel(t)
	m.hasServer = true
	m.crash = &daemon.Crash{Reason: daemon.CrashOOM}
	m.restartAt = time.Now().Add(time.Second)

	_ = m.stopServer()

	if !m.expectStop {
		t.Fatal("stop must mark the stop as intentional")
	}
	if m.crash != nil || !m.restartAt.IsZero() {
		t.Fatalf("crash = %+v restartAt = %s", m.crash, m.restartAt)
	}
}

func TestCrashCardRendersReasonAndActions(t *testing.T) {
	m := testModel(t)
	m.cfg.Server.Restart = "on-failure"
	at := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	m.crash = &daemon.Crash{
		At: at, Reason: daemon.CrashOOM,
		Lines: []string{"llama_init: CUDA error: out of memory"},
	}
	m.crashState = daemon.State{Name: "server"}
	m.restartAt = at.Add(2 * time.Second)

	m.width, m.height = 120, 40
	out := m.View()
	for _, want := range []string{"server crashed", "out of memory", "restart in", "S restart", "x dismiss"} {
		if !strings.Contains(out, want) {
			t.Fatalf("view missing %q:\n%s", want, out)
		}
	}
	checkView(t, m, 120, 40)
}
