# M1.1 — Override provenance and reset-to-inherited

Part of [M1](../roadmap.md#milestone-m1). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P0 · **Size** M · **Depends** none

**Status** shipped

**Problem.** The editor marks a value as inherited or not, but it cannot say
which layer supplied it, and there is no way to remove an override except
editing the TOML by hand. That makes the inheritance chain hard to reason about
and makes every edit permanent.

**Proposal.** Track the source of each resolved field and add a reset action.

- `config.ResolveDetailed` returns `Resolved` plus a source map:
  `built-in`, `[defaults]`, `models.<id>`, `presets.<name>`.
- The editor shows the source next to each field and binds `backspace`/`delete`
  to “remove this override”.
- `wool config explain TARGET [--json]` prints the same table.
- `config.RemoveValue(path, table, key)` removes one scalar key while
  preserving comments and ordering. Leave an empty table header rather than
  guessing which comments to delete.

**Implementation notes.** `ApplyDefaults` currently fills `c.Defaults` in
place, which loses the difference between a built-in default and an explicit
`[defaults]` key. Capture the explicitly present `[defaults]` keys before
applying defaults, or move built-in defaults into a separate layer. Extend
`mergeOptions` to record a source per field rather than inferring it later.

**Acceptance.**

- [x] Every editor field shows `built-in`, `defaults`, `model` or `preset`.
- [x] Reset removes only the selected key and the config still loads.
- [x] Reset works for nested `[*.sampling]` keys.
- [x] Comments and unrelated keys are byte-identical after reset.
- [x] `wool config explain --json` includes `value`, `source` and `layer`.
- [x] Tests cover removing the last key in a table and removing a key that is
      not present.

**Risk.** A line-based remover can corrupt multi-line values. Restrict it to
the scalar keys the editor exposes; reject anything else.
