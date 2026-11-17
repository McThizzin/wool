package gguf

import (
	"strconv"
	"strings"
)

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

// SlidingWindowLayer reports whether a layer uses sliding-window attention.
// An explicit per-layer array takes precedence over the scalar pattern. The
// scalar pattern follows llama.cpp's set_swa_pattern with dense_first=false:
// every Nth layer is dense and the rest are SWA (N=0 means all SWA, N=1 all
// dense).
func (m Meta) SlidingWindowLayer(layer int) bool {
	if len(m.SlidingWindowPattern) > 0 {
		return layer < len(m.SlidingWindowPattern) && m.SlidingWindowPattern[layer] != 0
	}
	if !m.SlidingWindowPatternSet || m.SlidingWindow <= 0 {
		return false
	}
	n := m.SlidingWindowPatternN
	if n <= 0 {
		return true
	}
	return layer%n != n-1
}

// KVLayers returns the number of leading layers that allocate a KV cache.
// Trailing shared-KV layers reuse an earlier cache and add no memory.
func (m Meta) KVLayers() int {
	if m.BlockCount <= 0 {
		return 0
	}
	n := m.BlockCount - m.SharedKVLayers
	if n < 1 {
		n = 1
	}
	return n
}

// KVBytes estimates the KV cache for ctx tokens with the given cache types.
// SWA layers are charged min(ctx, window); full-attention layers are charged
// the whole context. Recurrent and shared-KV layers are skipped.
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
	layers := m.KVLayers()
	var total float64
	for i := 0; i < layers; i++ {
		if !m.FullAttention(i) {
			continue
		}
		layerK, layerV := kd, vd
		tokens := ctx
		if m.SlidingWindowLayer(i) {
			if m.KeyLengthSWA > 0 {
				layerK = m.KeyLengthSWA
			}
			if m.ValueLengthSWA > 0 {
				layerV = m.ValueLengthSWA
			}
			if m.SlidingWindow > 0 && m.SlidingWindow < tokens {
				tokens = m.SlidingWindow
			}
		}
		h := float64(m.HeadsAt(i))
		total += float64(tokens) * h * (float64(layerK)*bk + float64(layerV)*bv)
	}
	return uint64(total)
}

// BytesPerElement returns the storage cost of one cache element for a
// llama.cpp cache type string.
