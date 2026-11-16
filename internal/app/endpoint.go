package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/atotto/clipboard"

	"wool/internal/endpoint"
)

// clipboardWrite is replaced by tests so copy failures stay testable without
// a display server.
var clipboardWrite = clipboard.WriteAll

// showEndpoint opens the endpoint helper. what is "", "server", "router" or a
// router model ID; empty follows the focused tab, then the running instance.
func (m *Model) showEndpoint(what string) {
	source, baseURL, model, err := m.endpointTarget(what)
	if err != nil {
		m.setToast("endpoint: " + err.Error())
		return
	}
	m.endpointData = endpoint.Build(source, baseURL, model)
	m.endpointSel = 0
	m.endpointNotice = ""
	m.endpointOpen = true
	m.overlayTitle = "endpoint · " + source
	m.overlayScroll = 0
	m.refreshEndpointOverlay()
}

// endpointTarget resolves the base URL and model the helper describes.
func (m *Model) endpointTarget(what string) (source, baseURL, model string, err error) {
	switch what {
	case "server":
		if !m.hasServer {
			return "", "", "", errors.New("server is not running (press s to start it)")
		}
		return "server", m.server.URL(), m.servedModelID(), nil
	case "router":
		if !m.hasRouter {
			return "", "", "", errors.New("router is not running (press r to start it)")
		}
		id := m.selectedRouterID()
		if id == "" {
			return "", "", "", errors.New("router lists no models")
		}
		return "router", m.router.URL(), id, nil
	case "":
		if m.tab == tabRouter && m.hasRouter {
			return m.endpointTarget("router")
		}
		if m.hasServer {
			return m.endpointTarget("server")
		}
		if m.hasRouter {
			return m.endpointTarget("router")
		}
		return "", "", "", errors.New("no server is running (start one with s or r)")
	default:
		if !m.hasRouter {
			return "", "", "", errors.New("router is not running (press r to start it)")
		}
		for i, rm := range m.routerModels {
			if rm.ID == what {
				m.routerSel, m.routerSelID = i, rm.ID
				return "router", m.router.URL(), rm.ID, nil
			}
		}
		return "", "", "", fmt.Errorf("router does not list %s", what)
	}
}

// servedModelID prefers the alias llama-server was launched with, which is
// the model ID its OpenAI endpoint reports. The recorded model ID is the
// fallback for an older state file or a model removed from the config.
func (m *Model) servedModelID() string {
	if m.server.Model == "" {
		return ""
	}
	if m.cfg != nil {
		if r, err := m.cfg.Resolve(m.server.Model, nil); err == nil && r.Alias != "" {
			return r.Alias
		}
	}
	return m.server.Model
}

// endpointBody renders the overlay and returns the first line of every
// snippet so selection can keep it in view.
func (m *Model) endpointBody() ([]string, []int) {
	ep := m.endpointData
	lines := []string{
		"base URL   " + ep.BaseURL,
		"API base   " + ep.APIBase,
		"model      " + ep.Model,
		"",
		"snippets   tab/1-4 select · c copy · u base URL · y model · j/k scroll",
		"",
	}
	starts := make([]int, len(ep.Snippets))
	for i, s := range ep.Snippets {
		starts[i] = len(lines)
		marker := "  "
		if i == m.endpointSel {
			marker = "> "
		}
		lines = append(lines, fmt.Sprintf("%s[%d] %s", marker, i+1, s.Name))
		for _, line := range strings.Split(s.Text, "\n") {
			lines = append(lines, "    "+line)
		}
		lines = append(lines, "")
	}
	return lines, starts
}

// refreshEndpointOverlay rebuilds the overlay and scrolls the selected
// snippet into view.
func (m *Model) refreshEndpointOverlay() {
	lines, starts := m.endpointBody()
	m.overlay = strings.Join(lines, "\n")
	if len(starts) == 0 {
		return
	}
	bodyH := m.height - 4
	if bodyH < 4 {
		bodyH = 4
	}
	start := starts[clamp(m.endpointSel, 0, len(starts)-1)]
	end := start + 1 + len(strings.Split(m.endpointData.Snippets[m.endpointSel].Text, "\n"))
	if start < m.overlayScroll {
		m.overlayScroll = start
	}
	if end >= m.overlayScroll+bodyH-1 {
		m.overlayScroll = end - (bodyH - 2)
		if m.overlayScroll < 0 {
			m.overlayScroll = 0
		}
	}
}

func (m *Model) endpointSelect(delta int) {
	n := len(m.endpointData.Snippets)
	if n == 0 {
		return
	}
	m.endpointSel = (m.endpointSel + delta + n) % n
	m.refreshEndpointOverlay()
}

func (m *Model) endpointPick(i int) {
	if i < 0 || i >= len(m.endpointData.Snippets) {
		return
	}
	m.endpointSel = i
	m.refreshEndpointOverlay()
}

func (m *Model) copyEndpoint(text, label string) {
	if text == "" {
		m.endpointNotice = "nothing to copy"
		return
	}
	if err := clipboardWrite(text); err != nil {
		m.endpointNotice = "clipboard error: " + err.Error()
		return
	}
	m.endpointNotice = "copied " + label
}

func (m *Model) endpointCopySelected() {
	if m.endpointSel < 0 || m.endpointSel >= len(m.endpointData.Snippets) {
		m.endpointNotice = "no snippet selected"
		return
	}
	s := m.endpointData.Snippets[m.endpointSel]
	m.copyEndpoint(s.Text, s.Name)
}

// handleEndpointKey handles the endpoint overlay keys. It returns false when
// the key does not belong to the panel.
func (m *Model) handleEndpointKey(key string) bool {
	switch key {
	case "esc", "enter", "q":
		m.endpointOpen = false
		m.overlay = ""
	case "tab":
		m.endpointSelect(1)
	case "shift+tab":
		m.endpointSelect(-1)
	case "1", "2", "3", "4":
		m.endpointPick(int(key[0] - '1'))
	case "c":
		m.endpointCopySelected()
	case "u":
		m.copyEndpoint(m.endpointData.BaseURL, "base URL")
	case "y":
		m.copyEndpoint(m.endpointData.Model, "model ID")
	case "j", "down":
		m.overlayScroll++
	case "k", "up":
		m.overlayScroll--
	case "g":
		m.overlayScroll = 0
	case "G":
		m.overlayScroll = 1 << 20
	case "ctrl+c":
		return false
	default:
		return false
	}
	return true
}
