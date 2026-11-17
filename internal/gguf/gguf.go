// Package gguf reads just enough of the GGUF metadata to estimate memory.
//
// It is deliberately not a tensor loader: it walks the key/value block and
// keeps architecture, layer counts and attention dimensions.
package gguf

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const magic = 0x46554747 // "GGUF" little-endian

// Meta is the subset of GGUF metadata needed for a memory estimate.
type Meta struct {
	Architecture string `json:"architecture"`
	BlockCount   int    `json:"block_count"`
	Embedding    int    `json:"embedding"`
	HeadCount    int    `json:"head_count"`
	HeadCountKV  []int  `json:"head_count_kv,omitempty"` // per layer; empty means use HeadCount
	KeyLength    int    `json:"key_length"`
	ValueLength  int    `json:"value_length"`
	CtxTrain     int    `json:"ctx_train"`
	// FullAttentionInterval is set by hybrid architectures (qwen35 and
	// friends): only every Nth layer keeps a KV cache, the rest are
	// recurrent and use constant-size state.
	FullAttentionInterval int `json:"full_attention_interval,omitempty"`
	// ExpertCount is the number of routed experts per layer as declared by
	// the architecture. Zero means the model is not a mixture of experts.
	ExpertCount int `json:"expert_count,omitempty"`

	// SlidingWindow is the window size for sliding-window attention layers.
	// Zero means the model does not use sliding-window attention.
	SlidingWindow int `json:"sliding_window,omitempty"`
	// SlidingWindowPattern is the per-layer SWA schedule from
	// attention.sliding_window_pattern. A non-zero value marks an SWA layer.
	SlidingWindowPattern []int `json:"sliding_window_pattern,omitempty"`
	// SlidingWindowPatternN is the scalar form of the pattern: every Nth
	// layer is dense, the rest are SWA. SlidingWindowPatternSet distinguishes
	// a present zero (all SWA) from an absent pattern.
	SlidingWindowPatternN   int  `json:"sliding_window_pattern_n,omitempty"`
	SlidingWindowPatternSet bool `json:"sliding_window_pattern_set,omitempty"`
	// KeyLengthSWA and ValueLengthSWA are per-head widths for SWA layers.
	// Zero means they match the full-attention lengths.
	KeyLengthSWA   int `json:"key_length_swa,omitempty"`
	ValueLengthSWA int `json:"value_length_swa,omitempty"`
	// SharedKVLayers is the number of trailing layers that reuse an earlier
	// layer's KV cache instead of allocating their own (Gemma 4 and friends).
	SharedKVLayers int `json:"shared_kv_layers,omitempty"`

	// Tensors is the parsed tensor-info table. It is intentionally omitted
	// from JSON: the table can be large and the estimate already exposes the
	// aggregated byte terms.
	Tensors []TensorInfo `json:"-"`
	// UnmodeledTensors counts tensors whose type or shape wool cannot size.
	UnmodeledTensors int `json:"unmodeled_tensors,omitempty"`
	// Approximate reports that some metadata or tensor data was not modelled.
	Approximate        bool     `json:"approximate,omitempty"`
	ApproximateReasons []string `json:"approximate_reasons,omitempty"`
}

// TensorInfo describes one GGUF tensor without reading its data.
type TensorInfo struct {
	Name     string   `json:"name"`
	Dims     []uint64 `json:"dims"`
	Type     uint32   `json:"type"`
	TypeName string   `json:"type_name"`
	Offset   uint64   `json:"offset"`
	Bytes    uint64   `json:"bytes"`
}

func ReadMeta(path string) (Meta, error) {
	f, err := os.Open(path)
	if err != nil {
		return Meta{}, err
	}
	defer f.Close()
	return readMeta(f)
}

