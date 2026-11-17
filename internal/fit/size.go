package fit

import (
	"errors"
	"fmt"
	"strings"
)

func ParseSize(raw string) (uint64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, errors.New("size is empty")
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("invalid size %q", raw)
	}
	n, err := parseUint(s[:i])
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", raw, err)
	}
	suffix := strings.ToLower(strings.TrimSpace(s[i:]))
	suffix = strings.TrimSuffix(suffix, "ib")
	suffix = strings.TrimSuffix(suffix, "b")
	var mult uint64 = 1
	switch suffix {
	case "":
	case "k":
		mult = 1 << 10
	case "m":
		mult = 1 << 20
	case "g":
		mult = 1 << 30
	case "t":
		mult = 1 << 40
	default:
		return 0, fmt.Errorf("invalid size suffix %q (want B, K, M, G or T)", s[i:])
	}
	if n != 0 && mult > (^uint64(0))/n {
		return 0, fmt.Errorf("size %q is too large", raw)
	}
	v := n * mult
	if v == 0 {
		return 0, fmt.Errorf("size %q must be greater than zero", raw)
	}
	return v, nil
}

func parseUint(s string) (uint64, error) {
	var n uint64
	for _, r := range s {
		d := uint64(r - '0')
		if d > 9 {
			return 0, fmt.Errorf("not a number")
		}
		if n > (^uint64(0)-d)/10 {
			return 0, fmt.Errorf("number is too large")
		}
		n = n*10 + d
	}
	return n, nil
}

// bytes is a compact human form used in errors and notes.
func bytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
