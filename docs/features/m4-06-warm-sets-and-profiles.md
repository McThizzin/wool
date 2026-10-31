# M4.6 — Warm sets and profiles

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** M · **Depends** M4.5

**Status** planned

**Problem.** Users repeatedly load the same groups (“coding”, “chat +
embedding”) one model at a time.

**Proposal.** Config groups and one action to load them:

```toml
[groups.coding]
models = ["qwen-coder", "embed-small"]
```

`wool router load-group coding` and a TUI action load each model in order,
continue on error, and print a summary. Warn when the group exceeds
`models_max`.

**Acceptance.**

- [ ] All models in a group load, or the summary says which failed.
- [ ] The action respects `models_max` and warns before starting.
- [ ] Partial failure is explicit, never silent.
- [ ] Tests use a fake router client.