func readMeta(rd io.Reader) (Meta, error) {
	r := &reader{r: bufio.NewReaderSize(rd, 64<<10)}
	m, err := r.u32()
	if err != nil {
		return Meta{}, err
	}
	if m != magic {
		return Meta{}, errors.New("not a GGUF file")
	}
	version, err := r.u32()
	if err != nil {
		return Meta{}, err
	}
	if version < 2 || version > 3 {
		return Meta{}, fmt.Errorf("unsupported GGUF version %d", version)
	}
	tensorCount, err := r.u64()
	if err != nil {
		return Meta{}, err
	}
	if tensorCount > 1<<20 {
		return Meta{}, fmt.Errorf("unreasonable tensor count %d", tensorCount)
	}
	kvCount, err := r.u64()
	if err != nil {
		return Meta{}, err
	}
	if kvCount > 1<<20 {
		return Meta{}, fmt.Errorf("unreasonable metadata count %d", kvCount)
	}

	raw := map[string]value{}
	for i := uint64(0); i < kvCount; i++ {
		key, err := r.str()
		if err != nil {
			return Meta{}, err
		}
		t, err := r.u32()
		if err != nil {
			return Meta{}, err
		}
		if wantedKey(key) {
			v, err := r.readValue(t)
			if err != nil {
				return Meta{}, fmt.Errorf("%s: %w", key, err)
			}
			raw[key] = v
		} else if err := r.skipValue(t); err != nil {
			return Meta{}, fmt.Errorf("%s: %w", key, err)
		}
	}

	tensors, unmodeled, reasons, err := readTensors(r, tensorCount)
	if err != nil {
		return Meta{}, err
	}

	arch := raw["general.architecture"].s
	if arch == "" {
		return Meta{}, errors.New("missing general.architecture")
	}
	get := func(suffix string) (value, bool) {
		v, ok := raw[arch+"."+suffix]
		return v, ok
	}
	meta := Meta{
		Architecture:          arch,
		BlockCount:            asInt(getOr(get, "block_count")),
		Embedding:             asInt(getOr(get, "embedding_length")),
		HeadCount:             asInt(getOr(get, "attention.head_count")),
		KeyLength:             asInt(getOr(get, "attention.key_length")),
		ValueLength:           asInt(getOr(get, "attention.value_length")),
		CtxTrain:              asInt(getOr(get, "context_length")),
		FullAttentionInterval: asInt(getOr(get, "full_attention_interval")),
		ExpertCount:           asInt(getOr(get, "expert_count")),
		SlidingWindow:         asInt(getOr(get, "attention.sliding_window")),
		KeyLengthSWA:          asInt(getOr(get, "attention.key_length_swa")),
		ValueLengthSWA:        asInt(getOr(get, "attention.value_length_swa")),
		SharedKVLayers:        asInt(getOr(get, "attention.shared_kv_layers")),
		Tensors:               tensors,
		UnmodeledTensors:      unmodeled,
		Approximate:           unmodeled > 0,
		ApproximateReasons:    reasons,
	}
	if v, ok := get("attention.sliding_window_pattern"); ok {
		switch v.kind {
		case kindUintArray:
			for _, n := range v.arr {
				meta.SlidingWindowPattern = append(meta.SlidingWindowPattern, int(n))
			}
		case kindUint:
			meta.SlidingWindowPatternN = int(v.u)
			meta.SlidingWindowPatternSet = true
		}
	}
	if meta.SlidingWindow == 0 && arch == "gemma2" {
		// llama.cpp defaults Gemma 2's window to 4096 when the key is absent.
		meta.SlidingWindow = 4096
	}
	if meta.SlidingWindow > 0 && len(meta.SlidingWindowPattern) == 0 && !meta.SlidingWindowPatternSet {
		// A few architectures have a fixed default schedule in llama.cpp.
		switch arch {
		case "gemma2":
			meta.SlidingWindowPatternN, meta.SlidingWindowPatternSet = 2, true
		case "gemma3", "gemma-embedding", "gemma-embedding2":
			meta.SlidingWindowPatternN, meta.SlidingWindowPatternSet = 6, true
		case "gemma3n":
			meta.SlidingWindowPatternN, meta.SlidingWindowPatternSet = 5, true
		}
	}
	if arch == "gemma3n" && meta.SharedKVLayers == 0 && meta.BlockCount > 20 {
		// llama.cpp starts Gemma 3n's shared KV at layer 20.
		meta.SharedKVLayers = meta.BlockCount - 20
	}
	if meta.SlidingWindow > 0 && len(meta.SlidingWindowPattern) == 0 && !meta.SlidingWindowPatternSet {
		meta.Approximate = true
		meta.ApproximateReasons = appendUnique(meta.ApproximateReasons,
			"sliding-window layer pattern is not modelled")
	}
	if meta.SharedKVLayers < 0 {
		meta.SharedKVLayers = 0
	}
	if meta.SharedKVLayers >= meta.BlockCount {
		meta.Approximate = true
		meta.ApproximateReasons = appendUnique(meta.ApproximateReasons,
			"shared KV layer count is out of range")
		meta.SharedKVLayers = meta.BlockCount - 1
	}
	if v, ok := get("attention.head_count_kv"); ok {
		if v.kind == kindUintArray {
			for _, n := range v.arr {
				meta.HeadCountKV = append(meta.HeadCountKV, int(n))
			}
		} else if v.kind == kindUint {
			meta.HeadCountKV = []int{int(v.u)}
		}
	}
	if meta.BlockCount <= 0 || meta.HeadCount <= 0 || meta.Embedding <= 0 {
		return Meta{}, fmt.Errorf("%s: incomplete attention metadata", arch)
	}
	return meta, nil
}

