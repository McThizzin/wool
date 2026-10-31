# M3.4 — Calibration correction

Part of [M3](../roadmap.md#milestone-m3). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** M · **Depends** M1.3, M3.1

**Status** planned

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

- [ ] No correction is applied below three samples.
- [ ] The UI always shows the raw estimate and the sample count.
- [ ] Changing the model file invalidates old samples.
- [ ] The clamp prevents a single bad observation from dominating.
- [ ] Tests cover convergence, outliers, expiry and clamping.

**Risk.** Observed VRAM is confounded by other processes and sampling lag.
Store the baseline, keep the raw data, and treat calibration as a hint, not a
measurement.
