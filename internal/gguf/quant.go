package gguf

import (
	"strings"
)

type ggmlType struct {
	name  string
	block uint64 // elements per quantisation block
	size  uint64 // bytes per block
}

// ggmlTypes follows the ggml_type enum. Removed types are deliberately absent:
// a file that uses one is reported as approximate rather than guessed at.
var ggmlTypes = map[uint32]ggmlType{
	0:  {"f32", 1, 4},
	1:  {"f16", 1, 2},
	2:  {"q4_0", 32, 18},
	3:  {"q4_1", 32, 20},
	6:  {"q5_0", 32, 22},
	7:  {"q5_1", 32, 24},
	8:  {"q8_0", 32, 34},
	9:  {"q8_1", 32, 36},
	10: {"q2_K", 256, 84},
	11: {"q3_K", 256, 110},
	12: {"q4_K", 256, 144},
	13: {"q5_K", 256, 176},
	14: {"q6_K", 256, 210},
	15: {"q8_K", 256, 292},
	16: {"iq2_xxs", 256, 66},
	17: {"iq2_xs", 256, 74},
	18: {"iq3_xxs", 256, 98},
	19: {"iq1_s", 256, 50},
	20: {"iq4_nl", 32, 18},
	21: {"iq3_s", 256, 110},
	22: {"iq2_s", 256, 82},
	23: {"iq4_xs", 256, 136},
	24: {"i8", 1, 1},
	25: {"i16", 1, 2},
	26: {"i32", 1, 4},
	27: {"i64", 1, 8},
	28: {"f64", 1, 8},
	29: {"iq1_m", 256, 56},
	30: {"bf16", 1, 2},
	34: {"tq1_0", 256, 54},
	35: {"tq2_0", 256, 66},
	39: {"mxfp4", 32, 17},
	40: {"nvfp4", 64, 36},
	41: {"q1_0", 128, 18},
	42: {"q2_0", 64, 18},
	43: {"f8_e4m3", 1, 1},
}

// TypeName returns the ggml type name, or "unknown" for an unsupported type.
func TypeName(t uint32) string {
	if info, ok := ggmlTypes[t]; ok {
		return info.name
	}
	return "unknown"
}

// TensorBytes returns the stored size of a tensor with the given dimensions
// and ggml type. Rows are quantised independently, so the row width is rounded
// up to a whole number of blocks before multiplying by the row count.
func TensorBytes(t uint32, dims []uint64) (uint64, bool) {
	info, ok := ggmlTypes[t]
	if !ok || info.block == 0 {
		return 0, false
	}
	if len(dims) == 0 {
		return 0, true
	}
	const max = ^uint64(0)
	rows := uint64(1)
	for _, d := range dims[1:] {
		if d != 0 && rows > max/d {
			return 0, false
		}
		rows *= d
	}
	row := dims[0]
	if row != 0 && info.block-1 > max-row {
		return 0, false
	}
	blocks := (row + info.block - 1) / info.block
	if blocks != 0 && rows > max/blocks {
		return 0, false
	}
	rows *= blocks
	if rows != 0 && info.size > max/rows {
		return 0, false
	}
	return rows * info.size, true
}

// blockLayer parses the transformer-block index from a tensor name such as
// blk.12.ffn_gate_exps.weight.
func BytesPerElement(t string) float64 {
	switch strings.ToLower(t) {
	case "f32":
		return 4
	case "f16", "bf16":
		return 2
	case "q8_0":
		return 34.0 / 32.0
	case "q5_1":
		return 24.0 / 32.0
	case "q5_0":
		return 22.0 / 32.0
	case "q4_1":
		return 20.0 / 32.0
	case "q4_0", "iq4_nl":
		return 18.0 / 32.0
	default:
		return 2
	}
}

// Estimate is a conservative VRAM estimate. GPUWeights and KVBytes are
// charged to VRAM; CPUWeights is the part of the model that stays in system
// memory (unoffloaded layers and expert tensors moved by n_cpu_moe). Total is
// the VRAM term, so CPUWeights is deliberately not part of it.
