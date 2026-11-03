package gguf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type testKV struct {
	key string
	typ uint32
	val any
}

type testTensor struct {
	name   string
	dims   []uint64
	typ    uint32
	offset uint64
}

func writeTestGGUF(t *testing.T, version uint32, kvs []testKV, tensors ...testTensor) string {
	t.Helper()
	var b bytes.Buffer
	w32 := func(v uint32) { binary.Write(&b, binary.LittleEndian, v) }
	w64 := func(v uint64) { binary.Write(&b, binary.LittleEndian, v) }
	wstr := func(s string) { w64(uint64(len(s))); b.WriteString(s) }

	w32(magic)
	w32(version)
	w64(uint64(len(tensors)))
	w64(uint64(len(kvs)))
	for _, kv := range kvs {
		wstr(kv.key)
		w32(kv.typ)
		switch kv.typ {
		case 8:
			wstr(kv.val.(string))
		case 4:
			w32(kv.val.(uint32))
		case 10:
			w64(kv.val.(uint64))
		case 6:
			binary.Write(&b, binary.LittleEndian, kv.val.(float32))
		case 9:
			arr := kv.val.([]uint32)
			w32(4)
			w64(uint64(len(arr)))
			for _, v := range arr {
				w32(v)
			}
		case 12:
			binary.Write(&b, binary.LittleEndian, kv.val.(float64))
		default:
			t.Fatalf("test writer: unsupported type %d", kv.typ)
		}
	}
	for _, tensor := range tensors {
		wstr(tensor.name)
		w32(uint32(len(tensor.dims)))
		for _, d := range tensor.dims {
			w64(d)
		}
		w32(tensor.typ)
		w64(tensor.offset)
	}
	path := filepath.Join(t.TempDir(), "test.gguf")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadMeta(t *testing.T) {
	path := writeTestGGUF(t, 3, []testKV{
		{"general.name", 8, "test"},
		{"general.architecture", 8, "gemma3"},
		{"gemma3.block_count", 4, uint32(2)},
		{"gemma3.embedding_length", 4, uint32(8)},
		{"gemma3.attention.head_count", 4, uint32(2)},
		{"gemma3.attention.head_count_kv", 9, []uint32{1, 1}},
		{"gemma3.attention.key_length", 4, uint32(4)},
		{"gemma3.attention.value_length", 4, uint32(4)},
		{"gemma3.context_length", 4, uint32(4096)},
		{"some.unknown", 12, float64(1.5)},
	})
	m, err := ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Architecture != "gemma3" || m.BlockCount != 2 || m.Embedding != 8 || m.HeadCount != 2 {
		t.Fatalf("meta = %+v", m)
	}
	if len(m.HeadCountKV) != 2 || m.HeadCountKV[1] != 1 {
		t.Fatalf("kv heads = %v", m.HeadCountKV)
	}
	if m.KeyLength != 4 || m.ValueLength != 4 || m.CtxTrain != 4096 {
		t.Fatalf("lengths = %+v", m)
	}
	if m.HeadsAt(5) != 1 {
		t.Fatalf("HeadsAt fallback = %d", m.HeadsAt(5))
	}
}

func TestReadMetaScalarKVHeads(t *testing.T) {
	path := writeTestGGUF(t, 2, []testKV{
		{"general.architecture", 8, "qwen3"},
		{"qwen3.block_count", 10, uint64(10)},
		{"qwen3.embedding_length", 10, uint64(128)},
		{"qwen3.attention.head_count", 10, uint64(8)},
		{"qwen3.attention.head_count_kv", 10, uint64(2)},
	})
	m, err := ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.HeadDim() != 16 || m.HeadsAt(9) != 2 {
		t.Fatalf("meta = %+v", m)
	}
}

func TestReadMetaFullAttentionInterval(t *testing.T) {
	path := writeTestGGUF(t, 3, []testKV{
		{"general.architecture", 8, "qwen35"},
		{"qwen35.block_count", 4, uint32(8)},
		{"qwen35.embedding_length", 4, uint32(128)},
		{"qwen35.attention.head_count", 4, uint32(8)},
		{"qwen35.attention.head_count_kv", 4, uint32(2)},
		{"qwen35.attention.key_length", 4, uint32(32)},
		{"qwen35.attention.value_length", 4, uint32(32)},
		{"qwen35.full_attention_interval", 4, uint32(4)},
	})
	m, err := ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.FullAttentionInterval != 4 {
		t.Fatalf("interval = %d", m.FullAttentionInterval)
	}
	for i := 0; i < 8; i++ {
		want := i == 3 || i == 7
		if got := m.FullAttention(i); got != want {
			t.Fatalf("FullAttention(%d) = %v, want %v", i, got, want)
		}
	}
	// Only the two full-attention layers contribute: 1024 tokens * 2 heads *
	// (32 + 32) dims * f16 * 2 layers.
	if got := m.KVBytes(1024, "f16", "f16"); got != 1024*2*64*2*2 {
		t.Fatalf("hybrid kv = %d", got)
	}
}

func TestReadMetaErrors(t *testing.T) {
	if _, err := readMeta(bytes.NewReader([]byte("not a gguf"))); err == nil {
		t.Fatal("expected magic error")
	}
	path := writeTestGGUF(t, 7, nil)
	if _, err := ReadMeta(path); err == nil {
		t.Fatal("expected version error")
	}
	path = writeTestGGUF(t, 3, []testKV{{"general.name", 8, "x"}})
	if _, err := ReadMeta(path); err == nil {
		t.Fatal("expected missing architecture error")
	}
	path = writeTestGGUF(t, 3, []testKV{
		{"general.architecture", 8, "x"},
		{"x.block_count", 4, uint32(1)},
	})
	if _, err := ReadMeta(path); err == nil {
		t.Fatal("expected incomplete attention error")
	}
}

func TestEstimateVRAM(t *testing.T) {
	m := Meta{
		Architecture: "x", BlockCount: 2, Embedding: 8, HeadCount: 2,
		HeadCountKV: []int{1, 1}, KeyLength: 4, ValueLength: 4,
	}
	const g = 1 << 30
	file := uint64(10 * g)
	e := EstimateVRAM(m, file, 1024, 1, 0, "q4_0", "q4_0")
	if e.GPUWeights != 5*g {
		t.Fatalf("weights = %d, want %d", e.GPUWeights, uint64(5*g))
	}
	if e.CPUWeights != 5*g {
		t.Fatalf("cpu weights = %d, want %d", e.CPUWeights, uint64(5*g))
	}
	// 1024 * (4*0.5625 + 4*0.5625) * 2 layers = 9216
	if e.KVBytes != 9216 {
		t.Fatalf("kv = %d", e.KVBytes)
	}
	if e.Overhead != 512<<20 {
		t.Fatalf("overhead = %d", e.Overhead)
	}
	if e.Total != e.GPUWeights+e.KVBytes+e.Overhead {
		t.Fatalf("total = %+v", e)
	}
	if !e.Approximate || len(e.Reasons) == 0 {
		t.Fatalf("metadata-free estimate should be flagged: %+v", e)
	}
	// gpu layers above the block count are clamped to all layers
	all := EstimateVRAM(m, file, 1024, 999, 0, "f16", "f16")
	if all.GPUWeights != file || all.CPUWeights != 0 {
		t.Fatalf("all weights = %d/%d", all.GPUWeights, all.CPUWeights)
	}
	if all.KVBytes != 1024*4*2*2*2 {
		t.Fatalf("f16 kv = %d", all.KVBytes)
	}
	// zero means no offloaded layers; negative means all
	zero := EstimateVRAM(m, file, 1024, 0, 0, "f16", "f16")
	if zero.GPUWeights != 0 || zero.CPUWeights != file {
		t.Fatalf("zero weights = %d/%d", zero.GPUWeights, zero.CPUWeights)
	}
	neg := EstimateVRAM(m, file, 1024, -1, 0, "f16", "f16")
	if neg.GPUWeights != file {
		t.Fatalf("negative weights = %d, want %d", neg.GPUWeights, file)
	}
}

func TestTensorBytes(t *testing.T) {
	cases := []struct {
		typ  uint32
		dims []uint64
		want uint64
		ok   bool
	}{
		{0, []uint64{3, 4}, 48, true},
		{1, []uint64{5}, 10, true},
		{2, []uint64{32}, 18, true},
		{2, []uint64{33}, 36, true}, // partial rows round up to a whole block
		{2, []uint64{32, 3}, 54, true},
		{12, []uint64{256, 2}, 288, true},
		{8, []uint64{0}, 0, true},
		{99, []uint64{1}, 0, false},
	}
	for _, tc := range cases {
		got, ok := TensorBytes(tc.typ, tc.dims)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("TensorBytes(%d, %v) = %d,%v want %d,%v", tc.typ, tc.dims, got, ok, tc.want, tc.ok)
		}
	}
	if TypeName(0) != "f32" || TypeName(12) != "q4_K" || TypeName(99) != "unknown" {
		t.Fatalf("type names = %q, %q, %q", TypeName(0), TypeName(12), TypeName(99))
	}
}

