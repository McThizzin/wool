// Package fit solves the inverse of the VRAM estimate: given a budget, it
// finds the largest context that fits and proposes fallbacks when the target
// settings cannot hold a useful context.
package fit

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"wool/internal/gguf"
)

const (
	// DefaultMinContext is the smallest context the solver treats as useful.
	DefaultMinContext = 2048
	// ContextStep keeps recommendations on a stable, readable grid.
	ContextStep = 256
)

// Fallback values explain why a recommendation differs from the requested
// settings. The empty string means the requested settings were kept.
const (
	FallbackNone         = ""
	FallbackCache        = "cache"
	FallbackOffload      = "offload"
	FallbackCacheOffload = "cache+offload"
)

// Errors reported by Solve. Callers should use errors.Is to distinguish them.
var (
	ErrNoBudget   = errors.New("VRAM budget is unknown")
	ErrNoMetadata = errors.New("model metadata cannot drive a fit")
	ErrNoFit      = errors.New("no useful context fits the VRAM budget")
)

// Settings are the launch options the solver can change.
type Settings struct {
	Ctx        int    `json:"ctx"`
	GPULayers  int    `json:"gpu_layers"`
	NCPUMoE    int    `json:"n_cpu_moe"`
	CacheTypeK string `json:"cache_type_k"`
	CacheTypeV string `json:"cache_type_v"`
}

// Request describes one fit query. FileBytes and Meta must come from the
// target GGUF; Settings is the resolved launch configuration.
type Request struct {
	ModelID       string
	ModelPath     string
	Meta          gguf.Meta
	FileBytes     uint64
	Budget        gguf.Budget
	Settings      Settings
	MinContext    int // zero uses DefaultMinContext
	NativeContext int // zero uses Meta.CtxTrain
}

// Recommendation is one fitting setting. All returned recommendations have
// Estimate.Total <= Budget.Available.
type Recommendation struct {
	Rank       int           `json:"rank"`
	Ctx        int           `json:"ctx"`
	GPULayers  int           `json:"gpu_layers"`
	NCPUMoE    int           `json:"n_cpu_moe"`
	CacheTypeK string        `json:"cache_type_k"`
	CacheTypeV string        `json:"cache_type_v"`
	Estimate   gguf.Estimate `json:"estimate"`
	Headroom   int64         `json:"headroom_bytes"`
	Limiting   string        `json:"limiting_term"`
	Fallback   string        `json:"fallback,omitempty"`
	Fits       bool          `json:"fits"`
	Changes    []string      `json:"changes,omitempty"`
}

// Result is the stable JSON shape returned by the CLI.
type Result struct {
	Target         string           `json:"target,omitempty"`
	Kind           string           `json:"kind,omitempty"`
	ModelID        string           `json:"model"`
	ModelPath      string           `json:"path"`
	Budget         gguf.Budget      `json:"budget"`
	NativeContext  int              `json:"native_context"`
	MinimumContext int              `json:"minimum_context"`
	Requested      Settings         `json:"requested"`
	Recommendation Recommendation   `json:"recommendation"`
	Alternatives   []Recommendation `json:"alternatives"`
	Approximate    bool             `json:"approximate"`
	Notes          []string         `json:"notes,omitempty"`
}

type tier int

const (
	tierRequested tier = iota
	tierCache
	tierOffload
	tierCombined
)

func (t tier) fallback() string {
	switch t {
	case tierCache:
		return FallbackCache
	case tierOffload:
		return FallbackOffload
	case tierCombined:
		return FallbackCacheOffload
	default:
		return FallbackNone
	}
}

type candidate struct {
	rec  Recommendation
	tier tier
}

type solver struct {
	req      Request
	settings Settings
	native   int
	min      int
	lastGrid int
}

// Solve finds the largest context that fits the budget. It returns ErrNoFit
// when neither the requested settings nor any fallback can hold MinContext.
// Every recommendation in a successful result is within the budget.
func Solve(req Request) (Result, error) {
	s, err := newSolver(req)
	if err != nil {
		return Result{}, err
	}
	return s.solve()
}

