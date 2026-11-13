package app

import (
	"sort"
	"strconv"
	"strings"

	"wool/internal/reqlog"
)

// reqlogRows bounds the structured request table kept in memory.
const reqlogRows = reqlog.DefaultRows

// newLogLines returns the lines appended since the previous tail. A tail is
// re-read on every poll, so the overlap between the old tail and the new one
// is skipped. When there is no overlap (more than one tail was written, or the
// file was truncated) the caller must reset the parser and feed everything.
func newLogLines(prev, cur []string) ([]string, bool) {
	if len(prev) == 0 || len(cur) == 0 {
		return cur, true
	}
	if len(cur) < len(prev) {
		// A shorter tail means the log was truncated or rotated; a coincidental
		// suffix match would leave the parser state from the old file behind.
		return cur, true
	}
	maxOverlap := len(prev)
	for k := maxOverlap; k > 0; k-- {
		if equalLines(prev[len(prev)-k:], cur[:k]) {
			return cur[k:], false
		}
	}
	return cur, true
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// resetReqlog starts a fresh parser for a log target.
func (m *Model) resetReqlog(name string) {
	m.logKey = name
	m.logSeen = nil
	m.reqlog = reqlog.New(reqlogRows)
	m.logFilter = ""
	m.logScroll = 0
}

// logRowModel attributes a row to a model. Standalone logs carry no model, so
// the daemon's target is used; router children are tagged with their port and
// the router model list supplies the matching ID.
func (m *Model) logRowModel(r reqlog.Row) string {
	if r.Port > 0 {
		if id := m.routerModelForPort(r.Port); id != "" {
			return id
		}
	}
	if m.logTarget() == "router" {
		return "router"
	}
	if m.server.Preset != "" {
		return m.server.Preset
	}
	if m.server.Model != "" {
		return m.server.Model
	}
	return "server"
}

func (m *Model) routerModelForPort(port int) string {
	want := strconv.Itoa(port)
	for _, rm := range m.routerModels {
		args := rm.Status.Args
		for i := 0; i < len(args); i++ {
			switch {
			case args[i] == "--port" && i+1 < len(args) && args[i+1] == want:
				return rm.ID
			case strings.HasPrefix(args[i], "--port=") && strings.TrimPrefix(args[i], "--port=") == want:
				return rm.ID
			}
		}
	}
	return ""
}

// filteredLogRows applies the model filter.
func (m *Model) filteredLogRows() []reqlog.Row {
	if m.reqlog == nil {
		return nil
	}
	rows := m.reqlog.Rows()
	if m.logFilter == "" {
		return rows
	}
	out := make([]reqlog.Row, 0, len(rows))
	for _, r := range rows {
		if m.logRowModel(r) == m.logFilter {
			out = append(out, r)
		}
	}
	return out
}

// logModels lists the models present in the structured tail, sorted.
func (m *Model) logModels() []string {
	if m.reqlog == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range m.reqlog.Rows() {
		id := m.logRowModel(r)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// cycleLogFilter moves through all models, then back to all.
func (m *Model) cycleLogFilter() {
	models := m.logModels()
	if len(models) == 0 {
		m.logFilter = ""
		m.setToast("no models in the request log")
		return
	}
	next := ""
	if m.logFilter == "" {
		next = models[0]
	} else if i := indexOf(models, m.logFilter); i >= 0 && i+1 < len(models) {
		next = models[i+1]
	}
	m.logFilter = next
	m.logScroll = 0
	if next == "" {
		m.setToast("log filter: all models")
	} else {
		m.setToast("log filter: " + next)
	}
}

// logRowCount is what the scroll position applies to: raw lines or the
// filtered structured rows.
func (m *Model) logRowCount() int {
	if m.logRaw || m.reqlog == nil || len(m.reqlog.Rows()) == 0 {
		return len(m.logLines)
	}
	return len(m.filteredLogRows())
}
