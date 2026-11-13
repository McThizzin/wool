package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"wool/internal/daemon"
	"wool/internal/llamaserver"
	"wool/internal/reqlog"
)

// feedRow feeds one complete request block to a parser.
func feedRow(p *reqlog.Parser, port, task, prompt, eval, gen int, at time.Time) {
	for _, line := range rowLines(port, task, prompt, eval, gen) {
		p.Feed(line, at)
	}
}

// rowLines renders one complete request block in the current llama-server
// format. A non-zero port gets the router's child prefix.
func rowLines(port, task, prompt, eval, gen int) []string {
	prefix := ""
	if port > 0 {
		prefix = "[ " + strconv.Itoa(port) + "] "
	}
	id := strconv.Itoa(task)
	return []string{
		prefix + "slot update_slots: id 0 | task " + id + " | new prompt, n_prompt_tokens = " + strconv.Itoa(prompt),
		prefix + "slot print_timing: id 0 | task " + id + " | prompt eval time = 10.00 ms / " + strconv.Itoa(eval) + " tokens",
		prefix + "eval time = 20.00 ms / " + strconv.Itoa(gen) + " tokens",
		prefix + "total time = 30.00 ms / " + strconv.Itoa(eval+gen) + " tokens",
	}
}

func TestNewLogLines(t *testing.T) {
	cases := []struct {
		name  string
		prev  []string
		cur   []string
		fresh []string
		reset bool
	}{
		{"first", nil, []string{"a"}, []string{"a"}, true},
		{"none", []string{"a"}, []string{"a"}, nil, false},
		{"append", []string{"a", "b", "c"}, []string{"b", "c", "d"}, []string{"d"}, false},
		{"rolled", []string{"a", "b"}, []string{"c", "d"}, []string{"c", "d"}, true},
		{"truncated", []string{"a", "b", "c"}, []string{"c"}, []string{"c"}, true},
	}
	for _, tc := range cases {
		fresh, reset := newLogLines(tc.prev, tc.cur)
		if reset != tc.reset || strings.Join(fresh, ",") != strings.Join(tc.fresh, ",") {
			t.Errorf("%s: fresh=%v reset=%v, want %v %v", tc.name, fresh, reset, tc.fresh, tc.reset)
		}
	}
}

func TestStructuredLogView(t *testing.T) {
	m := testModel(t)
	m.tab = tabLogs
	m.hasServer = true
	m.logSel, m.logName = 0, "server"
	m.server = serverInstance()
	m.server.Model = "qwen-test"
	m.logLines = []string{"raw log line"}
	m.reqlog = reqlog.New(10)
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	feedRow(m.reqlog, 0, 1, 100, 80, 20, at)
	m.width, m.height = 120, 40

	out := m.View()
	for _, want := range []string{"best effort", "CACHE", "qwen-test", "ok", "v raw log"} {
		if !strings.Contains(out, want) {
			t.Fatalf("structured view missing %q:\n%s", want, out)
		}
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	if !m.logRaw {
		t.Fatal("v did not switch to raw")
	}
	if !strings.Contains(m.View(), "raw log line") {
		t.Fatal("raw log not rendered")
	}
}

func TestStructuredLogFallback(t *testing.T) {
	m := testModel(t)
	m.tab = tabLogs
	m.hasServer = true
	m.logSel, m.logName = 0, "server"
	m.logLines = []string{"llama-server: listening on 127.0.0.1:8080"}
	m.reqlog = reqlog.New(10)
	m.width, m.height = 120, 40

	out := m.View()
	if !strings.Contains(out, "unrecognised llama-server request log") || !strings.Contains(out, "best effort") {
		t.Fatalf("fallback note missing:\n%s", out)
	}
	if !strings.Contains(out, "listening on") {
		t.Fatal("raw log not shown as fallback")
	}
}

func TestLogModelFilter(t *testing.T) {
	m := testModel(t)
	m.tab = tabLogs
	m.hasRouter = true
	m.logSel, m.logName = 1, "router"
	m.routerModels = []llamaserver.Model{
		{ID: "alpha", Status: llamaserver.ModelStatus{Args: []string{"--port", "8081"}}},
		{ID: "beta", Status: llamaserver.ModelStatus{Args: []string{"--port=8082"}}},
	}
	m.reqlog = reqlog.New(10)
	at := time.Now()
	feedRow(m.reqlog, 8081, 1, 100, 80, 20, at)
	feedRow(m.reqlog, 8082, 2, 200, 150, 50, at)

	if got := m.logModels(); strings.Join(got, ",") != "alpha,beta" {
		t.Fatalf("models = %v", got)
	}
	m.cycleLogFilter()
	if m.logFilter != "alpha" || len(m.filteredLogRows()) != 1 {
		t.Fatalf("first filter = %q rows=%d", m.logFilter, len(m.filteredLogRows()))
	}
	m.cycleLogFilter()
	if m.logFilter != "beta" || len(m.filteredLogRows()) != 1 {
		t.Fatalf("second filter = %q rows=%d", m.logFilter, len(m.filteredLogRows()))
	}
	m.cycleLogFilter()
	if m.logFilter != "" || len(m.filteredLogRows()) != 2 {
		t.Fatalf("all filter = %q rows=%d", m.logFilter, len(m.filteredLogRows()))
	}
}

func TestLoadLogsFeedsOnlyNewLines(t *testing.T) {
	m := testModel(t)
	m.hasServer = true
	m.logSel, m.logName = 0, "server"
	if err := os.MkdirAll(m.mgr.LogDir, 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(m.mgr.LogDir, "server.log")
	state := daemon.State{
		Name: "server", Kind: daemon.KindServer, Model: "qwen-test",
		Host: "127.0.0.1", Port: 8080, LogPath: logPath,
	}
	writeState(t, m, state)

	appendLines(t, logPath, rowLines(0, 1, 100, 80, 20)...)
	m.loadLogs()
	if got := len(m.reqlog.Rows()); got != 1 {
		t.Fatalf("rows after first load = %d, want 1", got)
	}

	// Re-reading the unchanged tail must not duplicate the first row.
	m.loadLogs()
	if got := len(m.reqlog.Rows()); got != 1 {
		t.Fatalf("rows after unchanged reload = %d, want 1", got)
	}

	appendLines(t, logPath, rowLines(0, 2, 200, 150, 50)...)
	m.loadLogs()
	if got := len(m.reqlog.Rows()); got != 2 {
		t.Fatalf("rows after append = %d, want 2", got)
	}
}

// writeState writes the daemon state JSON without starting a process.
func writeState(t *testing.T, m *Model, state daemon.State) {
	t.Helper()
	if err := os.MkdirAll(m.mgr.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.mgr.StateDir, state.Name+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// appendLines appends log lines to a file.
func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}
