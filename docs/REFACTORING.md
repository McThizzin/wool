# Refactoring large files

This is an internal engineering plan, not a milestone. It describes how to split
the largest files without changing behavior, and the order to do it in before
M5. The [`v0.5.0`](../README.md) tag is the baseline: every split keeps the
full test suite green, so any regression can be bisected to a pure-move commit
and reverted on its own.

## Sequencing

Cut the release first. A tag is a known-good bisect point, users get the
milestone, and generated release notes describe features rather than file
moves. A refactor that lands after the tag can be reverted without touching the
release, and it does not delay M4. Do the refactor before M5 starts so feature
branches do not conflict with moved code.

## Principles

1. **Behavior-preserving only.** No command, flag, output, JSON field, config
   schema or exported API changes. No "while I'm here" fixes.
2. **Same package first.** Splitting a file within `package main` or
   `package app` has no import-cycle or API-design risk. Extract a new package
   only when a stable seam already exists, as `internal/endpoint` did.
3. **One package per commit.** Every commit must build and pass the suite on its
   own, so a bad split can be reverted alone.
4. **Move code, do not rewrite it.** Cut function bodies verbatim and adjust
   imports only. Review with `git diff --color-moved`.
5. **Tests move with the code.** Split test files the same way and keep test
   names unchanged.
6. **No new abstractions.** No interfaces, registries, generic helpers or
   dependency changes. Deduplication is a separate follow-up with its own tests.
7. **Short-lived branch.** Work on `refactor/split-large-files`, merge after
   `go test -race ./...` passes.

## Verification per split

```sh
gofmt -l .
go vet ./...
go build ./...
go test -count=1 ./...
go test -race ./internal/app   # for TUI changes
```

Function-count invariant: the old file and the replacement files must declare
the same number of functions (methods included).

```sh
old=$(git show HEAD:cmd/wool/cli.go | grep -c '^func ')
new=$(cat $(find cmd/wool -maxdepth 1 -name 'cli*.go' ! -name '*_test.go') |
  grep -c '^func ')
[ "$old" = "$new" ] || echo "function count changed: $old -> $new"
```

Also check `git diff --stat` (roughly equal line totals, one deletion plus new
files) and `git diff --color-moved=dimmed-zebra`. The split adds imports and
package clauses; nothing else should be new.

## Phase 1 — the four large CLI/TUI files

These are edited most often and have the clearest seams. No M5 feature should
start until this phase is merged.

### 1. `cmd/wool/cli.go` (3,870 lines)

Split by command into the same `main` package. Target: `cli.go` at ~400–500
lines, no new file over ~700.

| File | Contents |
| --- | --- |
| `cli.go` | `main`, `run`, dispatch, usage text, config loading, `openTUI`, `extractConfig` |
| `cli_doctor.go` | `cmdDoctor` |
| `cli_fit.go` | fit options, parsing, command, calibration and print helpers |
| `cli_bench.go` | `cmdBench`, run options/record input, `printBench`, VRAM and version helpers |
| `cli_bench_compare.go` | comparison parsing, rounds, sides, table and noise helpers |
| `cli_bench_history.go` | history parsing, command and filters |
| `cli_config.go` | config subcommands and `resolveNamedTarget` |
| `cli_list.go` | `cmdList` |
| `cli_run.go` | `cmdRun`, launch record, target resolution, readiness, server printing |
| `cli_supervise.go` | supervision command, supervisor loop and smoke helpers |
| `cli_status.go` | `status`, `restart`, `stop` and stop-target resolution |
| `cli_alerts.go` | `cmdAlerts` and alert input assembly |
| `cli_stats.go` | stats parsing, target resolution, scrape and print helpers |
| `cli_endpoint.go` | endpoint parsing, resolution and print helpers |
| `cli_logs.go` | `cmdLogs` and `followFile` |
| `cli_router.go` | router command, start, groups, load-group and model actions |
| `cli_output.go` | `writeJSON`, byte/token formatting, `fallbackDash`, `durationOr` |

### 2. `internal/app/view.go` (1,466 lines)

Split by tab, keeping shared chrome in `view.go`. Target: `view.go` at ~350
lines.

| File | Contents |
| --- | --- |
| `view.go` | `View`, header, body dispatch, main panel, sidebar, input, footer, overlay |
| `view_dashboard.go` | dashboard body, right column, status and machine panels |
| `view_models.go` | models and presets bodies |
| `view_router.go` | router body, summary and right column |
| `view_inference.go` | inference body/right column and stats lines |
| `view_hardware.go` | hardware body and right column |
| `view_logs.go` | log right column, structured/raw bodies, table and line rendering |
| `view_bench.go` | bench body and right column |
| `view_fit.go` | fit line, breakdown, calibration text, bar and byte formatting |
| `view_helpers.go` | layout, truncation, duration, token and map helpers |