func newSolver(req Request) (*solver, error) {
	if req.ModelID == "" {
		req.ModelID = "(unknown)"
	}
	native := req.NativeContext
	if native <= 0 {
		native = req.Meta.CtxTrain
	}
	if native <= 0 {
		return nil, fmt.Errorf("%w: %s does not declare a trained context length", ErrNoMetadata, req.ModelID)
	}
	if req.Meta.BlockCount <= 0 {
		return nil, fmt.Errorf("%w: %s does not declare a transformer block count", ErrNoMetadata, req.ModelID)
	}
	if req.Meta.HeadDim() <= 0 && (req.Meta.KeyLength <= 0 || req.Meta.ValueLength <= 0) {
		return nil, fmt.Errorf("%w: %s is missing attention key/value dimensions", ErrNoMetadata, req.ModelID)
	}
	if !req.Budget.Known || req.Budget.Total == 0 {
		return nil, fmt.Errorf("%w; pass --vram SIZE or run on a GPU with readable VRAM", ErrNoBudget)
	}
	if req.Budget.Available == 0 {
		return nil, fmt.Errorf("%w: %s total leaves nothing after the %s reserve; lower router.memory_margin_mib or pass a larger --vram",
			ErrNoBudget, bytes(req.Budget.Total), bytes(req.Budget.Reserve))
	}

	min := req.MinContext
	if min <= 0 {
		min = DefaultMinContext
	}
	if min > native {
		return nil, fmt.Errorf("%w: %s has a native context of %d tokens, below the %d-token minimum",
			ErrNoMetadata, req.ModelID, native, min)
	}

	settings, err := normalizeSettings(req.Meta, req.Settings)
	if err != nil {
		return nil, err
	}
	if settings.Ctx <= 0 {
		settings.Ctx = native
	}
	req.Settings = settings
	req.NativeContext = native

	return &solver{
		req:      req,
		settings: settings,
		native:   native,
		min:      min,
		lastGrid: (native / ContextStep) * ContextStep,
	}, nil
}

func (s *solver) solve() (Result, error) {
	requestedMax, _, requestedOK := s.maxContext(s.settings, 1)

	candidates := s.candidates()
	if len(candidates) == 0 {
		best, _ := s.bestEffort()
		return Result{}, s.noFitError(best)
	}

	primary := choosePrimary(candidates)
	alts := alternatives(candidates, primary, 5)
	for i := range alts {
		alts[i].Rank = i + 2
	}
	primary.rec.Rank = 1

	res := Result{
		ModelID:        s.req.ModelID,
		ModelPath:      s.req.ModelPath,
		Budget:         s.req.Budget,
		NativeContext:  s.native,
		MinimumContext: s.min,
		Requested:      s.settings,
		Recommendation: primary.rec,
		Alternatives:   alts,
		Approximate:    primary.rec.Estimate.Approximate,
	}
	res.Notes = s.notes(primary, requestedMax, requestedOK)
	return res, nil
}

func (s *solver) candidates() []candidate {
	var out []candidate
	seen := map[string]bool{}
	add := func(set Settings, t tier) {
		c, ok := s.candidate(set, t)
		if !ok {
			return
		}
		key := candidateKey(c.rec)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, c)
	}

	add(s.settings, tierRequested)
	for _, set := range s.cacheSettings() {
		add(set, tierCache)
	}
	for _, set := range s.offloadSettings() {
		add(set, tierOffload)
	}
	// For MoE, all-CPU placement is a last resort because expert offload keeps
	// attention on the GPU. Only consider it when no gentler offload fits.
	if !hasTier(out, tierOffload) && s.req.Meta.ExpertCount > 0 && s.settings.GPULayers > 0 {
		set := s.settings
		set.GPULayers = 0
		add(set, tierOffload)
	}
	// Combined fallbacks are only useful when no single change finds a useful
	// context. Keeping them out of the normal alternative list avoids noise.
	if !hasTier(out, tierRequested) && !hasTier(out, tierCache) && !hasTier(out, tierOffload) {
		for _, set := range s.combinedSettings() {
			add(set, tierCombined)
		}
	}
	return out
}

func (s *solver) candidate(set Settings, t tier) (candidate, bool) {
	ctx, est, ok := s.maxContext(set, s.min)
	if !ok {
		return candidate{}, false
	}
	rec := Recommendation{
		Ctx:        ctx,
		GPULayers:  set.GPULayers,
		NCPUMoE:    set.NCPUMoE,
		CacheTypeK: set.CacheTypeK,
		CacheTypeV: set.CacheTypeV,
		Estimate:   est,
		Headroom:   s.req.Budget.Headroom(est),
		Limiting:   est.LimitingTerm(),
		Fallback:   t.fallback(),
		Fits:       est.Total <= s.req.Budget.Available,
		Changes:    changes(s.settings, set, ctx),
	}
	if !rec.Fits {
		return candidate{}, false
	}
	return candidate{rec: rec, tier: t}, true
}

