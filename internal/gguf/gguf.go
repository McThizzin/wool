// Package gguf reads just enough of the GGUF metadata to estimate memory.
//
// It is deliberately not a tensor loader: it walks the key/value block and
// keeps architecture, layer counts and attention dimensions.
package gguf

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
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

type ggmlType struct {
	name  string
	block uint64 // elements per quantisation block
	size  uint64 // bytes per block
}

// ggmlTypes follows the ggml_type enum. Removed types are deliberately absent:
// a file that uses one is reported as approximate rather than guessed at.
var ggmlTypes = map[uint32]ggmlType{
	0:  {"f32", 1, 4},
	1:  {"f16", 1, 2},
	2:  {"q4_0", 32, 18},
	3:  {"q4_1", 32, 20},
	6:  {"q5_0", 32, 22},
	7:  {"q5_1", 32, 24},
	8:  {"q8_0", 32, 34},
	9:  {"q8_1", 32, 36},
	10: {"q2_K", 256, 84},
	11: {"q3_K", 256, 110},
	12: {"q4_K", 256, 144},
	13: {"q5_K", 256, 176},
	14: {"q6_K", 256, 210},
	15: {"q8_K", 256, 292},
	16: {"iq2_xxs", 256, 66},
	17: {"iq2_xs", 256, 74},
	18: {"iq3_xxs", 256, 98},
	19: {"iq1_s", 256, 50},
	20: {"iq4_nl", 32, 18},
	21: {"iq3_s", 256, 110},
	22: {"iq2_s", 256, 82},
	23: {"iq4_xs", 256, 136},
	24: {"i8", 1, 1},
	25: {"i16", 1, 2},
	26: {"i32", 1, 4},
	27: {"i64", 1, 8},
	28: {"f64", 1, 8},
	29: {"iq1_m", 256, 56},
	30: {"bf16", 1, 2},
	34: {"tq1_0", 256, 54},
	35: {"tq2_0", 256, 66},
	39: {"mxfp4", 32, 17},
	40: {"nvfp4", 64, 36},
	41: {"q1_0", 128, 18},
	42: {"q2_0", 64, 18},
	43: {"f8_e4m3", 1, 1},
}

// TypeName returns the ggml type name, or "unknown" for an unsupported type.
func TypeName(t uint32) string {
	if info, ok := ggmlTypes[t]; ok {
		return info.name
	}
	return "unknown"
}

// TensorBytes returns the stored size of a tensor with the given dimensions
// and ggml type. Rows are quantised independently, so the row width is rounded
// up to a whole number of blocks before multiplying by the row count.
func TensorBytes(t uint32, dims []uint64) (uint64, bool) {
	info, ok := ggmlTypes[t]
	if !ok || info.block == 0 {
		return 0, false
	}
	if len(dims) == 0 {
		return 0, true
	}
	const max = ^uint64(0)
	rows := uint64(1)
	for _, d := range dims[1:] {
		if d != 0 && rows > max/d {
			return 0, false
		}
		rows *= d
	}
	row := dims[0]
	if row != 0 && info.block-1 > max-row {
		return 0, false
	}
	blocks := (row + info.block - 1) / info.block
	if blocks != 0 && rows > max/blocks {
		return 0, false
	}
	rows *= blocks
	if rows != 0 && info.size > max/rows {
		return 0, false
	}
	return rows * info.size, true
}

// blockLayer parses the transformer-block index from a tensor name such as
// blk.12.ffn_gate_exps.weight.
func blockLayer(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, "blk.")
	if !ok {
		return 0, false
	}
	dot := strings.IndexByte(rest, '.')
	if dot <= 0 {
		return 0, false
	}
	layer, err := strconv.Atoi(rest[:dot])
	if err != nil || layer < 0 {
		return 0, false
	}
	return layer, true
}

// isExpertTensor matches routed-expert tensors. llama.cpp spells them
// ffn_{gate,up,down}_exps, and fused variants keep the same _exps suffix.
// Shared-expert (_shexp) and router (ffn_gate_inp) tensors are not affected by
// n_cpu_moe, so they stay with the rest of the layer.
func isExpertTensor(name string) bool {
	layer, ok := blockLayer(name)
	if !ok || layer < 0 {
		return false
	}
	return strings.Contains(name, ".ffn_") && strings.Contains(name, "_exps.")
}

