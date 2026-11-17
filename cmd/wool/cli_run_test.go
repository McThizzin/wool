package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wool/internal/config"
	"wool/internal/daemon"
)

func TestCmdRestartRecordedServer(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	mgr := daemon.NewManager()
	first, err := mgr.Start(daemon.Spec{
		Name: "server", Kind: daemon.KindServer, Bin: "/bin/sh",
		Host: "127.0.0.1",
		Args: func(int) []string { return []string{"-c", "sleep 30"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Stop("server", time.Second)

	out, errs, code := captureOutput(t, func() int { return cmdRestart("", []string{"--no-wait"}) })
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errs)
	}
	if !strings.Contains(out, "restarted") {
		t.Fatalf("output = %q", out)
	}
	inst, err := mgr.Status("server")
	if err != nil || !inst.Alive {
		t.Fatalf("status = %+v, %v", inst, err)
	}
	if inst.PID == first.PID {
		t.Fatal("restart reused the old process")
	}
}

func TestCmdRestartWithoutRecordedState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, errs, code := captureOutput(t, func() int { return cmdRestart("", []string{"--no-wait"}) })
	if code != 1 || !strings.Contains(errs, "no recorded launch") {
		t.Fatalf("exit = %d, stderr = %q", code, errs)
	}
}

func TestCmdRestartUsage(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, errs, code := captureOutput(t, func() int { return cmdRestart("", []string{"bogus", "extra"}) })
	if code != 2 || !strings.Contains(errs, "usage: wool restart") {
		t.Fatalf("exit = %d, stderr = %q", code, errs)
	}
}

func TestExtractConfigStopsAtDoubleDash(t *testing.T) {
	wantPath := config.ExpandPath(config.DefaultPath())
	path, rest := extractConfig([]string{
		"supervise", "--foreground", "--",
		"-c", "4096", "--config", "child.toml",
	})
	if path != wantPath {
		t.Fatalf("path = %q, want %q", path, wantPath)
	}
	want := "supervise --foreground -- -c 4096 --config child.toml"
	if got := strings.Join(rest, " "); got != want {
		t.Fatalf("rest = %q, want %q", got, want)
	}
}

func TestCmdSuperviseForegroundGivesUp(t *testing.T) {
	_, errs, code := captureOutput(t, func() int {
		return cmdSupervise("", []string{
			"--foreground", "--name", "test", "--kind", "server",
			"--host", "127.0.0.1", "--child-port", "0",
			"--bin", "/bin/sh", "--restart-max", "0",
			"--restart-window", "1m", "--restart-backoff", "1ms",
			"--", "-c", "exit 0",
		})
	})
	if code != 1 {
		t.Fatalf("exit = %d, stderr = %q", code, errs)
	}
	if !strings.Contains(errs, "giving up") {
		t.Fatalf("stderr = %q", errs)
	}
}

func TestCmdSuperviseLaunchesDetachedSupervisor(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-supervisor.sh")
	script := "#!/bin/sh\ntrap 'exit 0' TERM\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := woolExecutable
	woolExecutable = func() (string, error) { return fake, nil }
	defer func() { woolExecutable = old }()

	cfgPath := filepath.Join(dir, "config.toml")
	body := "[app]\nllama_server = \"/bin/sh\"\n\n[models.m]\npath = \"m.gguf\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := daemon.NewManager()
	defer func() { _ = mgr.Stop("server", 3*time.Second) }()

	out, errs, code := captureOutput(t, func() int {
		return cmdSupervise(cfgPath, []string{"--no-wait", "m"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, errs)
	}
	if !strings.Contains(out, "supervised") {
		t.Fatalf("output = %q", out)
	}

	inst, err := mgr.Status("server")
	if err != nil {
		t.Fatal(err)
	}
	if !inst.Supervised || !inst.Alive || !inst.SupervisorAlive {
		t.Fatalf("status = %+v", inst)
	}
	if inst.Bin != fake {
		t.Fatalf("bin = %q, want %q", inst.Bin, fake)
	}
	joined := strings.Join(inst.Args, " ")
	for _, want := range []string{"supervise --foreground", "--restart-max 3", "-- --model", "--alias m"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q: %s", want, joined)
		}
	}
}
