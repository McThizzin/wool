package main

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"wool/internal/daemon"
)

func captureStdout(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	code := fn()
	w.Close()
	out, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

// writeInstanceState records a live-looking instance so Manager.Status finds
// it. StartTicks 0 skips PID-start validation; this process is alive.
func writeInstanceState(t *testing.T, stateHome, name, host string, port int, model string) {
	t.Helper()
	writeInstanceStateWithLog(t, stateHome, name, host, port, model, "")
}

func writeInstanceStateWithLog(t *testing.T, stateHome, name, host string, port int, model, logPath string) {
	t.Helper()
	dir := filepath.Join(stateHome, "wool", "servers")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	st := daemon.State{
		Name: name, Kind: daemon.KindServer,
		Model: model, Host: host, Port: port,
		PID: os.Getpid(), LogPath: logPath,
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// hostPort splits an httptest URL into its host and port.
func hostPort(t *testing.T, raw string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(raw, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

// captureOutput captures both streams around one command run.
func captureOutput(t *testing.T, fn func() int) (stdout, stderr string, code int) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = wOut, wErr
	code = fn()
	wOut.Close()
	wErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	out, err := io.ReadAll(rOut)
	if err != nil {
		t.Fatal(err)
	}
	rOut.Close()
	errs, err := io.ReadAll(rErr)
	if err != nil {
		t.Fatal(err)
	}
	rErr.Close()
	return string(out), string(errs), code
}