// TensorBytes returns the total size of the parsed tensor table.
func (m Meta) TensorBytes() uint64 {
	var total uint64
	for _, t := range m.Tensors {
		total += t.Bytes
	}
	return total
}

// ExpertBytes returns the total size of routed-expert tensors.
func (m Meta) ExpertBytes() uint64 {
	var total uint64
	for _, t := range m.Tensors {
		if isExpertTensor(t.Name) {
			total += t.Bytes
		}
	}
	return total
}

type valueKind int

const (
	kindUint valueKind = iota
	kindFloat
	kindString
	kindUintArray
)

type value struct {
	kind valueKind
	u    uint64
	f    float64
	s    string
	arr  []uint64
}

type reader struct {
	r *bufio.Reader
}

func (r *reader) u32() (uint32, error) {
	var b [4]byte
	if _, err := io.ReadFull(r.r, b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b[:]), nil
}

func (r *reader) u64() (uint64, error) {
	var b [8]byte
	if _, err := io.ReadFull(r.r, b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b[:]), nil
}

func (r *reader) str() (string, error) {
	n, err := r.u64()
	if err != nil {
		return "", err
	}
	if n > 1<<24 {
		return "", fmt.Errorf("metadata string too large (%d bytes)", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r.r, b); err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *reader) skip(n uint64) error {
	if n > 1<<34 {
		return fmt.Errorf("metadata field too large (%d bytes)", n)
	}
	_, err := io.CopyN(io.Discard, r.r, int64(n))
	return err
}

// scalar reads a value that is not an array, returning its numeric or string
// representation. Types follow the GGUF metadata enum.
func (r *reader) scalar(t uint32) (value, error) {
	switch t {
	case 0: // uint8
		b := make([]byte, 1)
		if _, err := io.ReadFull(r.r, b); err != nil {
			return value{}, err
		}
		return value{kind: kindUint, u: uint64(b[0])}, nil
	case 1: // int8
		b := make([]byte, 1)
		if _, err := io.ReadFull(r.r, b); err != nil {
			return value{}, err
		}
		return value{kind: kindUint, u: uint64(int64(int8(b[0])))}, nil
	case 2: // uint16
		b := make([]byte, 2)
		if _, err := io.ReadFull(r.r, b); err != nil {
			return value{}, err
		}
		return value{kind: kindUint, u: uint64(binary.LittleEndian.Uint16(b))}, nil
	case 3: // int16
		b := make([]byte, 2)
		if _, err := io.ReadFull(r.r, b); err != nil {
			return value{}, err
		}
		return value{kind: kindUint, u: uint64(int64(int16(binary.LittleEndian.Uint16(b))))}, nil
	case 4: // uint32
		v, err := r.u32()
		return value{kind: kindUint, u: uint64(v)}, err
	case 5: // int32
		v, err := r.u32()
		return value{kind: kindUint, u: uint64(int64(int32(v)))}, err
	case 6: // float32
		v, err := r.u32()
		return value{kind: kindFloat, f: float64(math.Float32frombits(v))}, err
	case 7: // bool
		b := make([]byte, 1)
		if _, err := io.ReadFull(r.r, b); err != nil {
			return value{}, err
		}
		var n uint64
		if b[0] != 0 {
			n = 1
		}
		return value{kind: kindUint, u: n}, nil
	case 8: // string
		s, err := r.str()
		return value{kind: kindString, s: s}, err
	case 10: // uint64
		v, err := r.u64()
		return value{kind: kindUint, u: v}, err
	case 11: // int64
		v, err := r.u64()
		return value{kind: kindUint, u: v}, err
	case 12: // float64
		v, err := r.u64()
		return value{kind: kindFloat, f: math.Float64frombits(v)}, err
	default:
		return value{}, fmt.Errorf("unsupported metadata type %d", t)
	}
}

func (r *reader) skipValue(t uint32) error {
	switch t {
	case 0, 1, 7:
		return r.skip(1)
	case 2, 3:
		return r.skip(2)
	case 4, 5, 6:
		return r.skip(4)
	case 8:
		_, err := r.str()
		return err
	case 10, 11, 12:
		return r.skip(8)
	case 9: // array
		et, err := r.u32()
		if err != nil {
			return err
		}
		count, err := r.u64()
		if err != nil {
			return err
		}
		if count > 1<<28 {
			return fmt.Errorf("metadata array too large (%d elements)", count)
		}
		switch et {
		case 0, 1, 7:
			return r.skip(count)
		case 2, 3:
			return r.skip(count * 2)
		case 4, 5, 6:
			return r.skip(count * 4)
		case 10, 11, 12:
			return r.skip(count * 8)
		case 8:
			for i := uint64(0); i < count; i++ {
				if _, err := r.str(); err != nil {
					return err
				}
			}
			return nil
		default:
			return fmt.Errorf("unsupported array element type %d", et)
		}
	default:
		return fmt.Errorf("unsupported metadata type %d", t)
	}
}

func (r *reader) readValue(t uint32) (value, error) {
	if t != 9 {
		return r.scalar(t)
	}
	et, err := r.u32()
	if err != nil {
		return value{}, err
	}
	count, err := r.u64()
	if err != nil {
		return value{}, err
	}
	if count > 1<<28 {
		return value{}, fmt.Errorf("metadata array too large (%d elements)", count)
	}
	if et > 12 {
		return value{}, fmt.Errorf("unsupported array element type %d", et)
	}
	arr := make([]uint64, 0, min(int(count), 4096))
	for i := uint64(0); i < count; i++ {
		v, err := r.scalar(et)
		if err != nil {
			return value{}, err
		}
		if v.kind == kindUint {
			arr = append(arr, v.u)
		}
	}
	return value{kind: kindUintArray, arr: arr}, nil
}

// ReadMeta parses the metadata block of a GGUF file.
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
		Tensors:               tensors,
		UnmodeledTensors:      unmodeled,
		Approximate:           unmodeled > 0,
		ApproximateReasons:    reasons,
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// HeadDim returns the per-head width used for the KV cache.
func (m Meta) HeadDim() int {
	if m.HeadCount > 0 && m.Embedding > 0 {
		return m.Embedding / m.HeadCount
	}
	return 0
}

// HeadsAt returns KV heads for a layer index, handling per-layer arrays.
func (m Meta) HeadsAt(layer int) int {
	if len(m.HeadCountKV) == 0 {
		return m.HeadCount
	}
	if layer < len(m.HeadCountKV) {
		return m.HeadCountKV[layer]
	}
	return m.HeadCountKV[len(m.HeadCountKV)-1]
}

// FullAttention reports whether a layer keeps a KV cache. Hybrid models such
// as qwen35 (Qwen3.5/Qwen3-Next) only use full attention every Nth layer; the
// remaining layers are recurrent with constant-size state. The schedule is
// 1-indexed, so with interval 4 layers 3, 7, 11, ... are full attention.
func (m Meta) FullAttention(layer int) bool {
	if m.FullAttentionInterval <= 1 {
		return true
	}
	return (layer+1)%m.FullAttentionInterval == 0
}

// KVBytes estimates the KV cache for ctx tokens with the given cache types.
func (m Meta) KVBytes(ctx int, cacheK, cacheV string) uint64 {
	if ctx <= 0 || m.BlockCount <= 0 {
		return 0
	}
	kd, vd := m.KeyLength, m.ValueLength
	if kd <= 0 || vd <= 0 {
		d := m.HeadDim()
		if kd <= 0 {
			kd = d
		}
		if vd <= 0 {
			vd = d
		}
		if kd <= 0 || vd <= 0 {
			return 0
		}
	}
	bk, bv := BytesPerElement(cacheK), BytesPerElement(cacheV)
	var total float64
	for i := 0; i < m.BlockCount; i++ {
		if !m.FullAttention(i) {
			continue
		}
		h := float64(m.HeadsAt(i))
		total += float64(ctx) * h * (float64(kd)*bk + float64(vd)*bv)
	}
	return uint64(total)
}

// BytesPerElement returns the storage cost of one cache element for a
// llama.cpp cache type string.
func BytesPerElement(t string) float64 {
	switch strings.ToLower(t) {
	case "f32":
		return 4
	case "f16", "bf16":
		return 2
	case "q8_0":
		return 34.0 / 32.0
	case "q5_1":
		return 24.0 / 32.0
	case "q5_0":
		return 22.0 / 32.0
	case "q4_1":
		return 20.0 / 32.0
	case "q4_0", "iq4_nl":
		return 18.0 / 32.0
	default:
		return 2
	}
}

// Estimate is a conservative VRAM estimate. GPUWeights and KVBytes are
// charged to VRAM; CPUWeights is the part of the model that stays in system
// memory (unoffloaded layers and expert tensors moved by n_cpu_moe). Total is
// the VRAM term, so CPUWeights is deliberately not part of it.
type Estimate struct {
	FileBytes   uint64   `json:"file_bytes"`
	GPUWeights  uint64   `json:"gpu_weights_bytes"`
	CPUWeights  uint64   `json:"cpu_weights_bytes"`
	KVBytes     uint64   `json:"kv_bytes"`
	Overhead    uint64   `json:"overhead_bytes"`
	Total       uint64   `json:"total_bytes"`
	Approximate bool     `json:"approximate,omitempty"`
	Reasons     []string `json:"approximate_reasons,omitempty"`
}

// LimitingTerm names the largest VRAM component of the estimate. Ties prefer
// weights because they dominate most dense models.
func (e Estimate) LimitingTerm() string {
	switch {
	case e.GPUWeights >= e.KVBytes && e.GPUWeights >= e.Overhead:
		return "weights"
	case e.KVBytes >= e.Overhead:
		return "kv"
	default:
		return "overhead"
	}
}

// Budget is the VRAM available to a new server. Used is the GPU's current
// usage; OwnUsed is the part belonging to the running wool server and is
// excluded from OtherUsed so a restart is not charged twice.
type Budget struct {
	Total     uint64 `json:"total_bytes"`
	Used      uint64 `json:"used_bytes"`
	Reserve   uint64 `json:"reserve_bytes"`
	OwnUsed   uint64 `json:"own_used_bytes"`
	OtherUsed uint64 `json:"other_used_bytes"`
	Available uint64 `json:"available_bytes"`
	Known     bool   `json:"known"`
	OwnKnown  bool   `json:"own_known"`
}

// ComputeBudget derives the budget for a new server. own is the running
// wool server's VRAM; pass ownKnown=false when it cannot be measured, in
// which case all current usage counts as other usage.
func ComputeBudget(total, used, reserve, own uint64, ownKnown bool) Budget {
	b := Budget{
		Total:    total,
		Used:     used,
		Reserve:  reserve,
		OwnKnown: ownKnown,
	}
	if total == 0 {
		return b
	}
	b.Known = true
	if ownKnown {
		if own > used {
			own = used
		}
		b.OwnUsed = own
	}
	b.OtherUsed = used - b.OwnUsed
	if reserve >= total {
		return b
	}
	remaining := total - reserve
	if b.OtherUsed < remaining {
		b.Available = remaining - b.OtherUsed
	}
	return b
}

// Headroom is the signed difference between the budget and an estimate.
// Positive values fit; negative values exceed the budget. It is zero when the
// budget is unknown.
func (b Budget) Headroom(e Estimate) int64 {
	if !b.Known {
		return 0
	}
	return int64(b.Available) - int64(e.Total)
}

// EstimateVRAM estimates GPU memory for the given context. gpuLayers is the
// number of transformer layers offloaded; values >= BlockCount mean all and
// zero means no layers. nCPUMoE is llama.cpp's --n-cpu-moe: the routed-expert
// tensors of the first N layers stay on the CPU. Overhead covers compute
// buffers and allocator slack, not the KV cache.
func EstimateVRAM(m Meta, fileBytes uint64, ctx, gpuLayers, nCPUMoE int, cacheK, cacheV string) Estimate {
	if m.BlockCount > 0 {
		if gpuLayers > m.BlockCount {
			gpuLayers = m.BlockCount
		}
		if gpuLayers < 0 {
			gpuLayers = m.BlockCount
		}
	}
	if nCPUMoE < 0 {
		nCPUMoE = 0
	}
	if nCPUMoE > m.BlockCount {
		nCPUMoE = m.BlockCount
	}

	est := Estimate{
		FileBytes: fileBytes,
		KVBytes:   m.KVBytes(ctx, cacheK, cacheV),
		Overhead:  512 << 20,
	}

	gpu, cpu, experts, ok := m.placeWeights(fileBytes, gpuLayers, nCPUMoE)
	if ok {
		est.GPUWeights, est.CPUWeights = gpu, cpu
	} else {
		frac := 0.0
		if m.BlockCount > 0 {
			frac = float64(gpuLayers) / float64(m.BlockCount)
		}
		est.GPUWeights = uint64(float64(fileBytes) * frac)
		est.CPUWeights = fileBytes - est.GPUWeights
		est.Approximate = true
		switch {
		case len(m.Tensors) == 0:
			est.Reasons = append(est.Reasons, "tensor metadata unavailable; using file-size approximation")
		case m.UnmodeledTensors > 0:
			est.Reasons = append(est.Reasons, "some tensor types are not modelled; using file-size approximation")
		case m.ExpertCount > 0:
			est.Reasons = append(est.Reasons, "MoE detected but expert tensors not modeled")
		default:
			est.Reasons = append(est.Reasons, "tensor layout not modelled; using file-size approximation")
		}
	}

	if m.Approximate {
		est.Approximate = true
		est.Reasons = appendUnique(est.Reasons, m.ApproximateReasons...)
	}
	if nCPUMoE > 0 && experts == 0 {
		est.Approximate = true
		est.Reasons = appendUnique(est.Reasons, "n_cpu_moe is not modeled for this model")
	}
	est.Total = est.GPUWeights + est.KVBytes + est.Overhead
	return est
}

// placeWeights splits the model into GPU and CPU weight terms using the parsed
// tensor table. It reports false when the table cannot be trusted, in which
// case the caller falls back to the file-size fraction.
func (m Meta) placeWeights(fileBytes uint64, gpuLayers, nCPUMoE int) (gpu, cpu, experts uint64, ok bool) {
	if len(m.Tensors) == 0 || m.BlockCount <= 0 || m.UnmodeledTensors > 0 {
		return 0, 0, 0, false
	}
	block := make([]uint64, m.BlockCount)
	expert := make([]uint64, m.BlockCount)
	var nonBlock uint64
	var accounted uint64
	for _, t := range m.Tensors {
		accounted += t.Bytes
		if layer, isBlock := blockLayer(t.Name); isBlock && layer < m.BlockCount {
			block[layer] += t.Bytes
			if isExpertTensor(t.Name) {
				expert[layer] += t.Bytes
			}
		} else {
			nonBlock += t.Bytes
		}
	}
	for _, n := range expert {
		experts += n
	}
	// A file always contains at least its tensor data. A larger computed total
	// means the type table or dimensions are wrong, so do not trust it.
	if accounted == 0 || (fileBytes > 0 && accounted > fileBytes) {
		return 0, 0, 0, false
	}
	// An architecture that declares experts but has no routed-expert tensors
	// is outside the naming table. Fall back rather than silently ignoring
	// n_cpu_moe.
	if m.ExpertCount > 0 && experts == 0 {
		return 0, 0, 0, false
	}

	gpuStart := m.BlockCount - gpuLayers
	if gpuLayers > 0 {
		// Non-block tensors (embeddings, output, norms) are assumed to follow
		// the layer offload. M2.2 refines this per tensor.
		gpu += nonBlock
	}
	// Metadata and alignment padding are mapped from the file and stay in RAM.
	if fileBytes > accounted {
		cpu += fileBytes - accounted
	}
	for i := 0; i < m.BlockCount; i++ {
		bytes := block[i]
		if i < gpuStart {
			cpu += bytes
			continue
		}
		moved := uint64(0)
		if i < nCPUMoE {
			moved = expert[i]
			if moved > bytes {
				moved = bytes
			}
		}
		gpu += bytes - moved
		cpu += moved
	}
	return gpu, cpu, experts, true
}

func appendUnique(dst []string, values ...string) []string {
	for _, v := range values {
		found := false
		for _, existing := range dst {
			if existing == v {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, v)
		}
	}
	return dst
}
