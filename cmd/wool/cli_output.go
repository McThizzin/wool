package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"wool/internal/ui"
)

func durationOr(s string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return fallback
}

func humanTokensCLI(n int) string {
	return ui.HumanTokens(n)
}

// trimTokens drops the fraction of a token count when it is integral.
func trimTokens(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%.1f", f)
}

func writeJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "wool: %v\n", err)
		return 1
	}
	return 0
}
func fallbackDash(s string) string {
	return ui.Fallback(s, "-")
}
