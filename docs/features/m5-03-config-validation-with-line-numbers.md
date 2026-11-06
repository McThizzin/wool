# M5.3 — Config validation with line numbers

Part of [M5](../roadmap.md#milestone-m5). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** S/M · **Depends** none

**Status** planned

**Problem.** A TOML error fails the whole config with a parser message. The TUI
cannot show where the problem is, and typos in keys are silently ignored.

**Proposal.**

- Return a structured `config.Error` with file, line, column, key and message
  from `go-toml`’s `DecodeError.Position`.
- Print `path:line:col: message` in the CLI.
- Show the same in a TUI overlay.
- Add `--strict` using `toml.Decoder.DisallowUnknownFields()` to catch typos,
  with `[*.extra]` and map fields still allowed. Flag-name, alias and value
  validation against the flag catalog belongs to M5.5.

**Acceptance.**

- [ ] Malformed TOML reports a line and column.
- [ ] The TUI shows the error without crashing.
- [ ] `--strict` catches an unknown key and names it.
- [ ] `[*.extra]` and other map fields remain allowed in strict mode.
- [ ] Non-strict mode keeps loading existing configs.
- [ ] Tests cover both modes.
