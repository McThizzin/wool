# M3.3 — A/B comparison

Part of [M3](../roadmap.md#milestone-m3). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** M · **Depends** M3.1, M3.2

**Status** shipped

**Problem.** “Did changing cache type help?” needs a controlled comparison, not
two numbers from different days.

**Proposal.** `wool bench compare A B --runs 3` interleaves the presets, uses
the same prompt and seed, and reports a side-by-side table with median, spread
and percentage delta. The TUI shows the same comparison.

```text
wool bench compare A B [--runs 3] [--pp 512] [--tg 128] [--json]
wool bench compare A B --engine llama-bench
```

- One round measures one target. `--runs N` means N rounds per target, so the
  command performs `2N` measured completions.
- Rounds alternate order (`A B B A A B …`) so neither target always runs on the
  hotter machine. Both targets use the same prompt, seed and generation length.
- Every server round starts a fresh ephemeral `llama-server`, so the two models
  never occupy VRAM together. Each side is aggregated into one normal
  `kind: "bench"` record and appended to the measurement store.
- The report shows each side's median, relative spread `(max-min)/median`, the
  percentage delta `B` versus `A`, raw generation runs and any warnings.
- A side warns when prompt or generation spread exceeds 10%, and the command
  warns when the GPU, GPU driver or `llama-server` version changed between
  sides.
- `--json` prints the stable `bench.Comparison` schema (version 1).

The TUI mirrors the CLI. `/bench compare A B` opens the newest recorded run for
each target in the same side-by-side overlay, including spread, raw runs and
warnings. Marking a run with space and pressing enter still compares two stored
runs.

**Acceptance.**

- [x] Runs are interleaved to reduce thermal drift.
- [x] Both targets use identical prompt, seed and generation length.
- [x] The report shows raw runs and a noise warning when spread is high.
- [x] `--json` is stable.
- [x] The command warns if hardware or `llama-server` version changed.

**Implementation notes.** The shared comparison model lives in
`internal/bench/compare.go` (`ComparisonVersion = 1`, `NoiseThreshold = 0.10`).
`bench compare` reuses the regular bench parser but rejects `--url`/`--model`
and requires exactly two different config targets. Each round is one measured
completion; the side's load is the median across rounds and its VRAM comes from
the round with the largest known delta so the stored record stays conservative.
The TUI builds sides with `bench.SideFromRecord`, so a comparison works from
stored records even when the CLI is not running.

**Risk.** Benchmarks are noisy. The spread and warnings are a coarse guard, not
a significance test. Short runs on a shared machine can still mislead; repeat
with more rounds before trusting a small delta.