// maxContext binary-searches the largest context on the ContextStep grid that
// fits, capped at the model's native context. floor is the smallest context to
// consider.
func (s *solver) maxContext(set Settings, floor int) (int, gguf.Estimate, bool) {
	if floor < 1 {
		floor = 1
	}
	first := ((floor + ContextStep - 1) / ContextStep) * ContextStep
	last := s.lastGrid
	if last < first {
		// Very small native contexts (or custom minimums) may not reach a full
		// grid step. Evaluate the native limit directly.
		if s.native >= floor && s.fits(s.native, set) {
			return s.native, s.estimate(s.native, set), true
		}
		return 0, gguf.Estimate{}, false
	}

	lo, hi := first/ContextStep, last/ContextStep
	best := -1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		ctx := mid * ContextStep
		if s.fits(ctx, set) {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if best < 0 {
		return 0, gguf.Estimate{}, false
	}
	ctx := best * ContextStep
	est := s.estimate(ctx, set)
	if est.Total > s.req.Budget.Available {
		return 0, gguf.Estimate{}, false
	}
	return ctx, est, true
}

func (s *solver) estimate(ctx int, set Settings) gguf.Estimate {
	return gguf.EstimateVRAM(s.req.Meta, s.req.FileBytes, ctx, set.GPULayers, set.NCPUMoE, set.CacheTypeK, set.CacheTypeV)
}

func (s *solver) fits(ctx int, set Settings) bool {
	return s.estimate(ctx, set).Total <= s.req.Budget.Available
}

func (s *solver) bestEffort() (Settings, gguf.Estimate) {
	best := s.settings
	est := s.estimate(s.min, best)
	for _, set := range append(s.cacheSettings(), s.offloadSettings()...) {
		e := s.estimate(s.min, set)
		if e.Total < est.Total {
			best, est = set, e
		}
	}
	for _, set := range s.combinedSettings() {
		e := s.estimate(s.min, set)
		if e.Total < est.Total {
			best, est = set, e
		}
	}
	return best, est
}

func (s *solver) noFitError(best Settings) error {
	est := s.estimate(s.min, best)
	return fmt.Errorf("%w: the smallest useful context (%d tokens) needs %s with %s, but only %s is available after the %s reserve; use a smaller quant, a smaller context, or free VRAM",
		ErrNoFit, s.min, bytes(est.Total), placement(best), bytes(s.req.Budget.Available), bytes(s.req.Budget.Reserve))
}

// cacheSettings returns symmetric cache types that are no larger than the
// requested pair and strictly smaller on at least one side.
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

func (s *solver) notes(primary candidate, requestedMax int, requestedOK bool) []string {
	var notes []string
	if primary.rec.Estimate.Approximate {
		note := "estimate is approximate"
		if len(primary.rec.Estimate.Reasons) > 0 {
			note += ": " + strings.Join(primary.rec.Estimate.Reasons, "; ")
		}
		notes = append(notes, note)
	}
	if primary.rec.Ctx == s.lastGrid {
		notes = append(notes, fmt.Sprintf("context is capped at the model's native limit (%d tokens)", s.native))
	}
	if primary.tier != tierRequested {
		if requestedOK {
			notes = append(notes, fmt.Sprintf("requested settings fit at most %d tokens, below the %d-token minimum", requestedMax, s.min))
		} else {
			notes = append(notes, "requested settings do not fit even at the smallest searched context")
		}
		notes = append(notes, "fallback: "+fallbackText(primary.tier))
	}
	if primary.rec.Ctx < s.settings.Ctx {
		notes = append(notes, fmt.Sprintf("recommended context is below the configured %d tokens", s.settings.Ctx))
	}
	return notes
}

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
func ParseSize(raw string) (uint64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, errors.New("size is empty")
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("invalid size %q", raw)
	}
	n, err := parseUint(s[:i])
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", raw, err)
	}
	suffix := strings.ToLower(strings.TrimSpace(s[i:]))
	suffix = strings.TrimSuffix(suffix, "ib")
	suffix = strings.TrimSuffix(suffix, "b")
	var mult uint64 = 1
	switch suffix {
	case "":
	case "k":
		mult = 1 << 10
	case "m":
		mult = 1 << 20
	case "g":
		mult = 1 << 30
	case "t":
		mult = 1 << 40
	default:
		return 0, fmt.Errorf("invalid size suffix %q (want B, K, M, G or T)", s[i:])
	}
	if n != 0 && mult > (^uint64(0))/n {
		return 0, fmt.Errorf("size %q is too large", raw)
	}
	v := n * mult
	if v == 0 {
		return 0, fmt.Errorf("size %q must be greater than zero", raw)
	}
	return v, nil
}

func parseUint(s string) (uint64, error) {
	var n uint64
	for _, r := range s {
		d := uint64(r - '0')
		if d > 9 {
			return 0, fmt.Errorf("not a number")
		}
		if n > (^uint64(0)-d)/10 {
			return 0, fmt.Errorf("number is too large")
		}
		n = n*10 + d
	}
	return n, nil
}

// bytes is a compact human form used in errors and notes.
func bytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
