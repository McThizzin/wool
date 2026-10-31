# M5.5 — Preset cloning and templates

Part of [M5](../roadmap.md#milestone-m5). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P3 · **Size** M · **Depends** M1.1

**Status** planned

**Problem.** Creating a variant means copying a preset by hand and remembering
which values to change.

**Proposal.**

```text
wool preset clone SRC NEW
wool preset new NAME --model ID --template low-vram|max-context|max-speed
```

Templates are built-in value sets; cloning preserves explicit overrides.
Refuse to overwrite unless `--force`.

**Acceptance.**

- [ ] Clone preserves overrides and model reference.
- [ ] Template values are valid and documented.
- [ ] Existing presets are never overwritten silently.
- [ ] The TUI exposes the same actions.
