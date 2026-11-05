package fit

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"wool/internal/gguf"
)

func testTensors(blocks int, perBlock, expertPerBlock, input, output uint64) []gguf.TensorInfo {
	var out []gguf.TensorInfo
	if input > 0 {
		out = append(out, gguf.TensorInfo{Name: "token_embd.weight", Bytes: input})
	}
	for i := 0; i < blocks; i++ {
		if expertPerBlock > 0 {
			out = append(out, gguf.TensorInfo{Name: blockName(i, "ffn_gate_exps.weight"), Bytes: expertPerBlock})
		}
		if perBlock > 0 {
			out = append(out, gguf.TensorInfo{Name: blockName(i, "attn_q.weight"), Bytes: perBlock})
		}
	}
	if output > 0 {
		out = append(out, gguf.TensorInfo{Name: "output.weight", Bytes: output})
	}
	return out
}

func blockName(layer int, suffix string) string {
	return "blk." + itoa(layer) + "." + suffix
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func tensorTotal(tensors []gguf.TensorInfo) uint64 {
	var n uint64
	for _, t := range tensors {
		n += t.Bytes
	}
	return n
}

func denseMeta() gguf.Meta {
	const blocks = 32
	m := gguf.Meta{
		Architecture: "llama",
		BlockCount:   blocks,
		Embedding:    4096,
		HeadCount:    32,
		HeadCountKV:  []int{8},
		KeyLength:    128,
		ValueLength:  128,
		CtxTrain:     131072,
	}
	m.Tensors = testTensors(blocks, 4<<20, 0, 16<<20, 16<<20)
	return m
}

func hybridMeta(interval int) gguf.Meta {
	const blocks = 33
	m := gguf.Meta{
		Architecture:          "qwen35",
		BlockCount:            blocks,
		Embedding:             4096,
		HeadCount:             16,
		HeadCountKV:           []int{4},
		KeyLength:             128,
		ValueLength:           128,
		CtxTrain:              262144,
		FullAttentionInterval: interval,
	}
	m.Tensors = testTensors(blocks, 4<<20, 0, 16<<20, 16<<20)
	return m
}

func moeMeta() gguf.Meta {
	const blocks = 40
	m := gguf.Meta{
		Architecture: "lfm2moe",
		BlockCount:   blocks,
		Embedding:    2048,
		HeadCount:    32,
		HeadCountKV:  []int{8},
		KeyLength:    128,
		ValueLength:  128,
		CtxTrain:     131072,
		ExpertCount:  8,
	}
	m.Tensors = testTensors(blocks, 2<<20, 8<<20, 16<<20, 16<<20)
	return m
}

func budget(total, reserve uint64) gguf.Budget {
	return gguf.ComputeBudget(total, 0, reserve, 0, false)
}

func baseSettings(m gguf.Meta, cache string) Settings {
	return Settings{
		Ctx:        32768,
		GPULayers:  m.BlockCount + 1,
		NCPUMoE:    0,
		CacheTypeK: cache,
		CacheTypeV: cache,
	}
}

func solve(t *testing.T, m gguf.Meta, b gguf.Budget, s Settings) Result {
	t.Helper()
	res, err := Solve(Request{
		ModelID:   "test-model",
		ModelPath: "/models/test.gguf",
		Meta:      m,
		FileBytes: tensorTotal(m.Tensors),
		Budget:    b,
		Settings:  s,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func assertWithinBudget(t *testing.T, res Result) {
	t.Helper()
	all := append([]Recommendation{res.Recommendation}, res.Alternatives...)
	for _, rec := range all {
		if !rec.Fits {
			t.Fatalf("recommendation %+v does not fit", rec)
		}
		if rec.Estimate.Total > res.Budget.Available {
			t.Fatalf("estimate %d exceeds budget %d: %+v", rec.Estimate.Total, res.Budget.Available, rec)
		}
		if rec.Headroom < 0 {
			t.Fatalf("negative headroom: %+v", rec)
		}
		if rec.Ctx < res.MinimumContext {
			t.Fatalf("context %d is below minimum %d", rec.Ctx, res.MinimumContext)
		}
		if rec.Ctx > res.NativeContext {
			t.Fatalf("context %d exceeds native %d", rec.Ctx, res.NativeContext)
		}
		if rec.Limiting == "" {
			t.Fatalf("missing limiting term: %+v", rec)
		}
	}
}

func assertLargest(t *testing.T, res Result, m gguf.Meta) {
	t.Helper()
	rec := res.Recommendation
	next := rec.Ctx + ContextStep
	if next > res.NativeContext {
		return
	}
	est := gguf.EstimateVRAM(m, tensorTotal(m.Tensors), next, rec.GPULayers, rec.NCPUMoE, rec.CacheTypeK, rec.CacheTypeV)
	if est.Total <= res.Budget.Available {
		t.Fatalf("ctx %d also fits at %s: recommendation %d was not the largest", next, human(est.Total), rec.Ctx)
	}
}

func human(n uint64) string {
	const unit = 1024
	return itoa(int(n/unit)) + " KiB"
}

func TestSolveDense(t *testing.T) {
	m := denseMeta()
	res := solve(t, m, budget(2<<30, 512<<20), baseSettings(m, "q8_0"))

	assertWithinBudget(t, res)
	assertLargest(t, res, m)
	if res.Recommendation.Fallback != FallbackNone {
		t.Fatalf("fallback = %q, want none", res.Recommendation.Fallback)
	}
	if res.Recommendation.Ctx >= res.NativeContext {
		t.Fatalf("expected a budget-limited context, got %d", res.Recommendation.Ctx)
	}
	if len(res.Alternatives) == 0 {
		t.Fatal("expected at least one alternative")
	}
	if res.Recommendation.Ctx%ContextStep != 0 {
		t.Fatalf("context %d is not on the %d-token grid", res.Recommendation.Ctx, ContextStep)
	}
}

func TestSolveDenseNativeCap(t *testing.T) {
	m := denseMeta()
	res := solve(t, m, budget(64<<30, 512<<20), baseSettings(m, "q8_0"))

	assertWithinBudget(t, res)
	if res.Recommendation.Ctx != res.NativeContext {
		t.Fatalf("ctx = %d, want native %d", res.Recommendation.Ctx, res.NativeContext)
	}
	if len(res.Alternatives) == 0 {
		t.Fatal("expected at least one alternative")
	}
	if res.Recommendation.CacheTypeK != "q8_0" {
		t.Fatalf("cache = %s, want requested q8_0", res.Recommendation.CacheTypeK)
	}
}

func TestSolveHybridUsesLessKV(t *testing.T) {
	hybrid := hybridMeta(4)
	dense := hybridMeta(0)
	b := budget(4<<30, 512<<20)
	hs := solve(t, hybrid, b, baseSettings(hybrid, "q8_0"))
	ds := solve(t, dense, b, baseSettings(dense, "q8_0"))

	assertWithinBudget(t, hs)
	assertWithinBudget(t, ds)
	if hs.Recommendation.Ctx <= ds.Recommendation.Ctx {
		t.Fatalf("hybrid ctx %d should exceed dense ctx %d", hs.Recommendation.Ctx, ds.Recommendation.Ctx)
	}
	if hs.Recommendation.Estimate.KVBytes >= ds.Recommendation.Estimate.KVBytes && hs.Recommendation.Ctx <= ds.Recommendation.Ctx {
		t.Fatal("hybrid model did not save KV space")
	}
}

func TestSolveMoEFallsBackToExpertOffload(t *testing.T) {
	m := moeMeta()
	res := solve(t, m, budget(800<<20, 64<<20), baseSettings(m, "q4_0"))

	assertWithinBudget(t, res)
	if res.Recommendation.Fallback != FallbackOffload {
		t.Fatalf("fallback = %q, want %q", res.Recommendation.Fallback, FallbackOffload)
	}
	if res.Recommendation.NCPUMoE == 0 {
		t.Fatalf("expected n_cpu_moe > 0: %+v", res.Recommendation)
	}
	if res.Recommendation.Estimate.CPUWeights == 0 {
		t.Fatalf("expected CPU expert weights: %+v", res.Recommendation.Estimate)
	}
	if len(res.Alternatives) == 0 {
		t.Fatal("expected at least one offload alternative")
	}
}

func TestSolveCacheFallback(t *testing.T) {
	m := denseMeta()
	res := solve(t, m, budget(800<<20, 40<<20), baseSettings(m, "f16"))

	assertWithinBudget(t, res)
	if res.Recommendation.Fallback != FallbackCache {
		t.Fatalf("fallback = %q, want %q", res.Recommendation.Fallback, FallbackCache)
	}
	if res.Recommendation.CacheTypeK == "f16" || res.Recommendation.CacheTypeV == "f16" {
		t.Fatalf("cache was not lowered: %+v", res.Recommendation)
	}
	if len(res.Alternatives) == 0 {
		t.Fatal("expected alternatives")
	}
}

func TestSolveTooSmall(t *testing.T) {
	m := denseMeta()
	_, err := Solve(Request{
		ModelID:   "test-model",
		Meta:      m,
		FileBytes: tensorTotal(m.Tensors),
		Budget:    budget(600<<20, 64<<20),
		Settings:  baseSettings(m, "q4_0"),
	})
	if !errors.Is(err, ErrNoFit) {
		t.Fatalf("err = %v, want ErrNoFit", err)
	}
}

func TestSolveNoBudget(t *testing.T) {
	m := denseMeta()
	_, err := Solve(Request{ModelID: "test-model", Meta: m, Settings: baseSettings(m, "q4_0")})
	if !errors.Is(err, ErrNoBudget) {
		t.Fatalf("err = %v, want ErrNoBudget", err)
	}
}

func TestSolveZeroAvailable(t *testing.T) {
	m := denseMeta()
	_, err := Solve(Request{
		ModelID:   "test-model",
		Meta:      m,
		FileBytes: tensorTotal(m.Tensors),
		Budget:    budget(512<<20, 512<<20),
		Settings:  baseSettings(m, "q4_0"),
	})
	if !errors.Is(err, ErrNoBudget) {
		t.Fatalf("err = %v, want ErrNoBudget", err)
	}
}

func TestSolveUnknownMetadata(t *testing.T) {
	m := gguf.Meta{CtxTrain: 8192}
	_, err := Solve(Request{
		ModelID:  "broken",
		Meta:     m,
		Budget:   budget(2<<30, 0),
		Settings: Settings{Ctx: 4096, GPULayers: 1, CacheTypeK: "q4_0", CacheTypeV: "q4_0"},
	})
	if !errors.Is(err, ErrNoMetadata) {
		t.Fatalf("err = %v, want ErrNoMetadata", err)
	}

	m = denseMeta()
	m.CtxTrain = 0
	_, err = Solve(Request{ModelID: "broken", Meta: m, Budget: budget(2<<30, 0), Settings: baseSettings(m, "q4_0")})
	if !errors.Is(err, ErrNoMetadata) {
		t.Fatalf("missing native context: err = %v, want ErrNoMetadata", err)
	}
}

func TestSolveRejectsUnknownCache(t *testing.T) {
	m := denseMeta()
	_, err := Solve(Request{
		ModelID:   "test-model",
		Meta:      m,
		FileBytes: tensorTotal(m.Tensors),
		Budget:    budget(2<<30, 0),
		Settings:  Settings{Ctx: 4096, GPULayers: 1, CacheTypeK: "q3_0", CacheTypeV: "q4_0"},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestSolveDeterministicAndJSONShape(t *testing.T) {
	m := denseMeta()
	b := budget(2<<30, 512<<20)
	a := solve(t, m, b, baseSettings(m, "q8_0"))
	c := solve(t, m, b, baseSettings(m, "q8_0"))
	if !reflect.DeepEqual(a, c) {
		t.Fatal("solver output is not deterministic")
	}
	j1, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if string(j1) != string(j2) {
		t.Fatal("JSON output is not deterministic")
	}

	var out map[string]json.RawMessage
	if err := json.Unmarshal(j1, &out); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"model", "path", "budget", "native_context", "minimum_context", "requested", "recommendation", "alternatives"} {
		if _, ok := out[key]; !ok {
			t.Fatalf("missing JSON key %q in %s", key, j1)
		}
	}
	var rec map[string]json.RawMessage
	if err := json.Unmarshal(out["recommendation"], &rec); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"rank", "ctx", "gpu_layers", "n_cpu_moe", "cache_type_k", "cache_type_v", "estimate", "headroom_bytes", "limiting_term", "fits"} {
		if _, ok := rec[key]; !ok {
			t.Fatalf("missing recommendation JSON key %q in %s", key, out["recommendation"])
		}
	}
	var est map[string]json.RawMessage
	if err := json.Unmarshal(rec["estimate"], &est); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"gpu_weights_bytes", "cpu_weights_bytes", "kv_bytes", "overhead_bytes", "total_bytes"} {
		if _, ok := est[key]; !ok {
			t.Fatalf("missing estimate JSON key %q in %s", key, rec["estimate"])
		}
	}
}

func TestParseSize(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want uint64
	}{
		{"7G", 7 << 30},
		{"7GiB", 7 << 30},
		{"7 GB", 7 << 30},
		{"512M", 512 << 20},
		{"512MiB", 512 << 20},
		{"8T", 8 << 40},
		{"1024B", 1024},
		{"1024", 1024},
	} {
		got, err := ParseSize(tc.in)
		if err != nil {
			t.Fatalf("ParseSize(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseSize(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
	for _, in := range []string{"", "G", "7X", "0", "-1G", "7.5G"} {
		if _, err := ParseSize(in); err == nil {
			t.Fatalf("ParseSize(%q) succeeded, want error", in)
		}
	}
}
