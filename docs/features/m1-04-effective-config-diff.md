# M1.4 — Effective-config diff

Part of [M1](../roadmap.md#milestone-m1). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** S · **Depends** M1.1

**Status** shipped

**Problem.** A preset inherits from defaults and its model. Users cannot see
what the preset actually changes.

**Proposal.** `wool config diff TARGET [--against defaults|model]` and a TUI
`D` overlay show only fields whose resolved value differs from the comparison
layer, with the source of each side.

**Acceptance.**

- [x] Diff contains only changed fields.
- [x] Sampling fields are compared individually.
- [x] `--json` is stable and includes both values and both sources.
- [x] No diff output when nothing differs.

**Implementation notes.** `config.Diff` resolves the target and a comparison
layer and returns fields in the same canonical order as `config explain`. A
preset defaults to its model, so the result is what the preset changes; a model
defaults to the defaults layer. Each field carries the value, source and exact
TOML layer for both sides, so sampling keys are compared one at a time. The CLI
prints a table and the TUI `D` overlay renders the same result; both stay quiet
when the list is empty. JSON always includes the target, kind, model, comparison
layer and a `fields` array (empty when nothing differs).

**M1 exit criteria:** provenance and reset work for every editor field; the
estimate is decomposed in the UI and JSON; at least one observed measurement is
recorded and displayed; the diff view works; all tests pass.
