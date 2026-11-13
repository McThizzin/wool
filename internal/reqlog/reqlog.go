// Package reqlog turns llama-server request-completion log lines into rows.
//
// The parser is deliberately isolated and stateful: llama-server prints a
// request's prompt size on one line, its timings on the next three, and its
// HTTP status on yet another. It keeps a bounded tail of completed rows and
// ignores anything it does not recognise. Formats drift between versions, so
// this is best effort, not a contract.
package reqlog

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Row is one completed request.
type Row struct {
	At           time.Time `json:"at"`
	Model        string    `json:"model,omitempty"`
	Port         int       `json:"port,omitempty"`
	PromptTokens int       `json:"prompt_tokens"`
	GenTokens    int       `json:"gen_tokens"`
	PromptMS     float64   `json:"prompt_ms"`
	GenMS        float64   `json:"gen_ms"`
	TotalMS      float64   `json:"total_ms"`
	CacheHits    int       `json:"cache_hits"`
	Status       int       `json:"status"`
}

// DefaultRows is the bounded tail kept by a parser.
const DefaultRows = 200

// Parser consumes log lines in order. Feed is cheap and allocation-light;
// call Reset when the log is truncated or the target changes.
type Parser struct {
	rows []Row
	max  int

	pending map[key]*pending
	timing  key
	hasTime bool

	recognized int
}

type key struct {
	slot int
	task int
}

type pending struct {
	port         int
	promptTokens int
	promptEval   int
	promptMS     float64
	genTokens    int
	genMS        float64
}

// New builds a parser that keeps at most maxRows completed rows. A non-positive
// value uses DefaultRows.
func New(maxRows int) *Parser {
	if maxRows <= 0 {
		maxRows = DefaultRows
	}
	return &Parser{max: maxRows, pending: map[key]*pending{}}
}

// Reset forgets every row and all in-flight request state.
func (p *Parser) Reset() {
	p.rows = nil
	p.pending = map[key]*pending{}
	p.timing = key{}
	p.hasTime = false
	p.recognized = 0
}

// Rows returns the completed rows oldest first. The slice is owned by the
// parser and must not be modified by the caller.
func (p *Parser) Rows() []Row { return p.rows }

// Recognized reports whether any known request-log line was seen. When it is
// false the log is either empty or in an unknown format, so callers can fall
// back to the raw log and say so.
func (p *Parser) Recognized() bool { return p.recognized > 0 }

var (
	reIDs    = regexp.MustCompile(`\bid\s+(\d+)\s*\|\s*task\s+(-?\d+)\s*\|`)
	rePort   = regexp.MustCompile(`^\s*\[\s*(\d+)\]\s*`)
	rePrompt = regexp.MustCompile(`(?:n_prompt_tokens|task\.n_tokens)\s*=\s*(\d+)`)
	rePEval  = regexp.MustCompile(`prompt eval time\s*=\s*([0-9.]+)\s*ms\s*/\s*(\d+)\s*tokens`)
	reEval   = regexp.MustCompile(`\beval time\s*=\s*([0-9.]+)\s*ms\s*/\s*(\d+)\s*tokens`)
	reTotal  = regexp.MustCompile(`\btotal time\s*=\s*([0-9.]+)\s*ms\s*/\s*(\d+)\s*tokens`)
	reStatus = regexp.MustCompile(`log_server_r:.*?\s(\d{3})\s*$`)
)