func TestIsExpertTensor(t *testing.T) {
	cases := map[string]bool{
		"blk.0.ffn_gate_exps.weight":  true,
		"blk.31.ffn_down_exps.weight": true,
		"blk.0.ffn_gate_inp.weight":   false,
		"blk.0.ffn_gate_shexp.weight": false,
		"blk.0.attn_q.weight":         false,
		"token_embd.weight":           false,
		"not-a-block.ffn_up_exps":     false,
	}
	for name, want := range cases {
		if got := isExpertTensor(name); got != want {
			t.Fatalf("isExpertTensor(%q) = %v, want %v", name, got, want)
		}
	}
}

// moeFixture builds a four-layer synthetic MoE model. Every layer has one dense
// attention tensor, a router, a shared expert and three routed-expert tensors.
func moeFixture(t *testing.T) ([]testKV, []testTensor) {
	t.Helper()
	kvs := []testKV{
		{"general.architecture", 8, "moe-test"},
		{"moe-test.block_count", 4, uint32(4)},
		{"moe-test.embedding_length", 4, uint32(8)},
		{"moe-test.attention.head_count", 4, uint32(2)},
		{"moe-test.attention.head_count_kv", 4, uint32(1)},
		{"moe-test.attention.key_length", 4, uint32(4)},
		{"moe-test.attention.value_length", 4, uint32(4)},
		{"moe-test.context_length", 4, uint32(4096)},
		{"moe-test.expert_count", 4, uint32(4)},
	}
	tensors := []testTensor{
		{name: "token_embd.weight", dims: []uint64{8, 4}, typ: 0},
		{name: "output_norm.weight", dims: []uint64{8}, typ: 0},
	}
	for l := 0; l < 4; l++ {
		p := fmt.Sprintf("blk.%d.", l)
		tensors = append(tensors,
			testTensor{name: p + "attn_q.weight", dims: []uint64{8, 8}, typ: 0},
			testTensor{name: p + "ffn_gate_inp.weight", dims: []uint64{8}, typ: 0},
			testTensor{name: p + "ffn_gate_exps.weight", dims: []uint64{8, 8}, typ: 0},
			testTensor{name: p + "ffn_up_exps.weight", dims: []uint64{8, 8}, typ: 0},
			testTensor{name: p + "ffn_down_exps.weight", dims: []uint64{8, 8}, typ: 0},
			testTensor{name: p + "ffn_gate_shexp.weight", dims: []uint64{8, 8}, typ: 0},
		)
	}
	return kvs, tensors
}

