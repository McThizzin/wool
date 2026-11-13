package daemon

import (
	"strings"
	"testing"
)

func TestClassifyCrashReasons(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  CrashReason
	}{
		{
			name:  "oom",
			lines: []string{"starting server", "llama_init: CUDA error: out of memory"},
			want:  CrashOOM,
		},
		{
			name:  "port",
			lines: []string{"starting server", "error: address already in use"},
			want:  CrashPort,
		},
		{
			name:  "model",
			lines: []string{"starting server", "failed to load model from /models/a.gguf"},
			want:  CrashModel,
		},
		{
			name:  "signal",
			lines: []string{"starting server", "terminated by signal: killed"},
			want:  CrashSignal,
		},
		{
			name:  "unknown",
			lines: []string{"starting server", "last words"},
			want:  CrashUnknown,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := ClassifyCrash(tc.lines)
			if c.Reason != tc.want {
				t.Fatalf("reason = %s, want %s (%+v)", c.Reason, tc.want, c)
			}
			if c.Detail == "" && tc.want != CrashUnknown {
				t.Fatalf("missing detail: %+v", c)
			}
		})
	}
}

func TestClassifyCrashPrecedence(t *testing.T) {
	lines := []string{
		"failed to load model",
		"error: address already in use",
		"CUDA error: out of memory",
	}
	c := ClassifyCrash(lines)
	if c.Reason != CrashOOM {
		t.Fatalf("reason = %s, want oom", c.Reason)
	}
	if len(c.Lines) != 1 || !strings.Contains(c.Lines[0], "out of memory") {
		t.Fatalf("lines = %v", c.Lines)
	}
}

func TestClassifyCrashIgnoresOldLaunches(t *testing.T) {
	lines := []string{
		"=== 2026-01-01T00:00:00Z starting server old on 127.0.0.1:8080 ===",
		"CUDA error: out of memory",
		"=== 2026-01-02T00:00:00Z starting server new on 127.0.0.1:8080 ===",
		"error: address already in use",
	}
	c := ClassifyCrash(lines)
	if c.Reason != CrashPort {
		t.Fatalf("reason = %s, want port (%+v)", c.Reason, c)
	}
	if len(c.Lines) != 1 || !strings.Contains(c.Lines[0], "address already in use") {
		t.Fatalf("lines = %v", c.Lines)
	}
}

func TestClassifyCrashUnknownKeepsTail(t *testing.T) {
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "line")
	}
	c := ClassifyCrash(lines)
	if c.Reason != CrashUnknown {
		t.Fatalf("reason = %s", c.Reason)
	}
	if len(c.Lines) != maxCrashLines {
		t.Fatalf("lines = %d, want %d", len(c.Lines), maxCrashLines)
	}
	if c.Detail != "line" {
		t.Fatalf("detail = %q", c.Detail)
	}
}

func TestSpecFromState(t *testing.T) {
	s := State{
		Name:   "server",
		Kind:   KindServer,
		Preset: "p",
		Model:  "m",
		Bin:    "/bin/sh",
		Host:   "127.0.0.1",
		Port:   9090,
		Args:   []string{"-c", "sleep 1"},
	}
	spec := SpecFromState(s)
	if spec.Name != s.Name || spec.Kind != s.Kind || spec.Preset != s.Preset ||
		spec.Model != s.Model || spec.Bin != s.Bin || spec.Host != s.Host || spec.Port != s.Port {
		t.Fatalf("spec = %+v", spec)
	}
	if spec.AutoPort {
		t.Fatal("restart must reuse the recorded port")
	}
	args := spec.Args(1234)
	if len(args) != 2 || args[0] != "-c" || args[1] != "sleep 1" {
		t.Fatalf("args = %v", args)
	}
	args[0] = "changed"
	if spec.Args(1234)[0] != "-c" {
		t.Fatal("argument builder must return a copy")
	}
}
