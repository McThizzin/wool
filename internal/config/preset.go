package config

import (
	"errors"
	"fmt"
	"strconv"
)

// ErrPresetExists is returned by SavePreset when the preset already exists and
// force is false.
var ErrPresetExists = errors.New("preset already exists")

// PresetFit is the set of launch options that reproduce a fit recommendation.
type PresetFit struct {
	Ctx        int
	GPULayers  int
	NCPUMoE    int
	CacheTypeK string
	CacheTypeV string
}

// SavePreset writes a [presets.name] section with the model reference and the
// fit-controlled options. The rest of the file, including comments and
// unrelated sections, is preserved. An existing preset is only replaced when
// force is true.
func (c *Config) SavePreset(path, name, modelID string, p PresetFit, force bool) error {
	if err := validateID("presets."+name, name); err != nil {
		return err
	}
	if _, ok := c.Models[modelID]; !ok {
		return fmt.Errorf("unknown model %q", modelID)
	}
	if _, ok := c.Presets[name]; ok && !force {
		return fmt.Errorf("%w: %s", ErrPresetExists, name)
	}
	if p.Ctx <= 0 {
		return fmt.Errorf("preset %s ctx must be positive", name)
	}
	values := []Value{
		{Key: "model", Value: strconv.Quote(modelID)},
		{Key: "ctx", Value: strconv.Itoa(p.Ctx)},
		{Key: "gpu_layers", Value: strconv.Itoa(p.GPULayers)},
		{Key: "n_cpu_moe", Value: strconv.Itoa(p.NCPUMoE)},
		{Key: "cache_type_k", Value: strconv.Quote(p.CacheTypeK)},
		{Key: "cache_type_v", Value: strconv.Quote(p.CacheTypeV)},
	}
	return SetValues(path, "presets."+name, values)
}
