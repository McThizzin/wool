# M5.2 — Disk and orphan view

Part of [M5](../roadmap.md#milestone-m5). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** S · **Depends** M5.1

**Status** planned

**Problem.** Users accumulate models they no longer use and duplicates they
cannot see.

**Proposal.** `wool models disk [--json]` and a TUI column show per-model size,
total disk usage, models unused by any preset, duplicate paths and missing
files. Full content hashing is opt-in (`--hash`); the default uses size and
metadata identity.

**Acceptance.**

- [ ] Sizes are accurate for regular files.
- [ ] Unused and duplicate lists are correct.
- [ ] The command is read-only.
- [ ] `--hash` is clearly slower and documented.
