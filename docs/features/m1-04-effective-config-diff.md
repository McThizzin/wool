# M1.4 — Effective-config diff

Part of [M1](../roadmap.md#milestone-m1). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** S · **Depends** M1.1

**Status** planned

**Problem.** A preset inherits from defaults and its model. Users cannot see
what the preset actually changes.

**Proposal.** `wool config diff TARGET [--against defaults|model]` and a TUI
`D` overlay show only fields whose resolved value differs from the comparison
layer, with the source of each side.

**Acceptance.**

- [ ] Diff contains only changed fields.
- [ ] Sampling fields are compared individually.
- [ ] `--json` is stable and includes both values and both sources.
- [ ] No diff output when nothing differs.

**M1 exit criteria:** provenance and reset work for every editor field; the
estimate is decomposed in the UI and JSON; at least one observed measurement is
recorded and displayed; the diff view works; all tests pass.
