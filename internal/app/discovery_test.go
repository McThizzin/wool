package app

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"wool/internal/config"
	"wool/internal/discovery"
)

func writeDiscoverGGUF(t *testing.T, path, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	type kv struct {
		key string
		typ uint32
		val any
	}
	var b bytes.Buffer
	w32 := func(v uint32) { binary.Write(&b, binary.LittleEndian, v) }
	w64 := func(v uint64) { binary.Write(&b, binary.LittleEndian, v) }
	wstr := func(s string) { w64(uint64(len(s))); b.WriteString(s) }
	kvs := []kv{
		{"general.name", 8, name},
		{"general.architecture", 8, "llama"},
		{"general.parameter_count", 10, uint64(3_000_000_000)},
		{"llama.block_count", 4, uint32(2)},
		{"llama.embedding_length", 4, uint32(8)},
		{"llama.attention.head_count", 4, uint32(2)},
		{"llama.context_length", 4, uint32(8192)},
	}
	w32(0x46554747)
	w32(3)
	w64(0)
	w64(uint64(len(kvs)))
	for _, item := range kvs {
		wstr(item.key)
		w32(item.typ)
		switch item.typ {
		case 8:
			wstr(item.val.(string))
		case 4:
			w32(item.val.(uint32))
		case 10:
			w64(item.val.(uint64))
		}
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverTabScansInBackgroundAndAdds(t *testing.T) {
	m := testModel(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "Fresh Model.gguf")
	writeDiscoverGGUF(t, path, "Fresh Model Alias")
	if err := config.SetValue(m.cfgPath, "app", "model_dirs", "["+strconv.Quote(dir)+"]"); err != nil {
		t.Fatal(err)
	}
	m.reloadConfig()

	cmd := m.setTab(tabDiscovered)
	if cmd == nil {
		t.Fatal("entering the Discovered tab did not start a scan")
	}
	msg := cmd()
	if _, ok := msg.(discoveryMsg); !ok {
		t.Fatalf("scan command returned %T", msg)
	}
	if _, next := m.Update(msg); next != nil {
		t.Fatalf("scan result scheduled unexpected work")
	}
	if m.busy != "" || m.discoveryBusy {
		t.Fatalf("scan left busy state: busy=%q discoveryBusy=%v", m.busy, m.discoveryBusy)
	}
	if len(m.discovery.Entries) != 1 {
		t.Fatalf("entries = %+v", m.discovery.Entries)
	}
	e := m.discovery.Entries[0]
	if e.SuggestedID != "fresh-model" || e.SuggestedAlias != "Fresh Model Alias" {
		t.Fatalf("entry = %+v", e)
	}
	m.width, m.height = 120, 40
	out := m.View()
	for _, want := range []string{"Discovered", "fresh-model", "new", "3.0B"} {
		if !strings.Contains(out, want) {
			t.Fatalf("discovered view missing %q:\n%s", want, out)
		}
	}
	checkView(t, m, 120, 40)
	checkView(t, m, 60, 20)

	m.openDiscoverAdd()
	if !m.discoveryAdd || !strings.Contains(m.overlay, "Fresh Model Alias") {
		t.Fatalf("add confirmation = %q", m.overlay)
	}
	_, cmd = m.handleKey(keyMsg("enter"))
	if m.discoveryAdd || cmd == nil {
		t.Fatal("add confirmation did not launch the add command")
	}
	msg = cmd()
	if _, ok := msg.(discoveryAddMsg); !ok {
		t.Fatalf("add command returned %T", msg)
	}
	_, rescan := m.Update(msg)
	cfg, err := config.Load(m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	added, ok := cfg.Models["fresh-model"]
	if !ok || added.Path != path || added.Alias != "Fresh Model Alias" {
		t.Fatalf("model = %+v, ok = %v", added, ok)
	}
	if _, err := cfg.Resolve("fresh-model", nil); err != nil {
		t.Fatalf("added model does not resolve: %v", err)
	}

	// The post-add rescan marks the entry as configured instead of new.
	if rescan != nil {
		m.Update(rescan())
	}
	ok = false
	for _, configured := range m.discovery.Configured {
		if configured.ID == "fresh-model" && configured.Discovered {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("rescan did not mark the new model configured: %+v", m.discovery.Configured)
	}
}

func TestDiscoverTabShortcutAndWarnings(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 120, 40
	if _, _ = m.handleKey(keyMsg("9")); m.tab != tabDiscovered {
		t.Fatalf("9 selected %v, want discovered", m.tab)
	}
	if !strings.Contains(m.View(), "Discovered") {
		t.Fatal("header does not show the Discovered tab")
	}
	m.discoveryBusy = false
	m.discoveryDone = true
	m.discovery = discovery.Report{
		Warnings: []discovery.Warning{{Path: "/models/x.gguf", Message: "bad metadata"}},
	}
	if _, _ = m.handleKey(keyMsg("w")); !strings.Contains(m.overlay, "bad metadata") {
		t.Fatalf("warning overlay = %q", m.overlay)
	}
}

func TestDiscoverAddSkipsConfiguredEntry(t *testing.T) {
	m := testModel(t)
	m.discovery = discovery.Report{Entries: []discovery.Entry{{
		Path: "/models/known.gguf", SuggestedID: "known", Configured: []string{"known"},
	}}}
	m.discoveredSel = 0
	m.openDiscoverAdd()
	if m.discoveryAdd {
		t.Fatal("configured entry opened the add confirmation")
	}
	if !strings.Contains(m.toast, "already configured") {
		t.Fatalf("toast = %q", m.toast)
	}
}
