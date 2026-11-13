package reqlog

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func feedFile(t *testing.T, p *Parser, name string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" {
			continue
		}
		p.Feed(line, at)
	}
}

func TestOldFormat(t *testing.T) {
	p := New(10)
	feedFile(t, p, "llama-server-old.log")
	rows := p.Rows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.PromptTokens != 1153 || r.GenTokens != 6 {
		t.Fatalf("tokens = %d/%d, want 1153/6", r.PromptTokens, r.GenTokens)
	}
	if r.CacheHits != 1 {
		t.Fatalf("cache hits = %d, want 1", r.CacheHits)
	}
	if r.PromptMS != 1086.52 || r.GenMS != 258.52 || r.TotalMS != 1345.04 {
		t.Fatalf("timings = %+v", r)
	}
	if r.Status != 200 || r.Port != 0 {
		t.Fatalf("status/port = %d/%d", r.Status, r.Port)
	}
	if !p.Recognized() {
		t.Fatal("recognized = false")
	}
}

func TestNewFormat(t *testing.T) {
	p := New(10)
	feedFile(t, p, "llama-server-new.log")
	rows := p.Rows()
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.PromptTokens != 7235 || r.GenTokens != 35 {
		t.Fatalf("tokens = %d/%d, want 7235/35", r.PromptTokens, r.GenTokens)
	}
	if r.CacheHits != 6722 {
		t.Fatalf("cache hits = %d, want 6722", r.CacheHits)
	}
	if r.Port != 8081 {
		t.Fatalf("port = %d, want 8081", r.Port)
	}
	if r.PromptMS != 383.98 || r.GenMS != 427.18 || r.TotalMS != 811.16 {
		t.Fatalf("timings = %+v", r)
	}
	if rows[1].Port != 8082 || rows[1].GenTokens != 0 {
		t.Fatalf("second row = %+v", rows[1])
	}
}

func TestUnknownLinesIgnored(t *testing.T) {
	p := New(10)
	at := time.Now()
	for _, line := range []string{
		"not a llama-server line",
		"slot update_slots: id 0 | task 0 | prompt processing progress, n_tokens = 10, progress = 0.5",
		"slot print_timing: id 0 | task 0 | n_gen = 10, tg = 1.00 t/s",
		"srv log_server_r: done request: POST /v1/chat/completions 127.0.0.1 200",
		"slot release: id 0 | task 0 | stop processing: n_tokens = 10, truncated = 0",
	} {
		p.Feed(line, at)
	}
	if len(p.Rows()) != 0 {
		t.Fatalf("unknown lines produced rows: %+v", p.Rows())
	}
	if !p.Recognized() {
		t.Fatal("a 200 status line should count as recognised")
	}
}

func TestUnknownFormat(t *testing.T) {
	p := New(10)
	p.Feed("llama-server: listening on 127.0.0.1:8080", time.Now())
	if p.Recognized() {
		t.Fatal("startup line should not count as a request log")
	}
}

func TestErrorStatusBecomesRow(t *testing.T) {
	p := New(10)
	at := time.Now()
	p.Feed("srv log_server_r: request: POST /v1/chat/completions 10.2.0.153 400", at)
	rows := p.Rows()
	if len(rows) != 1 || rows[0].Status != 400 || rows[0].PromptTokens != 0 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestBoundedTail(t *testing.T) {
	p := New(2)
	at := time.Now()
	for task := 0; task < 4; task++ {
		id := strconv.Itoa(task)
		p.Feed("slot update_slots: id 0 | task "+id+" | new prompt, n_prompt_tokens = 10", at)
		p.Feed("slot print_timing: id 0 | task "+id+" | prompt eval time = 1.00 ms / 5 tokens", at)
		p.Feed("eval time = 1.00 ms / 5 tokens", at)
		p.Feed("total time = 2.00 ms / 10 tokens", at)
	}
	rows := p.Rows()
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[1].PromptTokens != 10 {
		t.Fatalf("last row = %+v", rows[1])
	}
}

func TestReset(t *testing.T) {
	p := New(10)
	feedFile(t, p, "llama-server-old.log")
	p.Reset()
	if len(p.Rows()) != 0 || p.Recognized() {
		t.Fatal("reset did not clear state")
	}
}
