package config

import (
	"fmt"
	"reflect"
)

// DiffSide is one side of a resolved-field difference.
type DiffSide struct {
	Value  any    `json:"value"`
	Source string `json:"source"`
	Layer  string `json:"layer"`
}

// DiffField is one launch option whose resolved value differs between the
// target and the comparison layer.
type DiffField struct {
	Field   string   `json:"field"`
	Target  DiffSide `json:"target"`
	Against DiffSide `json:"against"`
}

// DiffResult is the outcome of comparing a target with a config layer.
type DiffResult struct {
	Against string      `json:"against"`
	Fields  []DiffField `json:"fields"`
}

// DefaultDiffAgainst returns the comparison layer used when none is given.
// A preset is measured against its model, so the diff shows only what the
// preset changes. A model is measured against the defaults layer.
func DefaultDiffAgainst(presetName string) string {
	if presetName != "" {
		return SourceModel
	}
	return SourceDefaults
}

// Diff compares every resolved launch option with the layer named by against
// ("defaults" or "model"). An empty against selects DefaultDiffAgainst. Only
// fields whose resolved values differ are returned, in canonical option order.
func (c *Config) Diff(modelID, presetName, against string) (DiffResult, error) {
	var target Resolved
	var targetSources map[string]Provenance
	var err error
	if presetName != "" {
		target, targetSources, err = c.ResolveDetailed("", presetName)
	} else {
		target, targetSources, err = c.ResolveDetailed(modelID, "")
	}
	if err != nil {
		return DiffResult{}, err
	}

	if against == "" {
		against = DefaultDiffAgainst(presetName)
	}
	switch against {
	case SourceDefaults:
	case SourceModel:
		if presetName == "" {
			return DiffResult{}, fmt.Errorf("a model can only be diffed against defaults")
		}
	default:
		return DiffResult{}, fmt.Errorf("unknown comparison layer %q (want defaults or model)", against)
	}

	var base Resolved
	var baseSources map[string]Provenance
	if against == SourceDefaults {
		base = c.ResolveDefaults()
		baseSources = make(map[string]Provenance, len(optionFields))
		for _, field := range optionFields {
			baseSources[field] = c.provenance("", "", field)
		}
	} else {
		base, baseSources, err = c.ResolveDetailed(target.ModelID, "")
		if err != nil {
			return DiffResult{}, err
		}
	}

	result := DiffResult{Against: against, Fields: []DiffField{}}
	for _, field := range optionFields {
		targetValue := FieldValue(target, field)
		baseValue := FieldValue(base, field)
		if reflect.DeepEqual(targetValue, baseValue) {
			continue
		}
		tp, bp := targetSources[field], baseSources[field]
		result.Fields = append(result.Fields, DiffField{
			Field:   field,
			Target:  DiffSide{Value: targetValue, Source: tp.Source, Layer: tp.Layer},
			Against: DiffSide{Value: baseValue, Source: bp.Source, Layer: bp.Layer},
		})
	}
	return result, nil
}

// FieldValue returns the resolved value of a canonical launch option.
func FieldValue(r Resolved, field string) any {
	switch field {
	case "ctx":
		return r.Ctx
	case "gpu_layers":
		return r.GPULayers
	case "n_cpu_moe":
		return r.NCPUMoE
	case "threads":
		return r.Threads
	case "threads_batch":
		return r.ThreadsBatch
	case "batch_size":
		return r.BatchSize
	case "ubatch_size":
		return r.UBatchSize
	case "parallel":
		return r.Parallel
	case "flash_attn":
		return r.FlashAttn
	case "cache_type_k":
		return r.CacheTypeK
	case "cache_type_v":
		return r.CacheTypeV
	case "jinja":
		return r.Jinja
	case "metrics":
		return r.Metrics
	case "sampling.temp":
		return r.Sampling.Temp
	case "sampling.top_p":
		return r.Sampling.TopP
	case "sampling.top_k":
		return r.Sampling.TopK
	case "sampling.min_p":
		return r.Sampling.MinP
	case "sampling.repeat_penalty":
		return r.Sampling.RepeatPenalty
	case "sampling.presence_penalty":
		return r.Sampling.PresencePenalty
	case "sampling.frequency_penalty":
		return r.Sampling.FrequencyPenalty
	case "sampling.typical_p":
		return r.Sampling.TypicalP
	case "sampling.mirostat":
		return r.Sampling.Mirostat
	case "sampling.seed":
		return r.Sampling.Seed
	}
	return nil
}
