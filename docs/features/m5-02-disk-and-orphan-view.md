# M5.2 — Disk and orphan view

Part of [M5](../roadmap.md#milestone-m5). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** S · **Depends** M5.1

**Status** shipped

**Problem.** Users accumulate models they no longer use and duplicates they
cannot see.

**Proposal.** `wool models disk [--json]` and a TUI column show per-model size,
total disk usage, models unused by any preset, duplicate paths and missing
files. Full content hashing is opt-in (`--hash`); the default uses size and
metadata identity.

**Acceptance.**

- [x] Sizes are accurate for regular files.
- [x] Unused and duplicate lists are correct.
- [x] The command is read-only.
- [x] `--hash` is clearly slower and documented.

**Implementation notes.**

- `internal/discovery/disk.go` builds a read-only inventory from
  `[models.*]`. Each entry records the resolved path, size, status (`ok`,
  `missing`, `unreadable`) and the presets that reference the model. It never
  writes to the config or the filesystem.
- Total disk usage counts unique canonical paths, so two model IDs pointing at
  the same file are counted once. Duplicate paths are reported separately.
- Unused means no `[presets.*]` entry references the model. Groups and direct
  launches are not counted; the spec defines presets as the usage signal.
- The default duplicate-content check uses file size plus GGUF identity
  (`general.name`, architecture, parameter count, native context and tensor
  count). `--hash` reads every file in full and compares SHA-256 digests. The
  JSON report exposes the mode as `duplicate_detection`
  (`size+metadata` or `sha256`), and warnings cover files whose metadata or
  hash could not be read. `--hash` is documented as much slower because it
  reads whole model files rather than headers.
- `wool models disk [--json] [--hash]` prints the report or a stable JSON
  document. The command only loads the config and stats/reads model files.
- The Models tab shows a summary line (total size, unused, missing, duplicate
  paths) and `SIZE`, `USE` and `STATUS` columns; the panel header shows total
  size. The TUI uses the stat-only path (`SkipContent`) so entering the tab
  stays responsive. Content duplicates remain a CLI concern.
