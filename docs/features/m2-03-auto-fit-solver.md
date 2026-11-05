# M2.3 — Auto-fit solver

Part of [M2](../roadmap.md#milestone-m2). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** M/L · **Depends** M1.2, M2.1, M2.2

**Status** shipped (v0.3)

**Problem.** Given a VRAM budget, users guess at context, cache type and layer
offload. The estimator knows the cost function; it should solve the inverse.

**Proposal.** Add a solver and expose it in the CLI and editor.

```text
wool fit TARGET --vram 7G [--json]
wool fit TARGET --vram 7G --cache-type q8_0 --gpu-layers 32
```

- Binary-search the largest context that fits, capped at the model’s native
  context.
- If no useful context fits, try lower-precision cache types, more CPU offload
  (including `n_cpu_moe` for MoE models), or report that the quant is too large.
- Return a ranked recommendation with weights, KV, overhead, headroom and the
  limiting term.
- In the editor, `F` opens a fit overlay and applies the result to the draft,
  not the file, until the user saves.

Saving a recommendation as a preset (`--save-preset`) is deferred to
[M2.4](m2-04-save-a-recommendation-as-a-preset.md).

**Scope.**

- In: a deterministic solver over the M2.1/M2.2 estimate, a CLI text and JSON
  surface, a TUI picker that edits an in-memory draft, and fallback ranking for
  cache precision and CPU offload.
- Out: writing presets from a recommendation (M2.4), calibration from observed
  runs (M3.4), multi-GPU splits (PLAT-2), and exact allocator accounting.

**Implementation notes.**

- `internal/fit.Solve` is the single entry point. It takes the GGUF metadata and
  file size, the resolved launch settings, and a `gguf.Budget`, then returns a
  `Result` with one `Recommendation` and up to five ranked alternatives.
- Context is searched on a 256-token grid. `2048` tokens is the minimum useful
  context; `native_context` is the cap. Every recommendation satisfies
  `estimate.total <= budget.available`; the solver never returns an over-budget
  estimate as a recommendation.
- The requested settings are tried first. Only when they cannot hold the
  minimum context does the solver step down through cache precision, then CPU
  offload, then a combined fallback. For MoE models, `n_cpu_moe` is preferred
  over reducing `gpu_layers`; all-CPU placement is a last resort.
- Results are ranked deterministically: requested settings before fallbacks;
  larger context first; then more GPU work, less expert offload, and a less
  compressed cache at the same context.
- `--vram SIZE` is the total card size. The solver subtracts
  `router.memory_margin_mib` and deliberately ignores live other usage, so
  repeated runs are deterministic. Without `--vram`, the budget comes from live
  GPU telemetry and excludes a running standalone server's own allocation.
- Errors are actionable and distinguish `ErrNoBudget`, `ErrNoMetadata` and
  `ErrNoFit`: missing GPU, unreadable or incomplete GGUF metadata, a reserve
  larger than the card, a native context below the minimum, and a quant that
  cannot hold 2048 tokens each say what to change.

**What shipped.**

- `internal/fit` contains the solver, ranking, fallback tiers and size parser,
  with unit tests for dense, hybrid, sliding-window/shared-KV and MoE models,
  cache fallback, CPU/MoE offload, tiny and unknown budgets, metadata errors,
  deterministic output and the stable JSON shape.
- `wool fit TARGET` accepts `--vram`, `--cache-type`, `--gpu-layers`,
  `--n-cpu-moe` and `--json`. Text output shows the budget, requested settings,
  each recommendation's weights/KV/overhead total, headroom, limiting term,
  changes and notes. JSON exposes the same data under a stable schema:
  `target`, `kind`, `model`, `path`, `budget`, `native_context`,
  `minimum_context`, `requested`, `recommendation`, `alternatives`,
  `approximate` and `notes`.
- The TUI editor opens the same solver with `F` or `/fit`. The picker lists the
  recommendation and alternatives; `enter` applies the selected settings to the
  editor draft, `j`/`k` move, and `esc` closes. Nothing is written to TOML until
  the user presses `ctrl+s` in the editor, which writes only the changed
  fit-controlled options (`ctx`, `gpu_layers`, `n_cpu_moe`, `cache_type_k`,
  `cache_type_v`). `esc` discards the draft. No preset is created.
- The solver uses the existing `gguf.EstimateVRAM` cost model, so approximate
  models stay approximate and the notes explain why.
- README, `docs/DESIGN.md`, the roadmap and the feature index document the
  command, the budget semantics and the editor flow.

**Validation.** Manual checks on the reference config:

```text
wool fit lfm25-24b-a2b --vram 8G --json
wool fit lfm3-1-7b --vram 8G
```

The MoE target returns `n_cpu_moe` fallbacks when the full expert set does not
fit, and the dense target caps at its native context. Both results stayed
within the derived budget. The unit tests use synthetic dense, hybrid and MoE
metadata so the suite does not depend on local model files.

**Acceptance.**

- [x] The solver never returns an estimate above the budget.
- [x] It respects the native context and the configured reserve.
- [x] Output is deterministic and has a stable `--json` schema.
- [x] It explains the limiting term and at least one alternative.
- [x] It handles no GPU, unknown metadata, tiny budgets and missing model files
      with actionable errors.
- [x] Unit tests cover dense, hybrid and MoE models.

**Risk.** The estimate is approximate, so the recommendation is only as good
as the model. Present it as a starting point with headroom, and let M3
calibration improve it.