### 3. `internal/app/model.go` (1,082 lines)

Separate state from the update loop. Target: `model.go` at ~400 lines.

| File | Contents |
| --- | --- |
| `model.go` | tab and `Model` types, message types, `Run`/`New`, `Init`, spinner |
| `update.go` | `Update`, `applyTick`, poll interval, `refreshLater` |
| `state.go` | selections, target resolution, fit cache, budget, metadata, tab/theme/reload, logs state |
| `helpers.go` | `indexOf`, `clamp`, `maxInt`/`minInt`, `oneLine`, `statFile` |

`refreshLater` contains several fetch closures; leave it intact for now. If it
is still hard to read after the split, extracting named `tea.Cmd` builders is a
follow-up that needs its own review.

### 4. `internal/app/editor.go` (867 lines)

| File | Contents |
| --- | --- |
| `editor.go` | editor types, open/update/view, scrolling, field validation and formatting |
| `editor_fit.go` | fit overlay, save-name/confirm states, preset saving, apply, previews |

## Phase 2 — library files M5 will touch

M5.1 (model discovery) edits config, GGUF and daemon code. Split these before it
starts to avoid conflicts.

### 5. `internal/gguf/gguf.go` (1,189 lines)

| File | Contents |
| --- | --- |
| `gguf.go` | `Meta`, `TensorInfo`, `ReadMeta`, `readMeta`, `readTensors` |
| `reader.go` | binary reader, scalar/value decoding and skipping |
| `meta.go` | layer/head/attention/KV accessors and tensor byte totals |
| `quant.go` | GGML type names, tensor byte sizes and bytes per element |
| `estimate.go` | `Estimate`, `Budget`, `ComputeBudget`, `EstimateVRAM` |
| `place.go` | tensor classes, name matching, weight placement |

### 6. `internal/config/config.go` (925 lines)

| File | Contents |
| --- | --- |
| `config.go` | types, defaults, `ApplyDefaults`, ID accessors |
| `load.go` | `Load`, `Parse`, `Validate`, `Warnings` |
| `resolve.go` | `Resolve`, option/sampling merging and dereference helpers |
| `provenance.go` | `Provenance`, field set, `ResolveDetailed`, defaults resolution |
| `format.go` | `FormatKB` and small formatting helpers |

### 7. `internal/llamaserver/client.go` (609 lines)

| File | Contents |
| --- | --- |
| `client.go` | `Client`, constructors, transport, errors, health, completion, readiness |
| `types.go` | completion, model, memory, slot and metrics types plus value methods |
| `models.go` | `Models`, `OpenAIModels`, reload, load and unload |
| `metrics.go` | metrics and inference scrapes, `ParseMetrics` |
| `slots.go` | `Slots` and `Props` |

## Phase 3 — optional follow-ups

Only after phases 1–2 are merged and stable:

- Split `internal/fit/fit.go` (749) into solver, candidates, settings and size
  parsing; `internal/doctor/doctor.go` (514) into report and check groups.
- Split `cmd/wool/cli_test.go` (1,462) by command once the command files exist.
- Deduplicate formatting helpers (`humanSize`/`humanTokens`, `fallback`/
  `fallbackDash`, repeated `clamp` helpers). This is behavior-adjacent, so do it
  separately with tests.
- Add a `.git-blame-ignore-revs` entry for the pure-move commits.

## What not to do

- Do not extract `internal/cli` or split `internal/app` into subpackages. Both
  would force exported APIs or create import cycles for little gain.
- Do not rename commands, flags, JSON fields, config keys or exported symbols
  during a move.
- Do not combine splits with behavior fixes, M5 features or dependency updates.
- Do not reorganize generated files, `config.example.toml` or golden output.

## Suggested commit sequence

```text
refactor(cli): split fit and config commands out of cli.go
refactor(cli): split benchmark commands out of cli.go
refactor(cli): split run, supervise and lifecycle commands out of cli.go
refactor(cli): split stats, endpoint, logs and router commands out of cli.go
refactor(cli): split output helpers out of cli.go
refactor(app): split tab views out of view.go
refactor(app): split state and update loop out of model.go
refactor(app): split fit editor out of editor.go
refactor(gguf): split reader, metadata, estimate and placement
refactor(config): split loading, resolution and provenance
refactor(llamaserver): split types, models, metrics and slots
refactor(app): split tests to match the new files
```

Each commit is a pure move. If one fails review or CI, revert that commit only;
the `v0.5.0` tag and the rest of the branch stay intact.
