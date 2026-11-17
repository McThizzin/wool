package fit

import (
	"fmt"
	"strings"

	"wool/internal/gguf"
)

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