// Feed consumes one log line. at is the best available timestamp; wool does
// not pass --log-timestamps, so in the TUI it is when the line was first seen.
func (p *Parser) Feed(line string, at time.Time) {
	s := strings.TrimRight(line, "\r")
	port := 0
	if m := rePort.FindStringSubmatch(s); m != nil {
		port, _ = strconv.Atoi(m[1])
	}

	switch {
	case strings.Contains(s, "new prompt"):
		k, ok := ids(s)
		if !ok || k.task < 0 {
			return
		}
		m := rePrompt.FindStringSubmatch(s)
		if m == nil {
			return
		}
		n, _ := strconv.Atoi(m[1])
		p.entry(k).promptTokens = n
		if port > 0 {
			p.entry(k).port = port
		}
		p.recognized++

	case strings.Contains(s, "print_timing"):
		k, ok := ids(s)
		if !ok || k.task < 0 {
			return
		}
		if m := rePEval.FindStringSubmatch(s); m != nil {
			e := p.entry(k)
			e.promptMS, e.promptEval = parseTiming(m)
			if port > 0 {
				e.port = port
			}
			p.timing, p.hasTime = k, true
			p.recognized++
			return
		}
		// Older versions print only the prefix, then the three timing lines.
		// Progress lines carry a payload and must not start a timing block.
		if strings.TrimSpace(tail(s)) == "" {
			p.timing, p.hasTime = k, true
			if port > 0 {
				p.entry(k).port = port
			}
			p.recognized++
		}

	case strings.Contains(s, "prompt eval time ="):
		if !p.hasTime {
			return
		}
		if m := rePEval.FindStringSubmatch(s); m != nil {
			e := p.entry(p.timing)
			e.promptMS, e.promptEval = parseTiming(m)
			p.recognized++
		}

	case strings.Contains(s, "eval time ="):
		if !p.hasTime {
			return
		}
		if m := reEval.FindStringSubmatch(s); m != nil {
			e := p.entry(p.timing)
			e.genMS, e.genTokens = parseTiming(m)
			p.recognized++
		}

	case strings.Contains(s, "total time ="):
		if !p.hasTime {
			return
		}
		m := reTotal.FindStringSubmatch(s)
		if m == nil {
			return
		}
		totalMS, _ := strconv.ParseFloat(m[1], 64)
		totalTokens, _ := strconv.Atoi(m[2])
		p.emit(p.timing, at, totalMS, totalTokens)
		p.recognized++

	case strings.Contains(s, "log_server_r:"):
		// The status line carries no slot/task, so it cannot be joined to a
		// timing block. Completed timing blocks are successful requests; only
		// error statuses become rows of their own.
		m := reStatus.FindStringSubmatch(s)
		if m == nil {
			return
		}
		code, _ := strconv.Atoi(m[1])
		p.recognized++
		if code >= 400 {
			p.append(Row{At: at, Port: port, Status: code})
		}
	}
}

// entry returns the in-flight state for a slot/task pair, creating it lazily.
func (p *Parser) entry(k key) *pending {
	e := p.pending[k]
	if e == nil {
		e = &pending{}
		p.pending[k] = e
	}
	return e
}

// emit closes one timing block and appends its row.
func (p *Parser) emit(k key, at time.Time, totalMS float64, totalTokens int) {
	e := p.pending[k]
	if e == nil {
		p.hasTime = false
		return
	}
	if e.genTokens == 0 && totalTokens > e.promptEval {
		e.genTokens = totalTokens - e.promptEval
	}
	promptTokens := e.promptTokens
	if promptTokens == 0 {
		// The tail began after the new-prompt line; report what was processed.
		promptTokens = e.promptEval
	}
	cache := promptTokens - e.promptEval
	if cache < 0 {
		cache = 0
	}
	p.append(Row{
		At:           at,
		Port:         e.port,
		PromptTokens: promptTokens,
		GenTokens:    e.genTokens,
		PromptMS:     e.promptMS,
		GenMS:        e.genMS,
		TotalMS:      totalMS,
		CacheHits:    cache,
		Status:       200,
	})
	delete(p.pending, k)
	p.hasTime = false
	p.timing = key{}
}

// append keeps the row tail bounded.
func (p *Parser) append(r Row) {
	p.rows = append(p.rows, r)
	if len(p.rows) > p.max {
		p.rows = p.rows[len(p.rows)-p.max:]
	}
}

// ids extracts the slot and task from a line such as
// "slot print_timing: id 0 | task 12 | ...".
func ids(s string) (key, bool) {
	m := reIDs.FindStringSubmatch(s)
	if m == nil {
		return key{}, false
	}
	slot, _ := strconv.Atoi(m[1])
	task, _ := strconv.Atoi(m[2])
	return key{slot: slot, task: task}, true
}

// tail returns the text after the last "|" in a slot line.
func tail(s string) string {
	if i := strings.LastIndexByte(s, '|'); i >= 0 {
		return s[i+1:]
	}
	return ""
}

// parseTiming reads a "<ms> ms / <tokens> tokens" pair.
func parseTiming(m []string) (float64, int) {
	ms, _ := strconv.ParseFloat(m[1], 64)
	n, _ := strconv.Atoi(m[2])
	return ms, n
}
