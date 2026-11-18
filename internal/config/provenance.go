package config

import (
	"fmt"
	"runtime"
	"strings"
)

type Provenance struct {
	Source string `json:"source"` // built-in, defaults, model or preset
	Layer  string `json:"layer"`  // the exact TOML layer
}

// Source labels reported by ResolveDetailed.
const (
	SourceBuiltin  = "built-in"
	SourceDefaults = "defaults"
	SourceModel    = "model"
	SourcePreset   = "preset"
)

// optionFields is the canonical order of launch options exposed by the editor
// and `wool config explain`.
var optionFields = []string{
	"ctx",
	"gpu_layers",
	"n_cpu_moe",
	"threads",
	"threads_batch",
	"batch_size",
	"ubatch_size",
	"parallel",
	"flash_attn",
	"cache_type_k",
	"cache_type_v",
	"jinja",
	"metrics",
	"sampling.temp",
	"sampling.top_p",
	"sampling.top_k",
	"sampling.min_p",
	"sampling.repeat_penalty",
	"sampling.presence_penalty",
	"sampling.frequency_penalty",
	"sampling.typical_p",
	"sampling.mirostat",
	"sampling.seed",
}

// OptionFields returns the canonical launch-option names in display order.
func OptionFields() []string {
	return append([]string(nil), optionFields...)
}

// ResolveDetailed merges the same layers as Resolve and reports where each
// launch option came from. Pass presetName to resolve a preset; pass an empty
// presetName and a model ID to resolve a model.
func (c *Config) ResolveDetailed(modelID, presetName string) (Resolved, map[string]Provenance, error) {
	var preset *PresetConfig
	if presetName != "" {
		p, ok := c.Presets[presetName]
		if !ok {
			return Resolved{}, nil, fmt.Errorf("unknown preset %q", presetName)
		}
		preset = &p
	}
	r, err := c.Resolve(modelID, preset)
	if err != nil {
		return Resolved{}, nil, err
	}
	return r, c.sourcesFor(r, r.ModelID, presetName), nil
}

// sourcesFor reports where each resolved option, including extras, came from.
func (c *Config) sourcesFor(r Resolved, modelID, presetName string) map[string]Provenance {
	sources := make(map[string]Provenance, len(optionFields)+len(r.Extra))
	for _, field := range optionFields {
		sources[field] = c.provenance(modelID, presetName, field)
	}
	for _, flag := range ExtraFlags(r.Extra) {
		sources[ExtraFieldName(flag)] = c.extraProvenance(modelID, presetName, flag)
	}
	return sources
}

// provenance checks layers from highest to lowest precedence.
func (c *Config) provenance(modelID, presetName, field string) Provenance {
	if presetName != "" {
		if p, ok := c.Presets[presetName]; ok && optionFieldSet(p.Options, field) {
			return Provenance{Source: SourcePreset, Layer: "presets." + presetName}
		}
	}
	if m, ok := c.Models[modelID]; ok && optionFieldSet(m.Options, field) {
		return Provenance{Source: SourceModel, Layer: "models." + modelID}
	}
	if optionFieldSet(c.Defaults, field) {
		return Provenance{Source: SourceDefaults, Layer: "defaults"}
	}
	return Provenance{Source: SourceBuiltin, Layer: "built-in"}
}

// extraProvenance uses the same precedence as option provenance, but looks in
// each layer's extra map instead of the fixed option fields.
func (c *Config) extraProvenance(modelID, presetName, flag string) Provenance {
	if presetName != "" {
		if p, ok := c.Presets[presetName]; ok && extraSet(p.Options, flag) {
			return Provenance{Source: SourcePreset, Layer: "presets." + presetName}
		}
	}
	if m, ok := c.Models[modelID]; ok && extraSet(m.Options, flag) {
		return Provenance{Source: SourceModel, Layer: "models." + modelID}
	}
	if extraSet(c.Defaults, flag) {
		return Provenance{Source: SourceDefaults, Layer: "defaults"}
	}
	return Provenance{Source: SourceBuiltin, Layer: "built-in"}
}

func extraSet(o Options, flag string) bool {
	_, ok := NormalizeExtraMap(o.Extra)[flag]
	return ok
}

func optionFieldSet(o Options, field string) bool {
	if key, ok := strings.CutPrefix(field, "sampling."); ok {
		return samplingFieldSet(o.Sampling, key)
	}
	switch field {
	case "ctx":
		return o.Ctx != nil
	case "gpu_layers":
		return o.GPULayers != nil
	case "n_cpu_moe":
		return o.NCPUMoE != nil
	case "threads":
		return o.Threads != nil
	case "threads_batch":
		return o.ThreadsBatch != nil
	case "batch_size":
		return o.BatchSize != nil
	case "ubatch_size":
		return o.UBatchSize != nil
	case "parallel":
		return o.Parallel != nil
	case "flash_attn":
		return o.FlashAttn != nil
	case "cache_type_k":
		return o.CacheTypeK != nil
	case "cache_type_v":
		return o.CacheTypeV != nil
	case "jinja":
		return o.Jinja != nil
	case "metrics":
		return o.Metrics != nil
	}
	return false
}

func samplingFieldSet(o *SamplingOverride, key string) bool {
	if o == nil {
		return false
	}
	switch key {
	case "temp":
		return o.Temp != nil
	case "top_p":
		return o.TopP != nil
	case "top_k":
		return o.TopK != nil
	case "min_p":
		return o.MinP != nil
	case "repeat_penalty":
		return o.RepeatPenalty != nil
	case "presence_penalty":
		return o.PresencePenalty != nil
	case "frequency_penalty":
		return o.FrequencyPenalty != nil
	case "typical_p":
		return o.TypicalP != nil
	case "mirostat":
		return o.Mirostat != nil
	case "seed":
		return o.Seed != nil
	}
	return false
}

// ResolveDefaults returns the defaults layer with no model-specific options.
func (c *Config) ResolveDefaults() Resolved {
	opts := builtinDefaults()
	mergeOptions(&opts, c.Defaults)
	threads := derefInt(opts.Threads)
	if threads == 0 {
		threads = runtime.NumCPU()
	}
	threadsBatch := derefInt(opts.ThreadsBatch)
	if threadsBatch == 0 {
		threadsBatch = threads
	}
	sampling := applySampling(DefaultSampling(), opts.Sampling)
	return Resolved{
		Ctx:          derefInt(opts.Ctx),
		GPULayers:    derefInt(opts.GPULayers),
		NCPUMoE:      derefInt(opts.NCPUMoE),
		Threads:      threads,
		ThreadsBatch: threadsBatch,
		BatchSize:    derefInt(opts.BatchSize),
		UBatchSize:   derefInt(opts.UBatchSize),
		Parallel:     derefInt(opts.Parallel),
		FlashAttn:    derefString(opts.FlashAttn),
		CacheTypeK:   derefString(opts.CacheTypeK),
		CacheTypeV:   derefString(opts.CacheTypeV),
		Jinja:        derefBool(opts.Jinja),
		Metrics:      derefBool(opts.Metrics),
		Sampling:     sampling,
		Extra:        NormalizeExtraMap(opts.Extra),
	}
}
