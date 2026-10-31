# M2.3 — Save a recommendation as a preset

Part of [M2](../roadmap.md#milestone-m2). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** S · **Depends** M2.2

**Status** planned

**Problem.** A recommendation is useful only if it can become a reproducible
preset without retyping values.

**Proposal.** `wool fit --save-preset NAME` writes a new `[presets.NAME]`
section with the model reference and the recommended overrides. Refuse to
overwrite without `--force`. The TUI offers the same action after a fit.

**Acceptance.**

- [ ] The new preset resolves and runs.
- [ ] Existing comments and sections are preserved.
- [ ] `--force` is required to replace an existing preset.
- [ ] The preset records enough values to reproduce the recommendation.

**M2 exit criteria:** `wool fit` works for dense and MoE models, the editor
can preview and apply a recommendation, and a saved preset launches.
