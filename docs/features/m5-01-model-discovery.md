# M5.1 — Model discovery

Part of [M5](../roadmap.md#milestone-m5). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** M · **Depends** none

**Status** shipped

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

- [x] A temporary directory tree produces the expected entries.
- [x] Partial files and permission errors are skipped with a warning.
- [x] Adding a model does not duplicate an existing path.
- [x] Large directories do not block the UI; scanning has a concurrency limit.
- [x] Generated entries load and resolve.

**Implementation notes.**

- `internal/discovery` walks every `app.model_dirs` entry recursively for
  `*.gguf` files. It reads metadata through the existing GGUF reader, skips
  `.part`, `.partial`, `.download`, `.crdownload`, `.tmp` and `.incomplete`
  downloads, and reports unreadable directories, invalid GGUF files and empty
  files as warnings instead of failing the whole scan.
- Metadata reads run in a bounded worker pool: four by default, clamped to 32.
  The TUI runs the same scan in a Bubble Tea command, so the interface is never
  blocked. `--jobs N` sets the CLI bound.
- Symlinks are skipped by default so a directory loop cannot expand a scan.
  `--follow` includes linked files and directories and uses real paths for loop
  and duplicate detection.
- Each entry gets a filename-derived ID slug (`Llama 3.gguf` → `llama-3`).
  The alias defaults to `general.name`, then the ID. Suggested IDs avoid IDs
  already used in `[models.*]`, including across multiple files in one scan.
- Configured paths are canonicalized with symlink resolution where possible, so
  two model IDs pointing at the same file are flagged as duplicates. The report
  separates configured models from new entries and marks missing files.
- `wool models add PATH [--id ID] [--alias ALIAS] [--json]` reads the GGUF,
  applies the same suggestions, reloads the config from disk and writes
  `[models.<id>]` with `path` and `alias` through the comment-preserving writer.
  An existing ID or path is refused. `wool models scan [--json]` prints the same
  report the TUI uses.
- The ninth TUI tab, Discovered, lists metadata, size and configured status.
  `enter`/`a` confirms adding the selected entry, `r` rescans, `w` shows scan
  warnings and `j`/`k` move the selection. Adding reloads `config.toml`, so the
  new model is immediately available to the Models, Router and Presets views.
