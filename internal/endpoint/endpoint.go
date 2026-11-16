// Package endpoint builds copy-paste client details for a running
// llama-server or router. It never includes credentials: llama-server ignores
// the API key, so every snippet uses a visible placeholder.
package endpoint

import (
	"fmt"
	"strings"
)

// Version is the stable JSON shape version.
const Version = 1

// Snippet is one copy-paste client example. Name is stable and lowercase.
type Snippet struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// Endpoint is the stable `wool endpoint --json` result.
type Endpoint struct {
	Version  int       `json:"version"`
	Source   string    `json:"source"`
	BaseURL  string    `json:"base_url"`
	APIBase  string    `json:"api_base"`
	Model    string    `json:"model"`
	Snippets []Snippet `json:"snippets"`
}

// Build returns the endpoint details and snippets for one running instance.
// baseURL may end in a slash; the result is normalized.
func Build(source, baseURL, model string) Endpoint {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	api := base + "/v1"
	return Endpoint{
		Version: Version,
		Source:  source,
		BaseURL: base,
		APIBase: api,
		Model:   model,
		Snippets: []Snippet{
			{Name: "curl", Text: curlSnippet(api, model)},
			{Name: "openai-python", Text: openAIPythonSnippet(api, model)},
			{Name: "continue", Text: continueSnippet(api, model)},
			{Name: "open-webui", Text: openWebUISnippet(api, model)},
		},
	}
}

func curlSnippet(api, model string) string {
	return fmt.Sprintf(`curl %s/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": %q,
    "messages": [{"role": "user", "content": "Hello"}]
  }'`, api, model)
}

func openAIPythonSnippet(api, model string) string {
	return fmt.Sprintf(`from openai import OpenAI

client = OpenAI(base_url=%q, api_key="not-needed")
response = client.chat.completions.create(
    model=%q,
    messages=[{"role": "user", "content": "Hello"}],
)
print(response.choices[0].message.content)`, api, model)
}

func continueSnippet(api, model string) string {
	return fmt.Sprintf(`# ~/.continue/config.yaml
name: wool
version: 1.0.0
schema: v1
models:
  - name: %s
    provider: openai
    model: %s
    apiBase: %s
    apiKey: not-needed`, model, model, api)
}

func openWebUISnippet(api, model string) string {
	return fmt.Sprintf(`# Open WebUI -> Admin Settings -> Connections -> OpenAI
base URL: %s
API key:  not-needed
model:    %s

# Running Open WebUI in Docker? Replace 127.0.0.1 with host.docker.internal.`,
		api, model)
}
