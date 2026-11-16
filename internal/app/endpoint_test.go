package app

import (
	"errors"
	"strings"
	"testing"

	"wool/internal/daemon"
	"wool/internal/llamaserver"
)

func endpointTestModel(t *testing.T) *Model {
	t.Helper()
	m := testModel(t)
	m.hasServer = true
	m.server = daemon.Instance{
		State: daemon.State{
			Name: "server", Kind: daemon.KindServer,
			Model: "qwen35-4b-mtp", Host: "127.0.0.1", Port: 8081,
		},
		Alive: true,
	}
	return m
}

func TestEndpointPanelShowsServerDetails(t *testing.T) {
	m := endpointTestModel(t)
	m.width, m.height = 120, 60
	m.showEndpoint("")
	if !m.endpointOpen {
		t.Fatal("endpoint overlay did not open")
	}
	checkView(t, m, 120, 60)
	out := m.View()
	for _, want := range []string{
		"endpoint · server",
		"http://127.0.0.1:8081",
		"http://127.0.0.1:8081/v1",
		"Qwen-MTP",
		"curl", "openai-python", "continue", "open-webui",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("endpoint panel missing %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"Bearer ", "sk-", "Authorization:"} {
		if strings.Contains(out, bad) {
			t.Fatalf("endpoint panel contains %q:\n%s", bad, out)
		}
	}
}

func TestEndpointPanelUsesRouterSelection(t *testing.T) {
	m := endpointTestModel(t)
	m.hasRouter = true
	m.router = daemon.Instance{
		State: daemon.State{Name: "router", Kind: daemon.KindRouter, Host: "127.0.0.1", Port: 8082},
		Alive: true,
	}
	m.routerModels = []llamaserver.Model{
		{ID: "a", Status: llamaserver.ModelStatus{Value: "loaded"}},
		{ID: "b", Status: llamaserver.ModelStatus{Value: "unloaded"}},
	}
	m.tab = tabRouter
	m.routerSel, m.routerSelID = 0, "a"

	m.showEndpoint("")
	if m.endpointData.Source != "router" || m.endpointData.Model != "a" {
		t.Fatalf("endpoint = %+v", m.endpointData)
	}

	m.showEndpoint("b")
	if m.endpointData.Model != "b" || m.routerSelID != "b" {
		t.Fatalf("explicit model did not select b: %+v / %q", m.endpointData, m.routerSelID)
	}

	m.showEndpoint("missing")
	if !strings.Contains(m.toast, "router does not list missing") {
		t.Fatalf("toast = %q", m.toast)
	}
}

func TestEndpointCopyReportsSuccessAndFailure(t *testing.T) {
	old := clipboardWrite
	defer func() { clipboardWrite = old }()

	m := endpointTestModel(t)
	m.width, m.height = 120, 40
	m.showEndpoint("")

	var copied string
	clipboardWrite = func(s string) error {
		copied = s
		return nil
	}
	m.handleEndpointKey("c")
	if copied != m.endpointData.Snippets[0].Text {
		t.Fatalf("copied %q, want the selected snippet", copied)
	}
	if !strings.Contains(m.endpointNotice, "copied curl") {
		t.Fatalf("notice = %q", m.endpointNotice)
	}

	m.handleEndpointKey("tab")
	if m.endpointSel != 1 {
		t.Fatalf("selection = %d, want 1", m.endpointSel)
	}
	m.handleEndpointKey("3")
	if m.endpointSel != 2 {
		t.Fatalf("selection = %d, want 2", m.endpointSel)
	}

	clipboardWrite = func(string) error { return errors.New("xclip not found") }
	m.handleEndpointKey("u")
	if !strings.Contains(m.endpointNotice, "clipboard error: xclip not found") {
		t.Fatalf("notice = %q", m.endpointNotice)
	}
	checkView(t, m, 120, 40)
	if out := m.View(); !strings.Contains(out, "clipboard error: xclip not found") {
		t.Fatalf("overlay footer missing clipboard error:\n%s", out)
	}
}

func TestEndpointCommandAndClose(t *testing.T) {
	m := endpointTestModel(t)
	if _, handled := m.runCommand("/endpoint"); !handled || !m.endpointOpen {
		t.Fatalf("handled = %v, open = %v", handled, m.endpointOpen)
	}
	m.handleEndpointKey("esc")
	if m.endpointOpen || m.overlay != "" {
		t.Fatalf("overlay did not close: open=%v overlay=%q", m.endpointOpen, m.overlay)
	}
}
