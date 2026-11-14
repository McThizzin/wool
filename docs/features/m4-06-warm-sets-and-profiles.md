# M4.6 — Warm sets and profiles

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** M · **Depends** M4.5

**Status** shipped

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

- [x] All models in a group load, or the summary says which failed.
- [x] The action respects `models_max` and warns before starting.
- [x] Partial failure is explicit, never silent.
- [x] Tests use a fake router client.

**Implementation notes.**

- `[groups.NAME]` holds a non-empty `models` list of `[models.*]` IDs. Config
  validation rejects unknown models, duplicates, empty lists and invalid group
  names. `wool router groups` lists the configured sets.
- `wool router load-group NAME` and the Router tab's `w` picker (or
  `/load-group NAME`) load the models in order and wait for each one to reach
  `loaded` or `sleeping` before starting the next. A model that is already
  resident is reported as loaded without a second request, because the router
  rejects a load for a running model; a repeated group load is therefore
  idempotent.
- Loading continues after a per-model failure. The CLI prints a row per model
  and `group NAME: N/M loaded; failed: …`, then exits non-zero if any model
  failed. The TUI shows a one-line toast and opens an overlay with the full
  per-model result so a partial failure is never silent.
- `router.models_max` is not changed by a group load. When the group has more
  models than the limit, the CLI prints a warning before the first request and
  a note afterwards; the TUI asks for confirmation before starting. The router
  keeps the newest `models_max` models and evicts earlier group members, which
  is the same LRU behaviour the Router tab predicts.
- `llamaserver.LoadGroup` owns the sequential loop and the per-model wait.
  Tests drive it with a fake `GroupClient`; CLI tests use an HTTP test server
  and TUI tests cover the warning, confirmation and summary paths.
