# M4.2 — Structured request log

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** M · **Depends** M4.1

**Status** done

**Problem.** The Logs tab shows raw text. Users want to know which requests
were slow or cache-heavy.

**Proposal.** Parse the request completion lines that `llama-server` already
writes into a structured table: timestamp, model, prompt tokens, generated
tokens, prompt/generation time, cache hits and status. Keep a bounded in-memory
tail. If the format is unknown, fall back to the raw log and say so.

**Implementation.** `internal/reqlog` parses both common layouts — the older
`slot print_timing:` prefix followed by bare timing lines and
`n_prompt_tokens`, and the newer inline `prompt eval time` with
`task.n_tokens` — keyed by `(slot, task)`. A row is emitted when `total time`
closes the block; non-2xx `log_server_r` lines become error rows on their own.
Cache hits are prompt tokens minus prompt-eval tokens, clamped at zero.
Unrecognised lines are ignored, and `Recognized()` lets the TUI fall back to
the raw log with an `unrecognised ... request log` note. The Logs tab defaults
to the table, keeps the newest 200 rows, rebuilds incrementally by diffing the
tailed log (resetting on truncation or rotation), and offers `v` for raw,
`m` to filter by model and `f` to pause/follow. Standalone rows use the
running preset/model; router rows map the child `[port]` prefix to the model's
`--port` argument, falling back to `router`. `wool logs --json` prints the
stable `logsSnapshot` (version 1) with `source`, `best_effort`, `recognized`
and `rows`, and rejects `-f`.

**Acceptance.**

- [x] Known log fixtures parse into rows.
- [x] Unknown lines are ignored, never turned into false rows.
- [x] The view can filter by model and pause/follow.
- [x] The UI labels the view “best effort” and links to the raw log.
- [x] Parser tests cover at least two `llama-server` versions.

**Risk.** Log formats drift. Feature-detect with the version reported by
Doctor and keep the parser isolated.
