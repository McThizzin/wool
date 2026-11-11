# M4.1 — Live inference panel

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** M · **Depends** none

**Status** done

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

**Implementation.** The Inference tab (key `5` or `i`) shows all of the above
plus lifetime averages and a per-slot KV fill table. The source is the
standalone server when alive, otherwise the selected router model; `m` cycles
between the live sources and `j`/`k` picks another router model. Rates prefer
the server's own busy seconds (`prompt_seconds_total`,
`tokens_predicted_seconds_total`) so idle time is not counted against
throughput, with wall clock as the fallback. A scrape that fails — including a
404 from a server started with `metrics = false` — leaves the source marked
`no metrics`; the panel shows the reason and the restart hint instead of
numbers. A router-model scrape that raced a selection move is discarded rather
than attributed to the newly selected model. The same tracker backs the
`wool stats [MODEL] [--window D] [--json]` CLI command for scripts, which
prints the stable `statsSnapshot` (version 1) and exits non-zero when metrics
are unavailable.

**Acceptance.**

- [x] The panel updates at the configured poll interval.
- [x] HTTP timeouts cannot freeze the UI.
- [x] Metrics-disabled servers show a clear message, not zeros presented as
      facts.
- [x] Counter resets are handled.
- [x] Tests use fake metrics and slots.
