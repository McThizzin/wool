package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"wool/internal/theme"
)

// Renderer owns the theme-dependent drawing helpers.
type Renderer struct {
	Theme  theme.Theme
	Styles theme.Styles
	Icons  theme.Icons
}

func New(t theme.Theme, nerdFonts bool) Renderer {
	icons := theme.ASCII
	if nerdFonts {
		icons = theme.Nerd
	}
	return Renderer{Theme: t, Styles: theme.NewStyles(t), Icons: icons}
}

// Fit truncates or pads s to exactly w cells (ANSI-aware).
func Fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = lipgloss.NewStyle().MaxWidth(w).Render(s) + "\x1b[0m"
	}
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// Panel draws a rounded box with the title embedded in the top border.
// h includes the border rows; w includes the border columns.
func (r Renderer) Panel(title, right string, body []string, w, h int, focused bool) string {
	if w < 6 || h < 2 {
		return ""
	}
	bc, titleFg := r.Theme.Border, r.Theme.Text
	if focused {
		bc, titleFg = r.Theme.Accent, r.Theme.Accent
	}
	bs := lipgloss.NewStyle().Foreground(bc)
	ts := lipgloss.NewStyle().Bold(true).Foreground(titleFg)
	iw := w - 2

	tw := lipgloss.Width(title)
	rightW, rightStr := 1, bs.Render("╮")
	if right != "" {
		rightW = lipgloss.Width(right) + 4
		rightStr = bs.Render(" ") + right + bs.Render(" ─╮")
	}
	fill := w - (4 + tw) - rightW
	if fill < 0 && right != "" {
		rightW, rightStr = 1, bs.Render("╮")
		fill = w - (4 + tw) - rightW
	}
	if fill < 0 {
		title = lipgloss.NewStyle().MaxWidth(max(w-5, 1)).Render(title)
		tw = lipgloss.Width(title)
		fill = max(w-(4+tw)-rightW, 0)
	}

	var sb strings.Builder
	sb.WriteString(bs.Render("╭─ ") + ts.Render(title) + bs.Render(" "+strings.Repeat("─", fill)) + rightStr)
	for i := 0; i < h-2; i++ {
		l := ""
		if i < len(body) {
			l = body[i]
		}
		sb.WriteString("\n" + bs.Render("│") + Fit(l, iw) + bs.Render("│"))
	}
	sb.WriteString("\n" + bs.Render("╰"+strings.Repeat("─", iw)+"╯"))
	return sb.String()
}

// Chip is a padded badge.
func (r Renderer) Chip(text string, bg lipgloss.Color) string {
	return r.Styles.ChipText.Background(bg).Padding(0, 1).Render(text)
}

// HumanBytes formats a byte count with binary units.
func HumanBytes(n uint64) string {
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

// Percent returns 0..100, guarding against a zero total.
func Percent(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

// Sparkline renders the last values as block characters, left-padded if needed.
func Sparkline(values []float64, width int) string {
	if width <= 0 {
		return ""
	}
	blocks := []rune("▁▂▃▄▅▆▇█")
	if len(values) == 0 {
		return strings.Repeat(" ", width)
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	lo, hi := values[0], values[0]
	for _, v := range values[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	span := hi - lo
	var b strings.Builder
	if pad := width - len(values); pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	for _, v := range values {
		idx := 0
		if span > 0 {
			idx = int((v - lo) / span * float64(len(blocks)-1))
		}
		if idx < 0 {
			idx = 0
		}
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		b.WriteRune(blocks[idx])
	}
	return b.String()
}

// Bar renders a horizontal percentage bar.
func Bar(percent float64, width int, filled, empty rune) string {
	if width <= 0 {
		return ""
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	n := int(percent / 100 * float64(width))
	if n > width {
		n = width
	}
	return strings.Repeat(string(filled), n) + strings.Repeat(string(empty), width-n)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
