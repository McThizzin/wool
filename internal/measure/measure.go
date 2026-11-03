// Package measure records observed VRAM next to the estimate.
//
// Records are append-only JSONL: one line per successful launch. A corrupt
// line is skipped rather than failing the whole store, because the file is
// written by more than one wool process over time.
package measure

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"wool/internal/config"
	"wool/internal/gguf"
	"wool/internal/sysinfo"
)

// Version is the on-disk record schema. Bump it only for a breaking change.
const Version = 1

// Record is one launch observation.
type Record struct {
	Version  int       `json:"version"`
	At       time.Time `json:"at"`
	Instance string    `json:"instance,omitempty"`

	ModelID      string    `json:"model_id"`
	ModelPath    string    `json:"model_path,omitempty"`
	ModelSize    uint64    `json:"model_size_bytes,omitempty"`
	ModelModTime time.Time `json:"model_mtime,omitempty"`
	Architecture string    `json:"architecture,omitempty"`

	Ctx        int    `json:"ctx"`
	CacheTypeK string `json:"cache_type_k,omitempty"`
	CacheTypeV string `json:"cache_type_v,omitempty"`
	GPULayers  int    `json:"gpu_layers"`
	NCPUMoE    int    `json:"n_cpu_moe,omitempty"`

	GPUName   string `json:"gpu_name,omitempty"`
	GPUPCIID  string `json:"gpu_pci_id,omitempty"`
	GPUDriver string `json:"gpu_driver,omitempty"`

	LlamaServerVersion string `json:"llama_server_version,omitempty"`

	EstimateBytes uint64 `json:"estimate_bytes"`
	EstimateKnown bool   `json:"estimate_known"`
	BudgetBytes   uint64 `json:"budget_bytes,omitempty"`

	BaselineBytes uint64 `json:"baseline_bytes,omitempty"`
	BaselineKnown bool   `json:"baseline_known"`
	ObservedBytes uint64 `json:"observed_bytes"`

	Samples     []uint64 `json:"samples_bytes,omitempty"`
	SampleCount int      `json:"sample_count"`
}

// Shape identifies a launch configuration that can be compared with another.
// Observations from a different context, cache type or offload shape are not
// interchangeable.
type Shape struct {
	ModelID    string
	Ctx        int
	CacheTypeK string
	CacheTypeV string
	GPULayers  int
	NCPUMoE    int
}

// Shape returns the comparison key for a record.
func (r Record) Shape() Shape {
	return Shape{
		ModelID:    r.ModelID,
		Ctx:        r.Ctx,
		CacheTypeK: r.CacheTypeK,
		CacheTypeV: r.CacheTypeV,
		GPULayers:  r.GPULayers,
		NCPUMoE:    r.NCPUMoE,
	}
}

// Summary is the most recent observation for one shape.
type Summary struct {
	At            time.Time `json:"at"`
	EstimateBytes uint64    `json:"estimate_bytes"`
	ObservedBytes uint64    `json:"observed_bytes"`
	BaselineKnown bool      `json:"baseline_known"`
	Count         int       `json:"count"`
	SampleCount   int       `json:"sample_count"`
}

// Store is an append-only JSONL file.
type Store struct {
	Path string
}

// NewStore returns the store under the XDG state directory.
func NewStore() *Store {
	return &Store{Path: config.MeasurementsPath()}
}

// NewStoreAt is for tests and unusual layouts.
func NewStoreAt(path string) *Store {
	return &Store{Path: path}
}

// Append writes one record as a single line.
func (s *Store) Append(r Record) error {
	if s == nil || s.Path == "" {
		return errors.New("no measurement store")
	}
	if r.Version == 0 {
		r.Version = Version
	}
	if r.At.IsZero() {
		r.At = time.Now()
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// All returns every readable record in file order. Empty and corrupt lines are
// skipped. A missing file is not an error.
func (s *Store) All() ([]Record, error) {
	if s == nil || s.Path == "" {
		return nil, nil
	}
	f, err := os.Open(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		if r.Version != Version || r.ModelID == "" {
			continue
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// Summary returns the newest record matching shape and how many records match.
func (s *Store) Summary(shape Shape) (Summary, bool) {
	records, err := s.All()
	if err != nil {
		return Summary{}, false
	}
	var latest Record
	found := false
	count := 0
	for _, r := range records {
		if r.Shape() != shape {
			continue
		}
		count++
		if !found || r.At.After(latest.At) {
			latest, found = r, true
		}
	}
	if !found {
		return Summary{}, false
	}
	return Summary{
		At:            latest.At,
		EstimateBytes: latest.EstimateBytes,
		ObservedBytes: latest.ObservedBytes,
		BaselineKnown: latest.BaselineKnown,
		Count:         count,
		SampleCount:   latest.SampleCount,
	}, true
}

// RunInfo is everything needed to turn a launch into a record.
type RunInfo struct {
	Instance string

	ModelID     string
	ModelPath   string
	FileSize    uint64
	FileModTime time.Time
	Meta        gguf.Meta
	MetaOK      bool

	Ctx        int
	CacheTypeK string
	CacheTypeV string
	GPULayers  int
	NCPUMoE    int

	GPU          sysinfo.GPU
	ReserveBytes uint64
	OwnUsed      uint64
	OwnKnown     bool

	LlamaServerVersion string
}

// NewRecord computes the estimate and budget for a launch.
func NewRecord(i RunInfo) Record {
	r := Record{
		Version:            Version,
		Instance:           i.Instance,
		ModelID:            i.ModelID,
		ModelPath:          i.ModelPath,
		ModelSize:          i.FileSize,
		ModelModTime:       i.FileModTime,
		Ctx:                i.Ctx,
		CacheTypeK:         i.CacheTypeK,
		CacheTypeV:         i.CacheTypeV,
		GPULayers:          i.GPULayers,
		NCPUMoE:            i.NCPUMoE,
		GPUName:            i.GPU.Name,
		GPUPCIID:           i.GPU.PCIID,
		GPUDriver:          i.GPU.Driver,
		LlamaServerVersion: i.LlamaServerVersion,
	}
	if i.MetaOK {
		r.Architecture = i.Meta.Architecture
		est := gguf.EstimateVRAM(i.Meta, i.FileSize, i.Ctx, i.GPULayers, i.NCPUMoE, i.CacheTypeK, i.CacheTypeV)
		r.EstimateBytes = est.Total
		r.EstimateKnown = est.Total > 0
	}
	if b := gguf.ComputeBudget(i.GPU.VRAMTotal, i.GPU.VRAMUsed, i.ReserveBytes, i.OwnUsed, i.OwnKnown); b.Known {
		r.BudgetBytes = b.Available
	}
	return r
}

// SampleVRAM returns the primary GPU's used VRAM. It is best-effort.
func SampleVRAM() (uint64, bool) {
	snap, err := sysinfo.NewCollector().Sample()
	if err != nil {
		return 0, false
	}
	gpu, ok := snap.Primary()
	if !ok || !gpu.HasVRAM {
		return 0, false
	}
	return gpu.VRAMUsed, true
}
