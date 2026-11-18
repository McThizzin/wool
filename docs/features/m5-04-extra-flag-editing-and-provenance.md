# M5.4 — Extra-flag editing and provenance

Part of [M5](../roadmap.md#milestone-m5). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** S/M · **Depends** M1.1

**Status** shipped

**Problem.** `[models.<id>.extra]` and `[presets.<name>.extra]` already turn
keys into `llama-server` flags, but the editor can only change its fixed field
list. A flag outside that list requires hand-editing TOML, inherited extras are
invisible, and `config explain` and `config diff` ignore them. Users cannot see
which layer supplied an extra or whether it overrides a first-class option.

**Proposal.**

- Treat extras as first-class resolved options for provenance, `config
  explain`, `config diff` and the editor.
- Add an extras section to the editor. It lists resolved extras sorted by flag
  name and marks each with its source layer.
- Add a flag that is not currently used, edit its value, convert it to a bare
  flag, or reset an override so it falls back to a lower layer.
- Store flag names without leading dashes for consistency; accept input with
  or without them. An empty value emits a bare flag.
- Warn when an extra duplicates a first-class option (`ctx` vs `--ctx-size`,
  `gpu_layers` vs `--gpu-layers`) because extras are appended last and win.
- Note that arbitrary extras are not modelled by the VRAM estimate. Do not
  imply the fit preview accounts for them.
- Keep the existing map representation: one value per flag. Repeated and
  ordered flags remain out of scope.

**Scope.**

- In: editor add/edit/remove/reset for extras, provenance and diff/explain
  support, conflict warnings, argv/INI round-trips.
- Out: descriptions, catalog data and fuzzy search (M5.5); repeated flags and
  ordered argv (Later Bets); modelling arbitrary flags in the estimator.

**Acceptance.**

- [x] A flag that has no dedicated editor field can be added to a model and a
      preset from the TUI.
- [x] The write goes to the correct `[*.extra]` table, preserves comments and
      ordering, and produces the expected standalone argv and router INI.
- [x] Editing, removing and resetting an extra works; reset removes only the
      current layer's key and falls back to an inherited value.
- [x] `config explain` and `config diff` include extras with value, source and
      exact layer.
- [x] Bare flags and flags with values round-trip correctly.
- [x] Duplicating a first-class option warns but remains an explicit user
      choice.
- [x] The editor makes it clear that arbitrary extras are outside the estimate.
- [x] Tests cover the config writer, argv/INI generation, the editor flow and
      provenance/diff.

**Implementation notes.** Resolved extras use normalized flag names and appear
as `extra.<flag>` fields in provenance, `config explain`, `config diff` and the
editor. The editor's add prompt accepts `name` for a bare flag or
`name=value` for a value; `backspace`, `delete` and `x` remove the current
layer's key. Conflicts warn in the TUI and in command output but never block the
write. Generated INI records a bare extra as `flag = true` and puts inherited
defaults extras only in `[*]`.

**Risk.** Some extras change VRAM use in ways the estimator does not model.
Warn about that and keep the estimate advisory rather than blocking a launch.
