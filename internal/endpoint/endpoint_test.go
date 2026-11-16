package endpoint

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildIncludesEveryClient(t *testing.T) {
	ep := Build("server", "http://127.0.0.1:8080/", "qwen")
	if ep.Version != Version || ep.Source != "server" {
		t.Fatalf("identity = %+v", ep)
	}
	if ep.BaseURL != "http://127.0.0.1:8080" || ep.APIBase != "http://127.0.0.1:8080/v1" {
		t.Fatalf("URLs = %q / %q", ep.BaseURL, ep.APIBase)
	}
	if ep.Model != "qwen" {
		t.Fatalf("model = %q", ep.Model)
	}
	want := []string{"curl", "openai-python", "continue", "open-webui"}
	if len(ep.Snippets) != len(want) {
		t.Fatalf("snippets = %d, want %d", len(ep.Snippets), len(want))
	}
	for i, name := range want {
		if ep.Snippets[i].Name != name {
			t.Fatalf("snippet %d = %q, want %q", i, ep.Snippets[i].Name, name)
		}
		text := ep.Snippets[i].Text
		if !strings.Contains(text, "http://127.0.0.1:8080/v1") {
			t.Fatalf("%s snippet missing API base:\n%s", name, text)
		}
		if !strings.Contains(text, "qwen") {
			t.Fatalf("%s snippet missing model:\n%s", name, text)
		}
	}
	if !strings.Contains(ep.Snippets[0].Text, "/chat/completions") {
		t.Fatalf("curl snippet missing completion path:\n%s", ep.Snippets[0].Text)
	}
}

func TestBuildNeverPrintsSecrets(t *testing.T) {
	ep := Build("router", "http://127.0.0.1:8080", "qwen")
	for _, s := range ep.Snippets {
		text := strings.ToLower(s.Text)
		for _, bad := range []string{"bearer ", "sk-", "authorization:"} {
			if strings.Contains(text, bad) {
				t.Fatalf("%s snippet looks like it contains a secret (%q):\n%s", s.Name, bad, s.Text)
			}
		}
		if s.Name != "curl" && !strings.Contains(text, "not-needed") {
			t.Fatalf("%s snippet should use a visible placeholder key:\n%s", s.Name, s.Text)
		}
	}
}

func TestBuildJSONIsStable(t *testing.T) {
	ep := Build("server", "http://127.0.0.1:8080", "qwen")
	first, err := json.Marshal(ep)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(Build("server", "http://127.0.0.1:8080", "qwen"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("JSON changed between identical builds:\n%s\n%s", first, second)
	}
}
