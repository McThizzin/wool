package gguf

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

type testKV struct {
	key string
	typ uint32
	val any
}

func writeTestGGUF(t *testing.T, version uint32, kvs []testKV) string {
	t.Helper()
	var b bytes.Buffer
	w32 := func(v uint32) { binary.Write(&b, binary.LittleEndian, v) }
	w64 := func(v uint64) { binary.Write(&b, binary.LittleEndian, v) }
	wstr := func(s string) { w64(uint64(len(s))); b.WriteString(s) }

	w32(magic)
	w32(version)
	w64(0) // tensors
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
	e := EstimateVRAM(m, file, 1024, 1, "q4_0", "q4_0")
	if e.WeightsBytes != 5*g {
		t.Fatalf("weights = %d, want %d", e.WeightsBytes, uint64(5*g))
	}
	// 1024 * (4*0.5625 + 4*0.5625) * 2 layers = 9216
	if e.KVBytes != 9216 {
		t.Fatalf("kv = %d", e.KVBytes)
	}
	if e.Overhead != 512<<20 {
		t.Fatalf("overhead = %d", e.Overhead)
	}
	if e.Total != e.WeightsBytes+e.KVBytes+e.Overhead {
		t.Fatalf("total = %+v", e)
	}
	// gpu layers above the block count are clamped to all layers
	all := EstimateVRAM(m, file, 1024, 999, "f16", "f16")
	if all.WeightsBytes != file {
		t.Fatalf("all weights = %d", all.WeightsBytes)
	}
	if all.KVBytes != 1024*4*2*2*2 {
		t.Fatalf("f16 kv = %d", all.KVBytes)
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
