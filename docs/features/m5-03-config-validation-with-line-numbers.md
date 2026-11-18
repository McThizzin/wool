# M5.3 — Config validation with line numbers

Part of [M5](../roadmap.md#milestone-m5). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** S/M · **Depends** none

**Status** shipped

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

- [x] Malformed TOML reports a line and column.
- [x] The TUI shows the error without crashing.
- [x] `--strict` catches an unknown key and names it.
- [x] `[*.extra]` and other map fields remain allowed in strict mode.
- [x] Non-strict mode keeps loading existing configs.
- [x] Tests cover both modes.

**Implementation notes.**

- `config.Error` carries `File`, `Line`, `Column`, `Key`, `Message` and the
  underlying decode error. `Error()` renders `file:line:col: message` (with the
  key quoted when go-toml supplied one). `config.Errors` groups all unknown
  keys reported by strict decoding; `config.AsErrors` and `config.ErrorLines`
  let the CLI and TUI render the same text.
- `config.ParseWithOptions`/`LoadWithOptions` accept `ParseOptions{Strict: true}`.
  `Parse`/`Load` keep their non-strict behavior, so existing configs and
  callers are unaffected. Strict mode uses go-toml's
  `Decoder.DisallowUnknownFields`; map fields such as `[models.*.extra]`,
  `[presets.*.extra]` and `[themes.*.colors]` still accept arbitrary keys.
- `wool config check [--strict] [--json]` validates without starting servers.
  Text output goes to stderr as `wool: file:line:col: message`; JSON reports
  `ok`, `strict` and an `errors` array with `file`, `line`, `column`, `key` and
  `message`. Other commands that load the config use the same formatting via
  `printConfigError`.
- `wool run --strict` is unchanged: it still means "fail on smoke-test
  failure". Config strictness lives under `wool config check --strict` to avoid
  overloading that flag.
- When the TUI starts with an unreadable config and stdin is a terminal, the
  error is shown in a dismissable "config error" overlay instead of exiting
  with only a stderr line. Dismissing the overlay quits the TUI; redirected or
  scripted runs keep the plain CLI error. The overlay is rendered through the
  existing overlay path and has tests at several terminal sizes.
- Validation errors have no TOML position, so they are returned as a
  `config.Error` with `Line`/`Column` zero and render as `file: message`.
