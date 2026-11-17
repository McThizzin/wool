package llamaserver

type CompletionRequest struct {
	Prompt      string  `json:"prompt"`
	NPredict    int     `json:"n_predict"`
	Temperature float64 `json:"temperature"`
	Seed        int     `json:"seed"`
	CachePrompt bool    `json:"cache_prompt"`
	Stream      bool    `json:"stream"`
}

// CompletionTimings is llama-server's own timing block. It is the most
// accurate source because it excludes HTTP overhead.
type CompletionTimings struct {
	PromptN            int     `json:"prompt_n"`
	PromptMS           float64 `json:"prompt_ms"`
	PromptPerSecond    float64 `json:"prompt_per_second"`
	PredictedN         int     `json:"predicted_n"`
	PredictedMS        float64 `json:"predicted_ms"`
	PredictedPerSecond float64 `json:"predicted_per_second"`
}

// Completion is the non-streaming /completion response.
type Completion struct {
	Content         string             `json:"content"`
	Stop            bool               `json:"stop"`
	TokensPredicted int                `json:"tokens_predicted"`
	TokensEvaluated int                `json:"tokens_evaluated"`
	Timings         *CompletionTimings `json:"timings"`
}

// ModelStatus is the router's per-model status block.
type ModelStatus struct {
	Value    string   `json:"value"`
	Args     []string `json:"args"`
	Preset   string   `json:"preset"`
	ExitCode *int     `json:"exit_code"`
	Failed   bool     `json:"failed"`
}

// Model is one entry from GET /models.
type Model struct {
	ID      string       `json:"id"`
	Aliases []string     `json:"aliases"`
	Tags    []string     `json:"tags"`
	Object  string       `json:"object"`
	OwnedBy string       `json:"owned_by"`
	Created int64        `json:"created"`
	Status  ModelStatus  `json:"status"`
	Memory  *ModelMemory `json:"memory,omitempty"`
}

// ModelMemory is the router's memory report for one model. The router only
// includes it when it was started with a positive --models-memory-margin.
type ModelMemory struct {
	Status    string            `json:"status"`
	Min       *ModelMemoryUsage `json:"min"`
	Max       *ModelMemoryUsage `json:"max"`
	Effective *ModelMemoryUsage `json:"effective"`
}

// ModelMemoryUsage is one measurement at a context size.
type ModelMemoryUsage struct {
	NCtx    uint32              `json:"n_ctx"`
	Devices []ModelMemoryDevice `json:"devices"`
}

// ModelMemoryDevice is the memory a model holds by buffer kind. Total and Free
// are set only for device entries.
type ModelMemoryDevice struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Total   uint64 `json:"total"`
	Free    uint64 `json:"free"`
	Model   uint64 `json:"model"`
	Context uint64 `json:"context"`
	Compute uint64 `json:"compute"`
}

// IsDevice reports whether the entry describes device memory.
func (d ModelMemoryDevice) IsDevice() bool { return d.Kind == "device" }

// Self is the memory the model holds for this entry.
func (d ModelMemoryDevice) Self() uint64 { return d.Model + d.Context + d.Compute }

// SelfBytes returns the bytes the model holds across its devices. Multi-device
// measurements return false because the router applies the margin per device,
// which this single total cannot represent.
func (u *ModelMemoryUsage) SelfBytes() (uint64, bool) {
	if u == nil {
		return 0, false
	}
	var total uint64
	devices := 0
	for _, d := range u.Devices {
		if !d.IsDevice() || d.Total == 0 {
			continue
		}
		total += d.Self()
		devices++
	}
	if devices != 1 {
		return 0, false
	}
	return total, true
}

// Routed reports whether the model has been loaded at least once.
func (m Model) Routed() bool {
	return m.Status.Value == "loaded" || m.Status.Value == "sleeping" || len(m.Status.Args) > 0
}

// Slot is one entry from /slots.
type Slot struct {
	ID               int  `json:"id"`
	NCtx             int  `json:"n_ctx"`
	NPast            int  `json:"n_past"`
	NPromptTokens    int  `json:"n_prompt_tokens"`
	NGeneratedTokens int  `json:"n_generated_tokens"`
	IsProcessing     bool `json:"is_processing"`
	Speculative      bool `json:"speculative"`
}

// Metrics holds the counters exposed by /metrics. Fields absent from the
// response remain zero.
type Metrics struct {
	PromptTokens       float64
	PromptTokensCached float64
	PromptSeconds      float64
	PredictedTokens    float64
	PredictedSeconds   float64
	NDecode            float64
	NTokensMax         float64
	SpecDraftTokens    float64
	SpecAcceptedTokens float64
	RequestsProcessing float64
	RequestsDeferred   float64
	BusySlotsPerDecode float64
	Raw                map[string]float64
}

// PromptTPS is prompt tokens per second, averaged over the server lifetime.
func (m Metrics) PromptTPS() float64 {
	if m.PromptSeconds <= 0 {
		return 0
	}
	return m.PromptTokens / m.PromptSeconds
}

// PredictedTPS is generated tokens per second.
func (m Metrics) PredictedTPS() float64 {
	if m.PredictedSeconds <= 0 {
		return 0
	}
	return m.PredictedTokens / m.PredictedSeconds
}

// SpecAcceptRate is accepted/draft tokens for speculative decoding.
func (m Metrics) SpecAcceptRate() float64 {
	if m.SpecDraftTokens <= 0 {
		return 0
	}
	return m.SpecAcceptedTokens / m.SpecDraftTokens * 100
}

// Health calls GET /health.
