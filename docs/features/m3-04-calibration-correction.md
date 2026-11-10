# M3.4 — Calibration correction

Part of [M3](../roadmap.md#milestone-m3). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** M · **Depends** M1.3, M3.1

**Status** shipped

**Problem.** The estimate is conservative by an unknown amount. With enough
observations, wool can learn the error per model and configuration.

**Proposal.** Compute a correction from the measurement store and show it
without hiding the raw estimate.

- Group samples by model identity, context, cache types, layer offload, GPU and
  driver/`llama-server` version.
- With at least three samples, compute the median observed/estimated ratio,
  clamp it to a safe range (for example 0.5–2.0), and show
  `estimate 6.1 GiB · corrected 6.4 GiB · observed 6.4 GiB (n=5, ±8%)`.
- Expire samples when the driver, `llama-server` version or model file changes.
- Make correction display-only by default. `wool fit --calibrated` may use it,
  and must label the result as calibrated.

**Acceptance.**

- [x] No correction is applied below three samples.
- [x] The UI always shows the raw estimate and the sample count.
- [x] Changing the model file invalidates old samples.
- [x] The clamp prevents a single bad observation from dominating.
- [x] Tests cover convergence, outliers, expiry and clamping.

**Implementation notes.** `internal/measure/calibration.go` owns the correction:
`MinCalibrationSamples = 3`, `MinCalibrationRatio = 0.5` and
`MaxCalibrationRatio = 2.0`. `Store.Calibration(shape, Identity)` filters
`kind: "observation"` records by launch shape, skips records whose baseline or
observed bytes are unknown, and expires records when the model size/modtime,
GPU, driver or `llama-server` version differs. Unknown identity fields never
expire a sample because the difference cannot be proven. The ratio is the
median `observed/estimate`; the spread is `(max-min)/median`; the corrected
value is the clamped ratio applied to the raw estimate.

The dashboard and `wool doctor` always show the raw estimate and sample count,
and add the corrected value only when `Calibration.OK()` is true. Expired-only
state is reported as such instead of silently reusing stale samples.
`wool fit --calibrated` attaches the same calibration to each recommendation,
labels the output as calibrated and prints the corrected value and headroom.
The raw estimate and solver verdict are never replaced.

**Risk.** Observed VRAM is confounded by other processes and sampling lag.
Store the baseline, keep the raw data, and treat calibration as a hint, not a
measurement.
