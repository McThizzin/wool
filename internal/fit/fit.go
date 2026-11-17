// Package fit solves the inverse of the VRAM estimate: given a budget, it
// finds the largest context that fits and proposes fallbacks when the target
// settings cannot hold a useful context.
package fit

import (
	"errors"

	"wool/internal/gguf"
	"wool/internal/measure"
)

const (
	// DefaultMinContext is the smallest context the solver treats as useful.
	DefaultMinContext = 2048
	// ContextStep keeps recommendations on a stable, readable grid.
	ContextStep = 256
)

// Fallback values explain why a recommendation differs from the requested
// settings. The empty string means the requested settings were kept.
const (
	FallbackNone         = ""
	FallbackCache        = "cache"
	FallbackOffload      = "offload"
	FallbackCacheOffload = "cache+offload"
)

// Errors reported by Solve. Callers should use errors.Is to distinguish them.
var (
	ErrNoBudget   = errors.New("VRAM budget is unknown")
	ErrNoMetadata = errors.New("model metadata cannot drive a fit")
	ErrNoFit      = errors.New("no useful context fits the VRAM budget")
)

// Settings are the launch options the solver can change.
type Settings struct {
	Ctx        int    `json:"ctx"`
	GPULayers  int    `json:"gpu_layers"`
	NCPUMoE    int    `json:"n_cpu_moe"`
	CacheTypeK string `json:"cache_type_k"`
	CacheTypeV string `json:"cache_type_v"`
}

// Request describes one fit query. FileBytes and Meta must come from the
// target GGUF; Settings is the resolved launch configuration.
type Request struct {
	ModelID       string
	ModelPath     string
	Meta          gguf.Meta
	FileBytes     uint64
	Budget        gguf.Budget
	Settings      Settings
	MinContext    int // zero uses DefaultMinContext
	NativeContext int // zero uses Meta.CtxTrain
}

// Recommendation is one fitting setting. All returned recommendations have
// Estimate.Total <= Budget.Available.
type Recommendation struct {
	Rank       int           `json:"rank"`
	Ctx        int           `json:"ctx"`
	GPULayers  int           `json:"gpu_layers"`
	NCPUMoE    int           `json:"n_cpu_moe"`
	CacheTypeK string        `json:"cache_type_k"`
	CacheTypeV string        `json:"cache_type_v"`
	Estimate   gguf.Estimate `json:"estimate"`
	Headroom   int64         `json:"headroom_bytes"`
	Limiting   string        `json:"limiting_term"`
	Fallback   string        `json:"fallback,omitempty"`
	Fits       bool          `json:"fits"`
	Changes    []string      `json:"changes,omitempty"`

	// Calibration is populated by `wool fit --calibrated`. It never changes
	// Estimate; Corrected and CorrectedHeadroom are the calibrated view.
	Calibration       *measure.Calibration `json:"calibration,omitempty"`
	Calibrated        bool                 `json:"calibrated,omitempty"`
	Corrected         uint64               `json:"corrected_bytes,omitempty"`
	CorrectedHeadroom int64                `json:"corrected_headroom_bytes,omitempty"`
}

// Result is the stable JSON shape returned by the CLI.
type Result struct {
	Target         string           `json:"target,omitempty"`
	Kind           string           `json:"kind,omitempty"`
	ModelID        string           `json:"model"`
	ModelPath      string           `json:"path"`
	Budget         gguf.Budget      `json:"budget"`
	NativeContext  int              `json:"native_context"`
	MinimumContext int              `json:"minimum_context"`
	Requested      Settings         `json:"requested"`
	Recommendation Recommendation   `json:"recommendation"`
	Alternatives   []Recommendation `json:"alternatives"`
	Approximate    bool             `json:"approximate"`
	SavedPreset    string           `json:"saved_preset,omitempty"`
	Calibrated     bool             `json:"calibrated,omitempty"`
	Notes          []string         `json:"notes,omitempty"`
}

type tier int

const (
	tierRequested tier = iota
	tierCache
	tierOffload
	tierCombined
)

func (t tier) fallback() string {
	switch t {
	case tierCache:
		return FallbackCache
	case tierOffload:
		return FallbackOffload
	case tierCombined:
		return FallbackCacheOffload
	default:
		return FallbackNone
	}
}

func Solve(req Request) (Result, error) {
	s, err := newSolver(req)
	if err != nil {
		return Result{}, err
	}
	return s.solve()
}
