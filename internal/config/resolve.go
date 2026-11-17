package config

import (
	"fmt"
	"runtime"
)

func (c *Config) Resolve(modelID string, preset *PresetConfig) (Resolved, error) {
	if preset != nil {
		if preset.Model == "" {
			return Resolved{}, fmt.Errorf("preset has no model")
		}
		modelID = preset.Model
	}
	model, ok := c.Models[modelID]
	if !ok {
		return Resolved{}, fmt.Errorf("unknown model %q", modelID)
	}

	opts := builtinDefaults()
	mergeOptions(&opts, c.Defaults)
	mergeOptions(&opts, model.Options)
	if preset != nil {
		mergeOptions(&opts, preset.Options)
	}

	alias := model.Alias
	if alias == "" {
		alias = modelID
	}
	threads := derefInt(opts.Threads)
	if threads == 0 {
		threads = runtime.NumCPU()
	}
	threadsBatch := derefInt(opts.ThreadsBatch)
	if threadsBatch == 0 {
		threadsBatch = threads
	}
	sampling := applySampling(DefaultSampling(), opts.Sampling)
	extra := map[string]string{}
	for k, v := range opts.Extra {
		extra[k] = v
	}

	return Resolved{
		ModelID:      modelID,
		ModelPath:    AbsPath(model.Path),
		Alias:        alias,
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
		Extra:        extra,
	}, nil
}

// Provenance says which config layer supplied a resolved field.
func mergeOptions(dst *Options, src Options) {
	if src.Ctx != nil {
		dst.Ctx = src.Ctx
	}
	if src.GPULayers != nil {
		dst.GPULayers = src.GPULayers
	}
	if src.NCPUMoE != nil {
		dst.NCPUMoE = src.NCPUMoE
	}
	if src.Threads != nil {
		dst.Threads = src.Threads
	}
	if src.ThreadsBatch != nil {
		dst.ThreadsBatch = src.ThreadsBatch
	}
	if src.BatchSize != nil {
		dst.BatchSize = src.BatchSize
	}
	if src.UBatchSize != nil {
		dst.UBatchSize = src.UBatchSize
	}
	if src.Parallel != nil {
		dst.Parallel = src.Parallel
	}
	if src.FlashAttn != nil {
		dst.FlashAttn = src.FlashAttn
	}
	if src.CacheTypeK != nil {
		dst.CacheTypeK = src.CacheTypeK
	}
	if src.CacheTypeV != nil {
		dst.CacheTypeV = src.CacheTypeV
	}
	if src.Jinja != nil {
		dst.Jinja = src.Jinja
	}
	if src.Metrics != nil {
		dst.Metrics = src.Metrics
	}
	if src.Sampling != nil {
		if dst.Sampling == nil {
			dst.Sampling = &SamplingOverride{}
		}
		mergeSamplingOverride(dst.Sampling, src.Sampling)
	}
	if len(src.Extra) > 0 {
		if dst.Extra == nil {
			dst.Extra = map[string]string{}
		}
		for k, v := range src.Extra {
			dst.Extra[k] = v
		}
	}
}

func applySampling(base Sampling, o *SamplingOverride) Sampling {
	if o == nil {
		return base
	}
	if o.Temp != nil {
		base.Temp = *o.Temp
	}
	if o.TopP != nil {
		base.TopP = *o.TopP
	}
	if o.TopK != nil {
		base.TopK = *o.TopK
	}
	if o.MinP != nil {
		base.MinP = *o.MinP
	}
	if o.RepeatPenalty != nil {
		base.RepeatPenalty = *o.RepeatPenalty
	}
	if o.PresencePenalty != nil {
		base.PresencePenalty = *o.PresencePenalty
	}
	if o.FrequencyPenalty != nil {
		base.FrequencyPenalty = *o.FrequencyPenalty
	}
	if o.TypicalP != nil {
		base.TypicalP = *o.TypicalP
	}
	if o.Mirostat != nil {
		base.Mirostat = *o.Mirostat
	}
	if o.Seed != nil {
		base.Seed = *o.Seed
	}
	return base
}

func mergeSamplingOverride(dst, src *SamplingOverride) {
	if src.Temp != nil {
		dst.Temp = src.Temp
	}
	if src.TopP != nil {
		dst.TopP = src.TopP
	}
	if src.TopK != nil {
		dst.TopK = src.TopK
	}
	if src.MinP != nil {
		dst.MinP = src.MinP
	}
	if src.RepeatPenalty != nil {
		dst.RepeatPenalty = src.RepeatPenalty
	}
	if src.PresencePenalty != nil {
		dst.PresencePenalty = src.PresencePenalty
	}
	if src.FrequencyPenalty != nil {
		dst.FrequencyPenalty = src.FrequencyPenalty
	}
	if src.TypicalP != nil {
		dst.TypicalP = src.TypicalP
	}
	if src.Mirostat != nil {
		dst.Mirostat = src.Mirostat
	}
	if src.Seed != nil {
		dst.Seed = src.Seed
	}
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

// FormatKB is a small helper for config/doctor output.
