# M4.1 — Live inference panel

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** M · **Depends** none

**Status** planned

**Problem.** Hardware telemetry exists, but inference-level health is buried in
router metrics or absent for standalone servers.

**Proposal.** Add an Inference tab (or a Dashboard section) with:

- prompt and generation tokens/s;
- requests processing and deferred;
- slot occupancy and KV cache fill (`n_past / n_ctx`);
- speculative decoding acceptance rate;
- sparklines over the same history window as the hardware graphs.

Use `/metrics` and `/slots` for both standalone and the selected router model.
Compute rates from counter deltas and reset the baseline when a counter
decreases (server restart).

**Acceptance.**

- [ ] The panel updates at the configured poll interval.
- [ ] HTTP timeouts cannot freeze the UI.
- [ ] Metrics-disabled servers show a clear message, not zeros presented as
      facts.
- [ ] Counter resets are handled.
- [ ] Tests use fake metrics and slots.
