package app

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"sort"
	"time"
	"wool/internal/config"
	"wool/internal/ui"
)

func sides(left, right string, width int) string {
	rw := lipgloss.Width(right)
	if rw+1 >= width {
		return ui.Fit(right, width)
	}
	return ui.Fit(left, width-rw-1) + " " + right
}

func kv(k, v string) string {
	return k + " " + v
}

func fallback(s, alt string) string {
	return ui.Fallback(s, alt)
}

func boolWord(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func truncateRight(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n]
}

func shortenPath(p string, w int) string {
	if len(p) <= w {
		return p
	}
	if w <= 8 {
		return p[len(p)-w:]
	}
	return "…" + p[len(p)-(w-1):]
}

func windowStart(sel, visible, total int) int {
	if visible <= 0 || total <= visible {
		return 0
	}
	start := sel - visible/2
	if start < 0 {
		start = 0
	}
	if start+visible > total {
		start = total - visible
	}
	return start
}

func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// uptimeSince renders a process uptime, or "-" when the start time is unknown
// (for example a state file written by an older version).
func uptimeSince(start time.Time) string {
	if start.IsZero() {
		return "-"
	}
	return shortDuration(time.Since(start))
}

func trimFloat(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%.1f", f)
}

// humanTokens renders a token count with k/M suffixes.
func humanTokens(n int) string {
	return ui.HumanTokens(n)
}

// humanTokenCount is the uint64 form used for persisted usage counters.
func humanTokenCount(n uint64) string {
	return ui.HumanTokenCount(n)
}

// relativeAge renders a last-used time as a short age for the router table.
func relativeAge(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// value2 returns the newest value of a series, scaled by max.
func latestFraction(series []float64, max float64) float64 {
	if len(series) == 0 || max <= 0 {
		return 0
	}
	return series[len(series)-1] / max
}

// abs expands ~ and makes a model path absolute.
func abs(p string) string {
	return config.AbsPath(p)
}