func TestReadMetaTensors(t *testing.T) {
	kvs, tensors := moeFixture(t)
	path := writeTestGGUF(t, 3, kvs, tensors...)
	m, err := ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.ExpertCount != 4 {
		t.Fatalf("expert count = %d", m.ExpertCount)
	}
	if len(m.Tensors) != len(tensors) {
		t.Fatalf("tensor count = %d, want %d", len(m.Tensors), len(tensors))
	}
	// 2 non-block f32 tensors (128 + 32) and 4 layers of 1312 bytes.
	const tensorBytes = 5408
	const expertBytes = 4 * 768
	if got := m.TensorBytes(); got != tensorBytes {
		t.Fatalf("tensor bytes = %d, want %d", got, tensorBytes)
	}
	if got := m.ExpertBytes(); got != expertBytes {
		t.Fatalf("expert bytes = %d, want %d", got, expertBytes)
	}
	if m.UnmodeledTensors != 0 || m.Approximate || len(m.ApproximateReasons) != 0 {
		t.Fatalf("fixture should be fully modelled: %+v", m)
	}
	if m.Tensors[0].TypeName != "f32" || m.Tensors[0].Bytes != 128 {
		t.Fatalf("first tensor = %+v", m.Tensors[0])
	}
}

func TestEstimateVRAMSplitsMoEWeights(t *testing.T) {
	kvs, tensors := moeFixture(t)
	path := writeTestGGUF(t, 3, kvs, tensors...)
	m, err := ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	const file = uint64(5408)
	const expertPerLayer = uint64(768)

	// All layers offloaded: everything is on the GPU until experts move.
	full := EstimateVRAM(m, file, 0, 4, 0, "f16", "f16")
	if full.GPUWeights != file || full.CPUWeights != 0 {
		t.Fatalf("full offload = %d/%d", full.GPUWeights, full.CPUWeights)
	}
	if full.Approximate {
		t.Fatalf("tensor-aware estimate should be exact: %+v", full)
	}

	// n_cpu_moe moves the routed experts of the first N layers to the CPU.
	var prev = full.GPUWeights
	for n := 1; n <= 4; n++ {
		e := EstimateVRAM(m, file, 0, 4, n, "f16", "f16")
		if e.GPUWeights >= prev {
			t.Fatalf("n_cpu_moe=%d did not lower GPU weights: %d >= %d", n, e.GPUWeights, prev)
		}
		if e.GPUWeights+e.CPUWeights != file {
			t.Fatalf("n_cpu_moe=%d split = %d+%d", n, e.GPUWeights, e.CPUWeights)
		}
		if e.CPUWeights != uint64(n)*expertPerLayer {
			t.Fatalf("n_cpu_moe=%d cpu = %d", n, e.CPUWeights)
		}
		prev = e.GPUWeights
	}

	// With only the last two layers offloaded, moving experts from layers that
	// already live on the CPU changes nothing.
	partial := EstimateVRAM(m, file, 0, 2, 0, "f16", "f16")
	if partial.GPUWeights != 2*1312+160 || partial.CPUWeights != 2*1312 {
		t.Fatalf("partial offload = %d/%d", partial.GPUWeights, partial.CPUWeights)
	}
	if moved := EstimateVRAM(m, file, 0, 2, 1, "f16", "f16"); moved.GPUWeights != partial.GPUWeights {
		t.Fatalf("moving a CPU layer's experts changed VRAM: %+v", moved)
	}
	third := EstimateVRAM(m, file, 0, 2, 3, "f16", "f16")
	if third.GPUWeights != partial.GPUWeights-expertPerLayer {
		t.Fatalf("n_cpu_moe=3 gpu = %d", third.GPUWeights)
	}
}

