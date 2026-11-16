package gguf

import (
	"strings"
)

type tensorClass int

const (
	tensorUnknown tensorClass = iota
	tensorInput
	tensorOutput
	tensorGlobal
)

// The lists below mirror llama.cpp's LLM_TENSOR_INFOS classification. Input
// tensors always stay on the CPU. Output tensors follow the output layer,
// which is the extra layer after the transformer blocks. Repeating tensors
// belong to a layer (usually layer 0). Any name outside these lists makes the
// placement uncertain, so the estimator falls back rather than guessing.
var inputTensorNames = []string{
	"token_embd",
	"position_embd",
	"token_types",
	"value_embd",
	"per_layer_token_embd",
	"masked_embd_centroids",
	"masked_embd_ordering",
	"mask_embd",
}

var outputTensorNames = []string{
	"output",
	"cls",
	"cls.output",
	"cls.norm",
	"decision.hidden_norm",
	"decision.proj_memory",
	"decision.proj_question",
	"decision.proj_option_question",
	"decision.proj_global",
	"decision.proj_option_context",
	"decision.proj_option_lexical",
	"decision.option_summary_norm",
	"decision.field_norm",
	"decision.option_norm",
	"decision.scales",
	"decision.scorer",
	"decision.scorer_out",
	"decision.query_norm",
	"decision.key",
	"decision.query",
	"decision.option_mlp",
	"decision.query_mlp",
	"dense_2",
	"dense_3",
	"output_norm",
	"dec.output_norm",
	"enc.output_norm",
	"output_hc_fn",
	"output_hc_base",
	"output_hc_scale",
	"output_hc_norm",
	"output_hc_down",
	"output_hc_up",
	"output_res_score",
	"altup_proj",
	"altup_unembd_proj",
	"nextn.post_projection",
	"fc",
	"d2t",
	"markov_w1",
	"markov_w2",
	"conf_proj",
	"joint_proj",
	"selector_predecessor",
	"selector_successor",
	"selector_hidden",
	"lilicorr.token_proj",
	"lilicorr.pass_hidden_proj",
	"lilicorr.slot_embedding",
	"lilicorr.rank_embedding",
	"lilicorr.attention_bias",
	"lilicorr.context_proj",
	"lilicorr.output_norm",
	"lilicorr.anchor_norm",
	"lilicorr.factor_input_proj",
	"lilicorr.out_head",
	"lilicorr.in_head",
	"lilicorr.anchor_out_head",
}

var globalTensorNames = []string{
	"hrm.z_l_init",
	"rope_freqs",
	"rope_factors_long",
	"rope_factors_short",
	"per_layer_model_proj",
	"per_layer_proj_norm",
	"conv1d",
	"nextn.pre_projection",
}

func tensorClassOf(arch, name string) tensorClass {
	// token_embd_norm is a repeating tensor in most architectures, but LFM2
	// reuses the name for its output norm.
	if name == "token_embd_norm" || strings.HasPrefix(name, "token_embd_norm.") {
		if arch == "lfm2" || arch == "lfm2moe" {
			return tensorOutput
		}
		return tensorGlobal
	}
	switch {
	case matchesTensorNames(inputTensorNames, name):
		return tensorInput
	case matchesTensorNames(outputTensorNames, name):
		return tensorOutput
	case matchesTensorNames(globalTensorNames, name):
		return tensorGlobal
	default:
		return tensorUnknown
	}
}

func matchesTensorNames(names []string, name string) bool {
	for _, base := range names {
		if name == base || strings.HasPrefix(name, base+".") {
			return true
		}
	}
	return false
}

// placeWeights splits the model into GPU and CPU weight terms using the parsed
// tensor table. It reports false with a reason when the table cannot be
// trusted, in which case the caller falls back to the file-size fraction.
func (m Meta) placeWeights(fileBytes uint64, gpuLayers, nCPUMoE int) (gpu, cpu, experts uint64, reason string, ok bool) {
	if len(m.Tensors) == 0 || m.BlockCount <= 0 || m.UnmodeledTensors > 0 {
		return 0, 0, 0, "", false
	}
	block := make([]uint64, m.BlockCount)
	expert := make([]uint64, m.BlockCount)
	var input, output, global uint64
	var accounted uint64
	for _, t := range m.Tensors {
		accounted += t.Bytes
		if layer, isBlock := blockLayer(t.Name); isBlock {
			if layer >= m.BlockCount {
				return 0, 0, 0, "tensor " + t.Name + " is outside block_count", false
			}
			block[layer] += t.Bytes
			if isExpertTensor(t.Name) {
				expert[layer] += t.Bytes
			}
			continue
		}
		switch tensorClassOf(m.Architecture, t.Name) {
		case tensorInput:
			input += t.Bytes
		case tensorOutput:
			output += t.Bytes
		case tensorGlobal:
			global += t.Bytes
		default:
			return 0, 0, 0, "unknown tensor placement: " + t.Name, false
		}
	}
	for _, n := range expert {
		experts += n
	}
	// A file always contains at least its tensor data. A larger computed total
	// means the type table or dimensions are wrong, so do not trust it.
	if accounted == 0 || (fileBytes > 0 && accounted > fileBytes) {
		return 0, 0, 0, "", false
	}
	// An architecture that declares experts but has no routed-expert tensors
	// is outside the naming table. Fall back rather than silently ignoring
	// n_cpu_moe.
	if m.ExpertCount > 0 && experts == 0 {
		return 0, 0, 0, "", false
	}

	// llama.cpp counts the output layer as layer BlockCount: with N GPU
	// layers, the last N of BlockCount+1 layers are on the GPU. Input tensors
	// stay on the CPU regardless. Repeating tensors follow layer 0.
	gpuStart := m.BlockCount + 1 - gpuLayers
	if gpuStart < 0 {
		gpuStart = 0
	}
	if gpuStart > m.BlockCount {
		gpuStart = m.BlockCount
	}
	if gpuLayers >= 1 {
		gpu += output
	} else {
		cpu += output
	}
	if gpuStart == 0 {
		gpu += global
	} else {
		cpu += global
	}
	cpu += input
	// Metadata and alignment padding are mapped from the file and stay in RAM.
	if fileBytes > accounted {
		cpu += fileBytes - accounted
	}
	for i := 0; i < m.BlockCount; i++ {
		bytes := block[i]
		if i < gpuStart {
			cpu += bytes
			continue
		}
		moved := uint64(0)
		if i < nCPUMoE {
			moved = expert[i]
			if moved > bytes {
				moved = bytes
			}
		}
		gpu += bytes - moved
		cpu += moved
	}
	return gpu, cpu, experts, "", true
}

func appendUnique(dst []string, values ...string) []string {
	for _, v := range values {
		found := false
		for _, existing := range dst {
			if existing == v {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, v)
		}
	}
	return dst
}
