package app

import (
	"fmt"
	"strings"
	"wool/internal/gguf"
	"wool/internal/measure"
	"wool/internal/ui"
)

func (m *Model) fitLine(w int) []string {
	st := m.render.Styles
	fi := m.fit
	if fi.Err != "" {
		return []string{st.Warn.Render("fit: " + fi.Err)}
	}
	if !fi.Ready {
		return []string{st.Muted.Render("fit: model file not readable")}
	}
	est := fi.Est
	budget := m.budget()
	lines := []string{st.Muted.Render("fit " + fitBreakdown(est))}
	if fi.HasCalib {
		lines = append(lines, st.Muted.Render(calibrationText(est.Total, fi.Calib)))
	}
	if fi.HasVerify {
		if fi.Verify.OK() {
			lines = append(lines, st.Good.Render(fmt.Sprintf("✓ verified %s · %d ms",
				fi.Verify.At.Local().Format("01-02 15:04"), int64(fi.Verify.LatencyMS))))
		} else {
			lines = append(lines, st.Warn.Render("smoke test failed: "+fi.Verify.Err))
		}
	}
	if !budget.Known {
		return append(lines, st.Muted.Render("budget unknown (no GPU VRAM detected)"))
	}
	head := budget.Headroom(est)
	style := st.Good
	verdict := "headroom " + ui.HumanBytes(uint64(head))
	if head < 0 {
		style = st.Bad
		verdict = "over by " + ui.HumanBytes(uint64(-head))
	} else if budget.Available > 0 && ui.Percent(est.Total, budget.Available) > 90 {
		style = st.Warn
	}
	pct := ui.Percent(est.Total, budget.Available)
	barW := clamp(w-28, 10, 26)
	lines = append(lines, m.fitBar(est, budget, barW)+" "+
		style.Render(fmt.Sprintf("%3.0f%% %s", pct, verdict)))
	detail := fmt.Sprintf("budget %s · used %s of %s · reserve %s",
		ui.HumanBytes(budget.Available), ui.HumanBytes(budget.Used),
		ui.HumanBytes(budget.Total), ui.HumanBytes(budget.Reserve))
	if budget.OwnKnown && budget.OwnUsed > 0 {
		detail += " · server " + ui.HumanBytes(budget.OwnUsed)
	}
	lines = append(lines, st.Muted.Render(detail+" · limit "+est.LimitingTerm()))
	return lines
}

// fitBreakdown renders the VRAM estimate as gpu weights + KV + overhead = total,
// with the CPU-resident weight term and the approximate flag alongside.
func fitBreakdown(e gguf.Estimate) string {
	s := fmt.Sprintf("gpu %s + kv %s + overhead %s = %s",
		ui.HumanBytes(e.GPUWeights), ui.HumanBytes(e.KVBytes),
		ui.HumanBytes(e.Overhead), ui.HumanBytes(e.Total))
	if e.CPUWeights > 0 {
		s += " · cpu " + ui.HumanBytes(e.CPUWeights)
	}
	if e.Approximate {
		s += " · approx"
	}
	return s
}

// calibrationText compares the live estimate with the observations for the
// current model and environment. It always shows the raw estimate and the
// sample count; the corrected value appears only once there are enough samples.
func calibrationText(estimate uint64, c measure.Calibration) string {
	switch {
	case c.Samples == 0 && c.Unknown > 0:
		return fmt.Sprintf("observed unknown (n=%d)", c.Unknown)
	case c.Samples == 0 && c.Expired > 0:
		return fmt.Sprintf("estimate %s · no current observations (%d expired)",
			ui.HumanBytes(estimate), c.Expired)
	case c.Samples == 0:
		return fmt.Sprintf("estimate %s · no observations", ui.HumanBytes(estimate))
	case !c.OK():
		return fmt.Sprintf("estimate %s · observed %s (n=%d)",
			ui.HumanBytes(estimate), ui.HumanBytes(c.ObservedBytes), c.Samples)
	}
	text := fmt.Sprintf("estimate %s · corrected %s · observed %s (n=%d, ±%.0f%%)",
		ui.HumanBytes(estimate), ui.HumanBytes(c.Corrected(estimate)),
		ui.HumanBytes(c.ObservedBytes), c.Samples, c.Spread*100)
	if c.Clamped {
		text += " · clamped"
	}
	return text
}

// fitBar draws the estimate as segments against the GPU's total VRAM.
func (m *Model) fitBar(e gguf.Estimate, b gguf.Budget, width int) string {
	st := m.render.Styles
	if width <= 0 {
		return ""
	}
	scale := e.Total
	if b.Known && b.Total > scale {
		scale = b.Total
	}
	if scale == 0 {
		return st.Muted.Render(strings.Repeat("░", width))
	}
	cells := func(v uint64) int {
		return int(float64(v) / float64(scale) * float64(width))
	}
	wc, kc, oc := cells(e.GPUWeights), cells(e.KVBytes), cells(e.Overhead)
	for wc+kc+oc > width {
		switch {
		case oc > 0:
			oc--
		case kc > 0:
			kc--
		default:
			wc--
		}
	}
	free := width - wc - kc - oc
	return st.Accent.Render(strings.Repeat("█", wc)) +
		st.Warn.Render(strings.Repeat("▓", kc)) +
		st.Muted.Render(strings.Repeat("▒", oc)) +
		st.Muted.Render(strings.Repeat("░", free))
}

func signedBytes(n int64) string {
	switch {
	case n > 0:
		return "+" + ui.HumanBytes(uint64(n))
	case n < 0:
		return "-" + ui.HumanBytes(uint64(-n))
	default:
		return "0 B"
	}
}
