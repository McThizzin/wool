package ui

import "testing"

func TestHumanTokens(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1_000, "1.0k"},
		{1_500, "1.5k"},
		{1_000_000, "1.0M"},
		{2_250_000, "2.2M"},
	}
	for _, tc := range tests {
		if got := HumanTokens(tc.n); got != tc.want {
			t.Errorf("HumanTokens(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestHumanTokenCount(t *testing.T) {
	tests := []struct {
		n    uint64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1_000, "1.0k"},
		{1_500, "1.5k"},
		{1_000_000, "1.0M"},
		{2_250_000, "2.2M"},
		{1_000_000_000, "1.0G"},
		{2_500_000_000, "2.5G"},
	}
	for _, tc := range tests {
		if got := HumanTokenCount(tc.n); got != tc.want {
			t.Errorf("HumanTokenCount(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestHumanCount(t *testing.T) {
	tests := []struct {
		n    uint64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1_000, "1.0k"},
		{1_500, "1.5k"},
		{1_000_000, "1.0M"},
		{2_250_000, "2.2M"},
		{1_000_000_000, "1.0B"},
		{7_000_000_000, "7.0B"},
	}
	for _, tc := range tests {
		if got := HumanCount(tc.n); got != tc.want {
			t.Errorf("HumanCount(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		n    uint64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
	}
	for _, tc := range tests {
		if got := HumanBytes(tc.n); got != tc.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestFallback(t *testing.T) {
	if got := Fallback("value", "-"); got != "value" {
		t.Errorf("Fallback(value) = %q, want value", got)
	}
	if got := Fallback("", "-"); got != "-" {
		t.Errorf("Fallback(empty) = %q, want -", got)
	}
}
