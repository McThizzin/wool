package fit

import (
	"fmt"
	"strings"

	"wool/internal/gguf"
)

func (s *solver) cacheSettings() []Settings {
	curK, okK := cacheBytes(s.settings.CacheTypeK)
	curV, okV := cacheBytes(s.settings.CacheTypeV)
	if !okK || !okV {
		return nil
	}
	var out []Settings
	for _, name := range cacheOrder {
		b, ok := cacheBytes(name)
		if !ok {
			continue
		}
		if b > curK || b > curV {
			continue
		}
		if b == curK && b == curV {
			continue
		}
		set := s.settings
		set.CacheTypeK, set.CacheTypeV = name, name
		out = append(out, set)
	}
	return out
}

// offloadSettings moves work to the CPU. MoE models first give up expert
// tensors with n_cpu_moe; every model can also reduce gpu_layers.
func (s *solver) offloadSettings() []Settings {
	var out []Settings
	if s.req.Meta.ExpertCount > 0 {
		for _, num := range []int{1, 2, 3, 4} {
			n := s.req.Meta.BlockCount * num / 4
			if n <= s.settings.NCPUMoE {
				continue
			}
			set := s.settings
			set.NCPUMoE = n
			out = append(out, set)
		}
		if s.req.Meta.BlockCount > s.settings.NCPUMoE {
			set := s.settings
			set.NCPUMoE = s.req.Meta.BlockCount
			out = append(out, set)
		}
	}
	all := s.req.Meta.BlockCount + 1
	fractions := []int{3, 2, 1, 0}
	if s.req.Meta.ExpertCount > 0 {
		// Keep at least some layers on the GPU; all-CPU is added only if no
		// expert-offload placement fits.
		fractions = []int{3, 2, 1}
	}
	for _, num := range fractions {
		layers := all * num / 4
		if layers >= s.settings.GPULayers {
			continue
		}
		set := s.settings
		set.GPULayers = layers
		out = append(out, set)
	}
	return out
}

// combinedSettings pairs the most compressed cache with every CPU-offload
// placement. It is the last resort before reporting that the quant is too big.
func (s *solver) combinedSettings() []Settings {
	base := s.settings
	for _, set := range s.cacheSettings() {
		base.CacheTypeK, base.CacheTypeV = set.CacheTypeK, set.CacheTypeV
	}
	var out []Settings
	for _, set := range s.offloadSettings() {
		set.CacheTypeK, set.CacheTypeV = base.CacheTypeK, base.CacheTypeV
		out = append(out, set)
	}
	if s.req.Meta.ExpertCount > 0 && s.settings.GPULayers > 0 {
		set := s.settings
		set.GPULayers = 0
		set.CacheTypeK, set.CacheTypeV = base.CacheTypeK, base.CacheTypeV
		out = append(out, set)
	}
	return out
}

func normalizeSettings(m gguf.Meta, s Settings) (Settings, error) {
	all := m.BlockCount + 1
	if s.GPULayers < 0 || s.GPULayers > all {
		s.GPULayers = all
	}
	if s.NCPUMoE < 0 {
		s.NCPUMoE = 0
	}
	if s.NCPUMoE > m.BlockCount {
		s.NCPUMoE = m.BlockCount
	}
	k, err := normalizeCache(s.CacheTypeK)
	if err != nil {
		return Settings{}, err
	}
	v, err := normalizeCache(s.CacheTypeV)
	if err != nil {
		return Settings{}, err
	}
	s.CacheTypeK, s.CacheTypeV = k, v
	return s, nil
}

func normalizeCache(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "q4_0", nil
	}
	switch name {
	case "fp16":
		name = "f16"
	case "fp32":
		name = "f32"
	}
	if _, ok := cacheBytes(name); !ok {
		return "", fmt.Errorf("unsupported cache type %q (want f32, f16, bf16, q8_0, q5_1, q5_0, q4_1, q4_0 or iq4_nl)", name)
	}
	return name, nil
}

// cacheOrder is highest storage first. The solver only steps downward.
var cacheOrder = []string{"f32", "f16", "bf16", "q8_0", "q5_1", "q5_0", "q4_1", "q4_0", "iq4_nl"}

func cacheBytes(name string) (float64, bool) {
	switch name {
	case "f32":
		return 4, true
	case "f16", "bf16":
		return 2, true
	case "q8_0":
		return 34.0 / 32.0, true
	case "q5_1":
		return 24.0 / 32.0, true
	case "q5_0":
		return 22.0 / 32.0, true
	case "q4_1":
		return 20.0 / 32.0, true
	case "q4_0", "iq4_nl":
		return 18.0 / 32.0, true
	default:
		return 0, false
	}
}
