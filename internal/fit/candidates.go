package fit

import (
	"fmt"
	"sort"
)

func choosePrimary(candidates []candidate) candidate {
	for t := tierRequested; t <= tierCombined; t++ {
		var best *candidate
		for i := range candidates {
			c := &candidates[i]
			if c.tier != t {
				continue
			}
			if best == nil || betterPrimary(*c, *best) {
				best = c
			}
		}
		if best != nil {
			return *best
		}
	}
	return candidates[0]
}

func betterPrimary(a, b candidate) bool {
	if a.rec.Ctx != b.rec.Ctx {
		return a.rec.Ctx > b.rec.Ctx
	}
	if a.tier == tierCache || a.tier == tierCombined {
		ak, _ := cacheBytes(a.rec.CacheTypeK)
		bk, _ := cacheBytes(b.rec.CacheTypeK)
		av, _ := cacheBytes(a.rec.CacheTypeV)
		bv, _ := cacheBytes(b.rec.CacheTypeV)
		if ak+av != bk+bv {
			return ak+av > bk+bv
		}
	}
	if a.rec.GPULayers != b.rec.GPULayers {
		return a.rec.GPULayers > b.rec.GPULayers
	}
	if a.rec.NCPUMoE != b.rec.NCPUMoE {
		return a.rec.NCPUMoE < b.rec.NCPUMoE
	}
	return a.rec.Estimate.Total < b.rec.Estimate.Total
}

func alternatives(candidates []candidate, primary candidate, limit int) []Recommendation {
	key := candidateKey(primary.rec)
	var rest []candidate
	for _, c := range candidates {
		if candidateKey(c.rec) == key {
			continue
		}
		rest = append(rest, c)
	}
	sort.SliceStable(rest, func(i, j int) bool {
		a, b := rest[i], rest[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.rec.Ctx != b.rec.Ctx {
			return a.rec.Ctx > b.rec.Ctx
		}
		// At the same context, prefer more work on the GPU and a less
		// compressed cache. Those are the faster/higher-quality trade-offs.
		if a.rec.GPULayers != b.rec.GPULayers {
			return a.rec.GPULayers > b.rec.GPULayers
		}
		if a.rec.NCPUMoE != b.rec.NCPUMoE {
			return a.rec.NCPUMoE < b.rec.NCPUMoE
		}
		if a.rec.Estimate.Total != b.rec.Estimate.Total {
			return a.rec.Estimate.Total > b.rec.Estimate.Total
		}
		if a.rec.CacheTypeK != b.rec.CacheTypeK {
			return a.rec.CacheTypeK < b.rec.CacheTypeK
		}
		return a.rec.CacheTypeV < b.rec.CacheTypeV
	})
	if limit > 0 && len(rest) > limit {
		rest = rest[:limit]
	}
	out := make([]Recommendation, len(rest))
	for i := range rest {
		out[i] = rest[i].rec
	}
	return out
}

func changes(requested, actual Settings, ctx int) []string {
	var out []string
	add := func(name string, from, to any) {
		if fmt.Sprint(from) != fmt.Sprint(to) {
			out = append(out, fmt.Sprintf("%s %v -> %v", name, from, to))
		}
	}
	add("ctx", requested.Ctx, ctx)
	add("gpu_layers", requested.GPULayers, actual.GPULayers)
	add("n_cpu_moe", requested.NCPUMoE, actual.NCPUMoE)
	add("cache_type_k", requested.CacheTypeK, actual.CacheTypeK)
	add("cache_type_v", requested.CacheTypeV, actual.CacheTypeV)
	return out
}

func candidateKey(r Recommendation) string {
	return fmt.Sprintf("%d/%d/%d/%s/%s", r.Ctx, r.GPULayers, r.NCPUMoE, r.CacheTypeK, r.CacheTypeV)
}

func hasTier(candidates []candidate, t tier) bool {
	for _, c := range candidates {
		if c.tier == t {
			return true
		}
	}
	return false
}

func fallbackText(t tier) string {
	switch t {
	case tierCache:
		return "lower KV cache precision"
	case tierOffload:
		return "CPU offload"
	case tierCombined:
		return "lower KV cache precision and CPU offload"
	default:
		return "none"
	}
}

func placement(s Settings) string {
	return fmt.Sprintf("gpu_layers %d, n_cpu_moe %d, cache %s/%s", s.GPULayers, s.NCPUMoE, s.CacheTypeK, s.CacheTypeV)
}

// ParseSize parses a VRAM size such as 7G, 7GiB, 512M or 8000MB. Binary
// suffixes (K/M/G/T, optionally with iB) are powers of 1024, matching how VRAM
// is usually quoted.
