# M4.5 — Router eviction visualization

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** M · **Depends** M2.1

**Status** shipped

**Problem.** With `models_max` and a memory margin, loading a new model can
evict one the user still needs. The router does not make the choice obvious in
the TUI.

**Proposal.** Show resident models, the limit, the margin and a best-effort
prediction of which model the next load would evict, with the estimate behind
the prediction. Label it as predicted unless the router exposes a definitive
policy.

**Acceptance.**

- [x] Shows resident count, limit, margin and candidate.
- [x] The prediction is labeled and never presented as a guarantee.
- [x] Missing estimates degrade to “unknown”.
- [x] Tests cover at-limit, over-limit and empty states.

**Implementation notes.**

- The Router tab starts with `resident N/limit · margin N MiB · est SIZE`.
  `est` is the sum of wool's memory estimates for the resident models, or
  `unknown` when any estimate is unavailable. An over-limit resident set is
  marked and rendered in the warning style.
- The second line predicts the effect of loading the selected model:
  `predicted eviction: A, B (LRU approx.) if loading X (SIZE)`,
  `predicted eviction: none if loading X (SIZE)`, or
  `predicted eviction: unknown (estimate missing) if loading X`. A resident
  selection says so instead.
- The pinned llama.cpp router evicts idle models in LRU order when
  `models_max` would be exceeded. `GET /models` does not expose `last_used`, so
  the TUI approximates the order from when each model was first seen resident;
  the victim list is always labeled `LRU approx.` and is not a guarantee.
- Busy models are never evicted by the router. The API does not expose
  per-model request counts, so the prediction cannot account for a busy model;
  the router waits for it to go idle instead.
- The memory margin is shown as configured. The pinned router limits eviction
  by model count, not by the margin; `--fit-target` uses the margin when sizing
  each child.
- The selected model's estimate comes from the same `fitFor` cache as the
  Models and Presets tabs, so a missing GGUF or an unknown architecture
  degrades to `unknown` rather than failing the tab.
- Loading/unloading through the TUI or the router API updates the resident set
  on the next poll, which resets the load-time approximation for that model.
