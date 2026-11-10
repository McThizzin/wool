// Package measure records observed VRAM next to the estimate.
//
// Records are append-only JSONL: one line per successful launch. A corrupt
// line is skipped rather than failing the whole store, because the file is
// written by more than one wool process over time.
package measure

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"wool/internal/config"
	"wool/internal/gguf"
	"wool/internal/sysinfo"
)

// Version is the on-disk record schema. Bump it only for a breaking change.
const Version = 1

// DefaultMaxRecords bounds the store. After an append pushes the file over the
// cap, the oldest records are dropped and the newest DefaultMaxRecords are
// rewritten atomically. Rotation is best-effort; the just-appended record is
// never lost.
const DefaultMaxRecords = 5000

// maxRecords is a variable so tests can exercise rotation with a small cap.
var maxRecords = DefaultMaxRecords

// Record kinds. An empty kind on disk is an observation from before benchmarks
// were added.
const (
	KindObservation = "observation"
	KindBench       = "bench"
)

// Record is one launch observation or benchmark run.
type Record struct {
	Version    int       `json:"version"`
	At         time.Time `json:"at"`
	Kind       string    `json:"kind,omitempty"`
	Instance   string    `json:"instance,omitempty"`
	Target     string    `json:"target,omitempty"`
	TargetKind string    `json:"target_kind,omitempty"`

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

	ConfigHash string `json:"config_hash,omitempty"`
	Bench      *Bench `json:"bench,omitempty"`
}

// Bench is the engine-independent result of a benchmark. Record fields still
// carry the model identity, hardware, estimate and observed VRAM so a
// benchmark can be compared with a launch observation.
type Bench struct {
	Engine string `json:"engine"`
	// Attached is true when the benchmark used an already-running server
	// instead of starting an ephemeral one.
	Attached bool   `json:"attached"`
	URL      string `json:"url,omitempty"`

	Runs             int  `json:"runs"`
	Warmup           bool `json:"warmup"`
	PromptTokens     int  `json:"prompt_tokens"`
	GenerationTokens int  `json:"generation_tokens"`
	Seed             int  `json:"seed"`

	LoadMS    float64 `json:"load_ms,omitempty"`
	LoadKnown bool    `json:"load_known"`

	BaselineBytes  uint64 `json:"baseline_bytes,omitempty"`
	PeakBytes      uint64 `json:"peak_bytes,omitempty"`
	VRAMDeltaBytes uint64 `json:"vram_delta_bytes,omitempty"`
	VRAMKnown      bool   `json:"vram_known"`
	OwnBytes       uint64 `json:"own_bytes,omitempty"`
	OwnKnown       bool   `json:"own_known"`

	Samples []BenchRun `json:"samples"`
	Median  BenchStats `json:"median"`
	Min     BenchStats `json:"min"`
	Max     BenchStats `json:"max"`
}

// BenchRun is one repetition.
type BenchRun struct {
	Index            int     `json:"index"`
	PromptTokens     int     `json:"prompt_tokens"`
	GenerationTokens int     `json:"generation_tokens"`
	PromptTPS        float64 `json:"prompt_tps"`
	GenerationTPS    float64 `json:"generation_tps"`
	PromptMS         float64 `json:"prompt_ms,omitempty"`
	GenerationMS     float64 `json:"generation_ms,omitempty"`
	TotalMS          float64 `json:"total_ms"`
	Source           string  `json:"source,omitempty"` // timings, metrics, wall or llama-bench
}

// BenchStats is one summary of a throughput series.
type BenchStats struct {
	PromptTPS     float64 `json:"prompt_tps"`
	GenerationTPS float64 `json:"generation_tps"`
	TotalMS       float64 `json:"total_ms,omitempty"`
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
	if r.Kind == "" {
		r.Kind = KindObservation
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
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	f, err := os.OpenFile(s.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// The record is already durable. A failed compaction leaves a larger file
	// but must not turn a successful append into an error.
	_ = s.compactLocked()
	return nil
}

// lock serializes writers with a sibling lock file. Appends are already atomic
// at the line level, but compaction rewrites the file and must not race an
// append from another wool process.
func (s *Store) lock() (func(), error) {
	f, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// compactLocked rewrites the store when it holds more than maxRecords. The
// caller must hold the store lock.
func (s *Store) compactLocked() error {
	if maxRecords <= 0 {
		return nil
	}
	records, err := s.All()
	if err != nil {
		return err
	}
	if len(records) <= maxRecords {
		return nil
	}
	records = records[len(records)-maxRecords:]

	var buf bytes.Buffer
	for _, r := range records {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".runs-*.jsonl")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, s.Path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
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
		if r.Kind == "" {
			r.Kind = KindObservation
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	return out, nil
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
