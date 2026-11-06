# M2.4 — Save a recommendation as a preset

Part of [M2](../roadmap.md#milestone-m2). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** S · **Depends** M2.3

**Status** shipped (v0.3)

**Problem.** A recommendation is useful only if it can become a reproducible
preset without retyping values.

**Proposal.** `wool fit --save-preset NAME` writes a new `[presets.NAME]`
section with the model reference and the recommended overrides. Refuse to
overwrite without `--force`. The TUI offers the same action after a fit.

**Scope.**

- In: saving the primary recommendation as a new preset from the CLI or the fit
  picker, explicit replacement of an existing preset, and preserving the rest
  of the TOML file.
- Out: cloning a preset without a fit (M5.5), previewing a saved preset's diff
  before writing, and launching the saved preset automatically.

**Implementation notes.**

- `config.SavePreset` validates the preset ID, checks that the model exists,
  refuses an existing name unless `force` is set, and writes `model`, `ctx`,
  `gpu_layers`, `n_cpu_moe`, `cache_type_k` and `cache_type_v`. Those values are
  enough to reproduce the recommendation through the normal inheritance layers.
- `config.SetValues` writes several keys in one atomic file replacement. It
  preserves comments, ordering and unrelated sections; an existing preset is
  updated in place rather than appended as a duplicate table.
- The CLI accepts `--save-preset NAME` and `--force`. Saving happens before
  output, so a failure returns non-zero without claiming success. With
  `--json`, the result includes an optional `saved_preset` field; the text
  output prints the saved name.
- In the fit picker, `p` opens a preset-name prompt pre-filled with
  `<model>-<ctx>`. `enter` saves, an existing name asks for `P` to replace, and
  `esc` cancels. Saving reloads the config so the new preset appears
  immediately; it does not close the editor or launch anything.
- Replacing a preset updates only the fit-controlled keys. Sampling, extras and
  other user options in that section are left alone.

**What shipped.**

- `internal/config/preset.go` with `SavePreset`, `PresetFit` and the
  `ErrPresetExists` sentinel.
- `internal/config/edit.go` now has `SetValues`; `SetValue` is its
  single-key wrapper, so both paths share the same comment-preserving writer.
- `wool fit` gained `--save-preset NAME` and `--force`. `fit.Result` gained an
  optional `saved_preset` JSON field.
- The TUI fit overlay gained the `p` name prompt and explicit `P` replacement
  path.
- Tests cover multi-key writes, comment preservation, saving and resolving a
  preset, refusing an overwrite without force, replacing with force, CLI flag
  parsing, and the TUI save/replace flow.

**Acceptance.**

- [x] The new preset resolves and runs.
- [x] Existing comments and sections are preserved.
- [x] `--force` is required to replace an existing preset.
- [x] The preset records enough values to reproduce the recommendation.

**M2 exit criteria:** `wool fit` works for dense and MoE models, the editor can
preview and apply a recommendation, and a saved preset launches. All M2
features are now shipped.