func TestEstimateVRAMFallsBackWithoutTensors(t *testing.T) {
	m := Meta{
		Architecture: "x", BlockCount: 4, Embedding: 8, HeadCount: 2,
		HeadCountKV: []int{1, 1, 1, 1}, KeyLength: 4, ValueLength: 4, ExpertCount: 8,
	}
	e := EstimateVRAM(m, 4096, 0, 2, 1, "f16", "f16")
	if !e.Approximate || len(e.Reasons) == 0 {
		t.Fatalf("fallback should be flagged: %+v", e)
	}
	if e.GPUWeights != 2048 || e.CPUWeights != 2048 {
		t.Fatalf("fallback split = %d/%d", e.GPUWeights, e.CPUWeights)
	}
	if !contains(e.Reasons, "tensor metadata unavailable; using file-size approximation") {
		t.Fatalf("reasons = %v", e.Reasons)
	}
	if !contains(e.Reasons, "n_cpu_moe is not modeled for this model") {
		t.Fatalf("n_cpu_moe reason missing: %v", e.Reasons)
	}
}

func TestEstimateVRAMRejectsUnnamedExperts(t *testing.T) {
	kvs := []testKV{
		{"general.architecture", 8, "moe-test"},
		{"moe-test.block_count", 4, uint32(1)},
		{"moe-test.embedding_length", 4, uint32(8)},
		{"moe-test.attention.head_count", 4, uint32(2)},
		{"moe-test.attention.head_count_kv", 4, uint32(1)},
		{"moe-test.attention.key_length", 4, uint32(4)},
		{"moe-test.attention.value_length", 4, uint32(4)},
		{"moe-test.expert_count", 4, uint32(8)},
	}
	tensors := []testTensor{
		{name: "blk.0.attn_q.weight", dims: []uint64{8, 8}, typ: 0},
	}
	path := writeTestGGUF(t, 3, kvs, tensors...)
	m, err := ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	e := EstimateVRAM(m, 256, 0, 1, 4, "f16", "f16")
	if !e.Approximate || e.GPUWeights != 256 {
		t.Fatalf("unnamed-expert fallback = %+v", e)
	}
	if !contains(e.Reasons, "MoE detected but expert tensors not modeled") {
		t.Fatalf("reasons = %v", e.Reasons)
	}
}

