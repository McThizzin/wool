# M3.3 — A/B comparison

Part of [M3](../roadmap.md#milestone-m3). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** M · **Depends** M3.1, M3.2

**Status** planned

**Problem.** “Did changing cache type help?” needs a controlled comparison, not
two numbers from different days.

**Proposal.** `wool bench compare A B --runs 3` interleaves the presets, uses
the same prompt and seed, and reports a side-by-side table with median, spread
and percentage delta. The TUI shows the same comparison.

**Acceptance.**

- [ ] Runs are interleaved to reduce thermal drift.
- [ ] Both targets use identical prompt, seed and generation length.
- [ ] The report shows raw runs and a noise warning when spread is high.
- [ ] `--json` is stable.
- [ ] The command warns if hardware or `llama-server` version changed.
