package bench

import (
	"fmt"
	"time"

	"wool/internal/measure"
)

const (
	// ComparisonVersion is the stable schema version of Comparison JSON.
	ComparisonVersion = 1

	// NoiseThreshold is the relative spread (max-min)/median above which a
	// comparison warns that the result may be noise. It is deliberately coarse:
	// short benchmarks on a shared machine are not precise.
	NoiseThreshold = 0.10
)

// Comparison is the stable JSON report of an interleaved A/B benchmark.
type Comparison struct {
	Version          int       `json:"version"`
	At               time.Time `json:"at"`
	Runs             int       `json:"runs"` // rounds per target
	PromptTokens     int       `json:"prompt_tokens"`
	GenerationTokens int       `json:"generation_tokens"`
	Seed             int       `json:"seed"`
	Interleaved      bool      `json:"interleaved"`
	A                Side      `json:"a"`
	B                Side      `json:"b"`
	Warnings         []string  `json:"warnings,omitempty"`
}

// Side is one target in a comparison.
type Side struct {
	Target             string             `json:"target"`
	TargetKind         string             `json:"target_kind,omitempty"`
	ModelID            string             `json:"model_id"`
	ConfigHash         string             `json:"config_hash,omitempty"`
	Engine             string             `json:"engine,omitempty"`
	LlamaServerVersion string             `json:"llama_server_version,omitempty"`
	GPUName            string             `json:"gpu_name,omitempty"`
	GPUPCIID           string             `json:"gpu_pci_id,omitempty"`
	GPUDriver          string             `json:"gpu_driver,omitempty"`
	Samples            []measure.BenchRun `json:"samples"`
	Median             measure.BenchStats `json:"median"`
	Min                measure.BenchStats `json:"min"`
	Max                measure.BenchStats `json:"max"`
	Spread             Spread             `json:"spread"`
	LoadMS             float64            `json:"load_ms,omitempty"`
	LoadKnown          bool               `json:"load_known"`
	BaselineBytes      uint64             `json:"baseline_bytes,omitempty"`
	PeakBytes          uint64             `json:"peak_bytes,omitempty"`
	VRAMDeltaBytes     uint64             `json:"vram_delta_bytes,omitempty"`
	VRAMKnown          bool               `json:"vram_known"`
}

// Spread is the relative (max-min)/median range for each throughput series.
type Spread struct {
	PromptTPS     float64 `json:"prompt_tps"`
	GenerationTPS float64 `json:"generation_tps"`
}

// NewSide summarizes samples and computes their spread.
func NewSide(samples []measure.BenchRun) Side {
	s := Side{Samples: samples}
	s.Median, s.Min, s.Max = Summarize(samples)
	s.Spread = SpreadOf(s.Min, s.Max, s.Median)
	return s
}

// SideFromRecord builds a comparison side from a stored benchmark record. It
// keeps the stored summary when the record predates raw samples.
func SideFromRecord(r measure.Record) Side {
	s := Side{
		Target:             measure.BenchTargetName(r),
		TargetKind:         r.TargetKind,
		ModelID:            r.ModelID,
		ConfigHash:         r.ConfigHash,
		LlamaServerVersion: r.LlamaServerVersion,
		GPUName:            r.GPUName,
		GPUPCIID:           r.GPUPCIID,
		GPUDriver:          r.GPUDriver,
	}
	if b := r.Bench; b != nil {
		if len(b.Samples) > 0 {
			s.Samples = b.Samples
			s.Median, s.Min, s.Max = Summarize(b.Samples)
		} else {
			s.Median, s.Min, s.Max = b.Median, b.Min, b.Max
		}
		s.Spread = SpreadOf(s.Min, s.Max, s.Median)
		s.Engine = b.Engine
		s.LoadMS, s.LoadKnown = b.LoadMS, b.LoadKnown
		s.BaselineBytes, s.PeakBytes = b.BaselineBytes, b.PeakBytes
		s.VRAMDeltaBytes, s.VRAMKnown = b.VRAMDeltaBytes, b.VRAMKnown
	}
	return s
}

// SpreadOf returns the relative range of each series. A zero or negative
// median has no meaningful spread.
func SpreadOf(min, max, median measure.BenchStats) Spread {
	return Spread{
		PromptTPS:     relativeSpread(min.PromptTPS, max.PromptTPS, median.PromptTPS),
		GenerationTPS: relativeSpread(min.GenerationTPS, max.GenerationTPS, median.GenerationTPS),
	}
}

func relativeSpread(min, max, median float64) float64 {
	if median <= 0 || max <= min {
		return 0
	}
	return (max - min) / median
}

// Delta is the percentage change from base to next. A zero base yields zero.
func Delta(base, next float64) float64 {
	if base <= 0 {
		return 0
	}
	return (next - base) / base * 100
}

// Median returns the middle value of values, or 0 when empty.
func Median(values []float64) float64 {
	return medianOf(values)
}

// BuildWarnings fills Warnings with noise and environment cautions. It is
// called after both sides are populated.
func (c *Comparison) BuildWarnings() {
	c.Warnings = nil
	for _, s := range []struct {
		label string
		side  Side
	}{{"A", c.A}, {"B", c.B}} {
		if s.side.Spread.PromptTPS > NoiseThreshold {
			c.Warnings = append(c.Warnings, fmt.Sprintf(
				"prompt t/s spread on %s is %.1f%% (above %.0f%%); the prompt result may be noise",
				s.label, s.side.Spread.PromptTPS*100, NoiseThreshold*100))
		}
		if s.side.Spread.GenerationTPS > NoiseThreshold {
			c.Warnings = append(c.Warnings, fmt.Sprintf(
				"generation t/s spread on %s is %.1f%% (above %.0f%%); the difference may be noise",
				s.label, s.side.Spread.GenerationTPS*100, NoiseThreshold*100))
		}
	}
	if gpuChanged(c.A, c.B) {
		c.Warnings = append(c.Warnings, fmt.Sprintf(
			"GPU changed between runs: %s vs %s", gpuLabel(c.A), gpuLabel(c.B)))
	}
	if c.A.GPUDriver != "" && c.B.GPUDriver != "" && c.A.GPUDriver != c.B.GPUDriver {
		c.Warnings = append(c.Warnings, fmt.Sprintf(
			"GPU driver changed between runs: %s vs %s", c.A.GPUDriver, c.B.GPUDriver))
	}
	if c.A.LlamaServerVersion != "" && c.B.LlamaServerVersion != "" &&
		c.A.LlamaServerVersion != c.B.LlamaServerVersion {
		c.Warnings = append(c.Warnings, fmt.Sprintf(
			"llama-server version changed between runs: %s vs %s",
			c.A.LlamaServerVersion, c.B.LlamaServerVersion))
	}
}

func gpuChanged(a, b Side) bool {
	if a.GPUPCIID != "" && b.GPUPCIID != "" {
		return a.GPUPCIID != b.GPUPCIID
	}
	return a.GPUName != "" && b.GPUName != "" && a.GPUName != b.GPUName
}

func gpuLabel(s Side) string {
	switch {
	case s.GPUName != "" && s.GPUPCIID != "":
		return s.GPUName + " (" + s.GPUPCIID + ")"
	case s.GPUName != "":
		return s.GPUName
	case s.GPUPCIID != "":
		return s.GPUPCIID
	default:
		return "unknown"
	}
}
