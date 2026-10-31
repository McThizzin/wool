# M5.6 — Machine-readable surface

Part of [M5](../roadmap.md#milestone-m5). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** M/L · **Depends** M1.3, M3.1

**Status** planned

**Problem.** Scripts can use some commands, but JSON is inconsistent and there
is no local API.

**Proposal.**

- Make every read command support `--json` with a documented schema and a
  `schema_version` field.
- Add `wool api` as a local HTTP or Unix-socket server. Start read-only:
  status, list, doctor, fit, bench history. Add actions only with an explicit
  token and a clear security section.

**Acceptance.**

- [ ] JSON errors are structured, not plain text.
- [ ] Golden tests pin the schema.
- [ ] The API works without the TUI.
- [ ] The read-only API needs no authentication on loopback; actions require a
      token.
- [ ] Docs show one `curl` and one Go client example.
