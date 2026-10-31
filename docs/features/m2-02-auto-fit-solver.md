# M2.2 — Auto-fit solver

Part of [M2](../roadmap.md#milestone-m2). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** M/L · **Depends** M1.2, M2.1

**Status** planned

**Problem.** Given a VRAM budget, users guess at context, cache type and layer
offload. The estimator knows the cost function; it should solve the inverse.

**Proposal.** Add a solver and expose it in the CLI and editor.

```text
wool fit TARGET --vram 7G [--json]
wool fit TARGET --vram 7G --cache-type q8_0 --gpu-layers 32
wool fit TARGET --vram 7G --save-preset NAME
```

- Binary-search the largest context that fits, capped at the model’s native
  context.
- If no useful context fits, try lower-precision cache types, more CPU offload
  (including `n_cpu_moe` for MoE models), or report that the quant is too large.
- Return a ranked recommendation with weights, KV, overhead, headroom and the
  limiting term.
- In the editor, `F` opens a fit overlay and applies the result to the draft,
  not the file, until the user saves.

**Acceptance.**

- [ ] The solver never returns an estimate above the budget.
- [ ] It respects the native context and the configured reserve.
- [ ] Output is deterministic and has a stable `--json` schema.
- [ ] It explains the limiting term and at least one alternative.
- [ ] It handles no GPU, unknown metadata, tiny budgets and missing model files
      with actionable errors.
- [ ] Unit tests cover dense, hybrid and MoE models.

**Risk.** The estimate is approximate, so the recommendation is only as good
as the model. Present it as a starting point with headroom, and let M3
calibration improve it.
