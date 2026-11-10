# M1.3 — Observed-vs-estimate capture

Part of [M1](../roadmap.md#milestone-m1). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** S/M · **Depends** none

**Status** shipped

**Problem.** The estimate is a prediction that is never checked. Users cannot
tell whether it is conservative by 5% or 40%, and later calibration has no data
to learn from.

**Proposal.** Record one measurement per launch in an append-only store, then
show it next to the estimate.

- Add `internal/measure` with a versioned JSONL store under
  `$XDG_STATE_HOME/wool/measurements/runs.jsonl`.
- Before launch, sample VRAM. After `/health` succeeds, sample again after a
  short settle window (for example 5s and 15s) and record the delta.
- Record: timestamp, model ID and identity, file size/mtime, architecture,
  ctx, cache types, gpu layers, `n_cpu_moe`, GPU PCI ID, driver and
  `llama-server --version`, estimate bytes, baseline used, observed used,
  sample count, and whether the baseline was known.
- Dashboard and Doctor show `estimate 6.1 GiB · observed 6.4 GiB (n=3)`.
- The raw observation is the source for the M3.4 calibration correction; the
  capture itself does not apply one.

**Implementation notes.** sysfs VRAM includes every process on the card, so
subtract a pre-launch baseline and store it. If the baseline is unavailable,
record `baseline_known: false` and do not present the delta as truth. Sample
after mmap pages have been faulted; a reading immediately after health can be
low.

`internal/measure` owns the store and the capture sequence. `wool run` records
one observation after the first `/health` success, sampling at 5s and 15s and
using the last sample for the delta. The TUI launches the same capture as a
background command, so the 15s window never blocks input; restarts re-sample the
baseline after the old process stops. Records are matched by model, context,
cache types, GPU layers and `n_cpu_moe`, so an observation from a different
launch shape is not shown next to the current estimate.

**Acceptance.**

- [x] A successful `wool run` writes exactly one measurement record.
- [x] The record survives restarts and is readable by the TUI and CLI.
- [x] A corrupt JSONL line is skipped, not fatal.
- [x] Doctor shows the most recent observation and the sample count.
- [x] Tests use a fake sysfs root and a temporary state directory.

**Risk.** Background GPU users and driver behavior make the observation noisy.
Label it “observed delta”, never “actual usage”, and keep the raw samples.