func TestEstimateVRAMUnknownTensorType(t *testing.T) {
	kvs := []testKV{
		{"general.architecture", 8, "x"},
		{"x.block_count", 4, uint32(1)},
		{"x.embedding_length", 4, uint32(8)},
		{"x.attention.head_count", 4, uint32(2)},
		{"x.attention.head_count_kv", 4, uint32(1)},
		{"x.attention.key_length", 4, uint32(4)},
		{"x.attention.value_length", 4, uint32(4)},
	}
	tensors := []testTensor{
		{name: "blk.0.attn_q.weight", dims: []uint64{8, 8}, typ: 99},
		{name: "token_embd.weight", dims: []uint64{8, 4}, typ: 0},
	}
	path := writeTestGGUF(t, 3, kvs, tensors...)
	m, err := ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.UnmodeledTensors != 1 || !m.Approximate {
		t.Fatalf("unmodeled = %+v", m)
	}
	if !contains(m.ApproximateReasons, "unsupported tensor type unknown") {
		t.Fatalf("reasons = %v", m.ApproximateReasons)
	}
	e := EstimateVRAM(m, 300, 0, 1, 0, "f16", "f16")
	if !e.Approximate || e.GPUWeights != 300 {
		t.Fatalf("unknown-type fallback = %+v", e)
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestEstimateLimitingTerm(t *testing.T) {
	cases := []struct {
		e    Estimate
		want string
	}{
		{Estimate{GPUWeights: 3, KVBytes: 2, Overhead: 1}, "weights"},
		{Estimate{GPUWeights: 2, KVBytes: 3, Overhead: 1}, "kv"},
		{Estimate{GPUWeights: 1, KVBytes: 2, Overhead: 3}, "overhead"},
		{Estimate{GPUWeights: 2, KVBytes: 2, Overhead: 2}, "weights"},
	}
	for _, tc := range cases {
		if got := tc.e.LimitingTerm(); got != tc.want {
			t.Fatalf("%+v limiting = %q, want %q", tc.e, got, tc.want)
		}
	}
}

func TestComputeBudget(t *testing.T) {
	const g = 1 << 30
	b := ComputeBudget(8*g, 3*g, 512<<20, 2*g, true)
	if !b.Known || b.OwnUsed != 2*g || b.OtherUsed != 1*g {
		t.Fatalf("budget = %+v", b)
	}
	if b.Available != 8*g-(512<<20)-g {
		t.Fatalf("available = %d", b.Available)
	}
	e := Estimate{Total: 6 * g}
	if b.Headroom(e) != int64(b.Available)-int64(e.Total) {
		t.Fatalf("headroom = %d", b.Headroom(e))
	}
	// An unmeasured own allocation counts as someone else's usage.
	unknown := ComputeBudget(8*g, 3*g, 512<<20, 0, false)
	if unknown.OwnUsed != 0 || unknown.OtherUsed != 3*g {
		t.Fatalf("unknown own = %+v", unknown)
	}
	// No GPU means no known budget.
	if none := ComputeBudget(0, 0, 0, 0, false); none.Known || none.Headroom(e) != 0 {
		t.Fatalf("none = %+v", none)
	}
	// A reserve larger than the card leaves no budget rather than wrapping.
	tiny := ComputeBudget(1*g, 0, 2*g, 0, false)
	if tiny.Available != 0 || tiny.Headroom(e) != -int64(e.Total) {
		t.Fatalf("tiny = %+v", tiny)
	}
	// Own usage is clamped to the GPU total.
	clamped := ComputeBudget(8*g, 1*g, 0, 5*g, true)
	if clamped.OwnUsed != 1*g || clamped.OtherUsed != 0 {
		t.Fatalf("clamped = %+v", clamped)
	}
}

func TestBytesPerElement(t *testing.T) {
	if BytesPerElement("q4_0") != 18.0/32.0 {
		t.Fatal("q4_0")
	}
	if BytesPerElement("f16") != 2 || BytesPerElement("unknown") != 2 {
		t.Fatal("f16/unknown")
	}
}
