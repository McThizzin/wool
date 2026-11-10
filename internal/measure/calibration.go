package measure

import (
	"math"
	"sort"
	"time"
)

const (
	// MinCalibrationSamples is the number of usable observations required
	// before a correction is applied. Below it the raw estimate is used.
	MinCalibrationSamples = 3

	// MinCalibrationRatio and MaxCalibrationRatio clamp the median
	// observed/estimate ratio so one bad observation cannot dominate.
	MinCalibrationRatio = 0.5
	MaxCalibrationRatio = 2.0
)

// Identity describes the environment a sample was taken in. Zero values mean
// "unknown" and never expire a sample. The model size and modification time
// stand in for the model file identity.
type Identity struct {
	ModelSize          uint64
	ModelModTime       time.Time
	GPUPCIID           string
	GPUDriver          string
	LlamaServerVersion string
}

// Calibration summarizes observations for one launch shape and environment.
// It is a hint, not a measurement: the raw estimate and sample count are always
// kept alongside it.
type Calibration struct {
	Samples     int     `json:"samples"`
	Unknown     int     `json:"unknown,omitempty"`
	Expired     int     `json:"expired,omitempty"`
	MedianRatio float64 `json:"median_ratio"`
	Ratio       float64 `json:"ratio"`
	Clamped     bool    `json:"clamped"`
	Spread      float64 `json:"spread"`
	// ObservedBytes and EstimateBytes are the medians of the usable samples.
	ObservedBytes uint64 `json:"observed_bytes"`
	EstimateBytes uint64 `json:"estimate_bytes"`
}

// OK reports whether the calibration has enough samples to correct an estimate.
func (c Calibration) OK() bool {
	return c.Samples >= MinCalibrationSamples && c.Ratio > 0
}

// Corrected applies the clamped ratio to an estimate. Without a usable
// calibration it returns the raw estimate unchanged.
func (c Calibration) Corrected(estimate uint64) uint64 {
	if !c.OK() || estimate == 0 {
		return estimate
	}
	return uint64(math.Round(float64(estimate) * c.Ratio))
}

// Calibration groups observations by launch shape and environment and returns
// the median observed/estimate ratio. The bool is false when there is nothing
// to report for the shape.
func (s *Store) Calibration(shape Shape, id Identity) (Calibration, bool) {
	records, err := s.All()
	if err != nil {
		return Calibration{}, false
	}

	var cal Calibration
	var ratios, observed, estimates []float64
	for _, r := range records {
		if r.Kind != KindObservation || r.Shape() != shape {
			continue
		}
		if expired(r, id) {
			cal.Expired++
			continue
		}
		if !r.BaselineKnown || r.ObservedBytes == 0 || r.EstimateBytes == 0 {
			cal.Unknown++
			continue
		}
		ratios = append(ratios, float64(r.ObservedBytes)/float64(r.EstimateBytes))
		observed = append(observed, float64(r.ObservedBytes))
		estimates = append(estimates, float64(r.EstimateBytes))
	}

	cal.Samples = len(ratios)
	if cal.Samples == 0 {
		return cal, cal.Unknown > 0 || cal.Expired > 0
	}

	cal.MedianRatio = medianFloat(ratios)
	cal.Ratio = cal.MedianRatio
	if cal.Ratio < MinCalibrationRatio {
		cal.Ratio, cal.Clamped = MinCalibrationRatio, true
	}
	if cal.Ratio > MaxCalibrationRatio {
		cal.Ratio, cal.Clamped = MaxCalibrationRatio, true
	}
	cal.Spread = spreadFloat(ratios, cal.MedianRatio)
	cal.ObservedBytes = uint64(math.Round(medianFloat(observed)))
	cal.EstimateBytes = uint64(math.Round(medianFloat(estimates)))
	return cal, true
}

// expired reports whether a sample came from a different model file, GPU,
// driver or llama-server version than the current identity. Unknown fields on
// either side do not expire a sample because the difference cannot be proven.
func expired(r Record, id Identity) bool {
	if id.ModelSize > 0 && r.ModelSize > 0 && id.ModelSize != r.ModelSize {
		return true
	}
	if !id.ModelModTime.IsZero() && !r.ModelModTime.IsZero() &&
		!id.ModelModTime.Equal(r.ModelModTime) {
		return true
	}
	if id.GPUPCIID != "" && r.GPUPCIID != "" && id.GPUPCIID != r.GPUPCIID {
		return true
	}
	if id.GPUDriver != "" && r.GPUDriver != "" && id.GPUDriver != r.GPUDriver {
		return true
	}
	if id.LlamaServerVersion != "" && r.LlamaServerVersion != "" &&
		id.LlamaServerVersion != r.LlamaServerVersion {
		return true
	}
	return false
}

func medianFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// spreadFloat is the relative (max-min)/median range.
func spreadFloat(values []float64, median float64) float64 {
	if len(values) < 2 || median <= 0 {
		return 0
	}
	min, max := values[0], values[0]
	for _, v := range values[1:] {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	return (max - min) / median
}
