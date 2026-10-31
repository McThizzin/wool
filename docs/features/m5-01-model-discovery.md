# M5.1 — Model discovery

Part of [M5](../roadmap.md#milestone-m5). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** M · **Depends** none

**Status** planned

**Problem.** Models are paths configured by hand, even though `model_dirs`
already exists.

**Proposal.** `wool models scan [--json]` and a TUI Discovered view:

- Walk `model_dirs` for `*.gguf`, skipping partial downloads and loops.
- Read metadata and show architecture, parameter count if available, native
  context and file size.
- Group configured versus discovered, and flag duplicates.
- Offer to add a discovered model as `[models.<id>]` with `path` and `alias`,
  using the comment-preserving writer.

**Acceptance.**

- [ ] A temporary directory tree produces the expected entries.
- [ ] Partial files and permission errors are skipped with a warning.
- [ ] Adding a model does not duplicate an existing path.
- [ ] Large directories do not block the UI; scanning has a concurrency limit.
- [ ] Generated entries load and resolve.
