package ui

import "fmt"

// HumanTokens renders a token count with k/M suffixes.
func HumanTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// HumanTokenCount is the uint64 form used for persisted usage counters.
func HumanTokenCount(n uint64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fG", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// Fallback returns s, or alt when s is empty.
func Fallback(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}