// readTensors parses the tensor-info table. It never reads tensor data, only
// the fixed-size descriptors that follow the key/value block.
func readTensors(r *reader, count uint64) ([]TensorInfo, int, []string, error) {
	tensors := make([]TensorInfo, 0, min(int(count), 4096))
	var unmodeled int
	var reasons []string
	seen := map[string]bool{}
	for i := uint64(0); i < count; i++ {
		name, err := r.str()
		if err != nil {
			return nil, 0, nil, fmt.Errorf("tensor %d: %w", i, err)
		}
		nDims, err := r.u32()
		if err != nil {
			return nil, 0, nil, fmt.Errorf("tensor %s: %w", name, err)
		}
		if nDims == 0 || nDims > 8 {
			return nil, 0, nil, fmt.Errorf("tensor %s: unreasonable dimension count %d", name, nDims)
		}
		dims := make([]uint64, nDims)
		for j := range dims {
			if dims[j], err = r.u64(); err != nil {
				return nil, 0, nil, fmt.Errorf("tensor %s: %w", name, err)
			}
		}
		typ, err := r.u32()
		if err != nil {
			return nil, 0, nil, fmt.Errorf("tensor %s: %w", name, err)
		}
		offset, err := r.u64()
		if err != nil {
			return nil, 0, nil, fmt.Errorf("tensor %s: %w", name, err)
		}
		bytes, ok := TensorBytes(typ, dims)
		if !ok {
			unmodeled++
			typeName := TypeName(typ)
			if !seen[typeName] {
				seen[typeName] = true
				reasons = append(reasons, "unsupported tensor type "+typeName)
			}
		}
		tensors = append(tensors, TensorInfo{
			Name:     name,
			Dims:     dims,
			Type:     typ,
			TypeName: TypeName(typ),
			Offset:   offset,
			Bytes:    bytes,
		})
	}
	return tensors, unmodeled, reasons, nil
}

func getOr(get func(string) (value, bool), suffix string) value {
	v, _ := get(suffix)
	return v
}

func wantedKey(key string) bool {
	if key == "general.architecture" {
		return true
	}
	switch {
	case strings.HasSuffix(key, ".block_count"),
		strings.HasSuffix(key, ".embedding_length"),
		strings.HasSuffix(key, ".attention.head_count"),
		strings.HasSuffix(key, ".attention.head_count_kv"),
		strings.HasSuffix(key, ".attention.key_length"),
		strings.HasSuffix(key, ".attention.value_length"),
		strings.HasSuffix(key, ".attention.key_length_swa"),
		strings.HasSuffix(key, ".attention.value_length_swa"),
		strings.HasSuffix(key, ".attention.sliding_window"),
		strings.HasSuffix(key, ".attention.sliding_window_pattern"),
		strings.HasSuffix(key, ".attention.shared_kv_layers"),
		strings.HasSuffix(key, ".context_length"),
		strings.HasSuffix(key, ".full_attention_interval"),
		strings.HasSuffix(key, ".expert_count"):
		return true
	}
	return false
}

func asInt(v value) int {
	if v.kind != kindUint {
		return 0
	}
	return int(v.u)
}
