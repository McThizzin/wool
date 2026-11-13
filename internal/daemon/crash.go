package daemon

import (
	"regexp"
	"strings"
	"time"
)

// CrashReason is the coarse cause of a dead server, inferred from its log.
// The values are stable so the TUI, CLI and future JSON output agree.
type CrashReason string

const (
	CrashOOM     CrashReason = "oom"
	CrashPort    CrashReason = "port"
	CrashModel   CrashReason = "model"
	CrashSignal  CrashReason = "signal"
	CrashUnknown CrashReason = "unknown"
)

// Label is the human-facing form of a reason.
func (r CrashReason) Label() string {
	switch r {
	case CrashOOM:
		return "out of memory"
	case CrashPort:
		return "port conflict"
	case CrashModel:
		return "model load failure"
	case CrashSignal:
		return "terminated by signal"
	default:
		return "unknown"
	}
}

// Crash is one detected failure. Lines holds the log lines that explain it,
// already limited to the current launch.
type Crash struct {
	At     time.Time   `json:"at"`
	Reason CrashReason `json:"reason"`
	Detail string      `json:"detail,omitempty"`
	Lines  []string    `json:"lines,omitempty"`
}

const maxCrashLines = 6

// crashPatterns are checked in precedence order. A log can mention several
// conditions (for example a failed allocation during a model load); the first
// match wins so the card does not report the symptom instead of the cause.
var crashPatterns = []struct {
	reason CrashReason
	re     *regexp.Regexp
}{
	{CrashOOM, regexp.MustCompile(`(?i)(out of memory|outofmemory|\boom\b|failed to allocate|cuda error:.*memory|memory allocation failed)`)},
	{CrashPort, regexp.MustCompile(`(?i)(address already in use|failed to bind|bind.*failed|couldn't bind|port.*already.*in use|listen.*failed)`)},
	{CrashModel, regexp.MustCompile(`(?i)(failed to load model|error loading model|model load failed|failed to load|unable to load model|invalid model)`)},
	{CrashSignal, regexp.MustCompile(`(?i)(received signal|terminated by signal|signal: (killed|terminated|aborted|segmentation)|sigkill|sigterm|segmentation fault|core dumped|killed by signal)`)},
}

// ClassifyCrash inspects a log tail and reports the most likely cause plus the
// lines that support it. Only the newest launch section is considered, because
// the log is appended to across restarts.
func ClassifyCrash(lines []string) Crash {
	run := currentRun(lines)
	for _, p := range crashPatterns {
		matches := matchingLines(run, p.re)
		if len(matches) == 0 {
			continue
		}
		return Crash{
			Reason: p.reason,
			Detail: matches[len(matches)-1],
			Lines:  lastLines(matches, maxCrashLines),
		}
	}
	tail := lastLines(run, maxCrashLines)
	c := Crash{Reason: CrashUnknown, Lines: tail}
	if len(tail) > 0 {
		c.Detail = tail[len(tail)-1]
	}
	return c
}

// currentRun drops everything before the newest manager launch header.
func currentRun(lines []string) []string {
	start := 0
	for i, line := range lines {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "=== ") && strings.Contains(s, " starting ") {
			start = i + 1
		}
	}
	return lines[start:]
}

func matchingLines(lines []string, re *regexp.Regexp) []string {
	var out []string
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if s != "" && re.MatchString(s) {
			out = append(out, s)
		}
	}
	return out
}

func lastLines(lines []string, n int) []string {
	var out []string
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}
