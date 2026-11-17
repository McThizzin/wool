package gguf

type Estimate struct {
	FileBytes   uint64   `json:"file_bytes"`
	GPUWeights  uint64   `json:"gpu_weights_bytes"`
	CPUWeights  uint64   `json:"cpu_weights_bytes"`
	KVBytes     uint64   `json:"kv_bytes"`
	Overhead    uint64   `json:"overhead_bytes"`
	Total       uint64   `json:"total_bytes"`
	Approximate bool     `json:"approximate,omitempty"`
	Reasons     []string `json:"approximate_reasons,omitempty"`
}

// LimitingTerm names the largest VRAM component of the estimate. Ties prefer
// weights because they dominate most dense models.
func (e Estimate) LimitingTerm() string {
	switch {
	case e.GPUWeights >= e.KVBytes && e.GPUWeights >= e.Overhead:
		return "weights"
	case e.KVBytes >= e.Overhead:
		return "kv"
	default:
		return "overhead"
	}
}

// Budget is the VRAM available to a new server. Used is the GPU's current
// usage; OwnUsed is the part belonging to the running wool server and is
// excluded from OtherUsed so a restart is not charged twice.
type Budget struct {
	Total     uint64 `json:"total_bytes"`
	Used      uint64 `json:"used_bytes"`
	Reserve   uint64 `json:"reserve_bytes"`
	OwnUsed   uint64 `json:"own_used_bytes"`
	OtherUsed uint64 `json:"other_used_bytes"`
	Available uint64 `json:"available_bytes"`
	Known     bool   `json:"known"`
	OwnKnown  bool   `json:"own_known"`
}

// ComputeBudget derives the budget for a new server. own is the running
// wool server's VRAM; pass ownKnown=false when it cannot be measured, in
// which case all current usage counts as other usage.
func ComputeBudget(total, used, reserve, own uint64, ownKnown bool) Budget {
	b := Budget{
		Total:    total,
		Used:     used,
		Reserve:  reserve,
		OwnKnown: ownKnown,
	}
	if total == 0 {
		return b
	}
	b.Known = true
	if ownKnown {
		if own > used {
			own = used
		}
		b.OwnUsed = own
	}
	b.OtherUsed = used - b.OwnUsed
	if reserve >= total {
		return b
	}
	remaining := total - reserve
	if b.OtherUsed < remaining {
		b.Available = remaining - b.OtherUsed
	}
	return b
}

// Headroom is the signed difference between the budget and an estimate.
// Positive values fit; negative values exceed the budget. It is zero when the
// budget is unknown.
func (b Budget) Headroom(e Estimate) int64 {
	if !b.Known {
		return 0
	}
	return int64(b.Available) - int64(e.Total)
}

// EstimateVRAM estimates GPU memory for the given context. gpuLayers is the
// number of layers offloaded, counting the output layer like llama.cpp does:
// the last N of BlockCount+1 layers are placed on the GPU. Values greater
// than BlockCount+1 mean all and negative means all. nCPUMoE is llama.cpp's
// --n-cpu-moe: the routed-expert tensors of the first N layers stay on the
// CPU. Overhead covers compute buffers and allocator slack, not the KV cache.
func EstimateVRAM(m Meta, fileBytes uint64, ctx, gpuLayers, nCPUMoE int, cacheK, cacheV string) Estimate {
	if gpuLayers < 0 {
		gpuLayers = m.BlockCount + 1
	}
	if m.BlockCount > 0 && gpuLayers > m.BlockCount+1 {
		gpuLayers = m.BlockCount + 1
	}
	if nCPUMoE < 0 {
		nCPUMoE = 0
	}
	if nCPUMoE > m.BlockCount {
		nCPUMoE = m.BlockCount
	}

	est := Estimate{
		FileBytes: fileBytes,
		KVBytes:   m.KVBytes(ctx, cacheK, cacheV),
		Overhead:  512 << 20,
	}

	gpu, cpu, experts, reason, ok := m.placeWeights(fileBytes, gpuLayers, nCPUMoE)
	if ok {
		est.GPUWeights, est.CPUWeights = gpu, cpu
	} else {
		// The file-size fallback treats gpu_layers as transformer blocks, so
		// the output layer does not push the fraction past one.
		blocks := gpuLayers
		if blocks > m.BlockCount {
			blocks = m.BlockCount
		}
		frac := 0.0
		if m.BlockCount > 0 {
			frac = float64(blocks) / float64(m.BlockCount)
		}
		est.GPUWeights = uint64(float64(fileBytes) * frac)
		est.CPUWeights = fileBytes - est.GPUWeights
		est.Approximate = true
		switch {
		case reason != "":
			est.Reasons = append(est.Reasons, reason)
		case len(m.Tensors) == 0:
			est.Reasons = append(est.Reasons, "tensor metadata unavailable; using file-size approximation")
		case m.UnmodeledTensors > 0:
			est.Reasons = append(est.Reasons, "some tensor types are not modelled; using file-size approximation")
		case m.ExpertCount > 0:
			est.Reasons = append(est.Reasons, "MoE detected but expert tensors not modeled")
		default:
			est.Reasons = append(est.Reasons, "tensor layout not modelled; using file-size approximation")
		}
	}

	if m.Approximate {
		est.Approximate = true
		est.Reasons = appendUnique(est.Reasons, m.ApproximateReasons...)
	}
	if nCPUMoE > 0 && experts == 0 {
		est.Approximate = true
		est.Reasons = appendUnique(est.Reasons, "n_cpu_moe is not modeled for this model")
	}
	est.Total = est.GPUWeights + est.KVBytes + est.Overhead
	return est
}
