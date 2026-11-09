# M3.2 — Benchmark history and trends

Part of [M3](../roadmap.md#milestone-m3). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** M · **Depends** M3.1

**Status** shipped

**Problem.** A single benchmark is a data point. Users need to see whether a
change helped over time.

**Proposal.** `wool bench history [TARGET] [--limit N] [--json]` and a TUI
Bench view with a throughput sparkline. Selecting two runs shows the config
diff between them (reuse M1.4).

```text
wool bench history [TARGET] [--limit 20] [--json]
```

- History is read from the same measurement store as observations. Only
  `kind: "bench"` records are listed.
- `TARGET` matches the recorded preset/model target or its model ID; omit it to
  show every benchmark.
- `--limit` keeps the newest runs; the default is 20.
- The TUI adds a Bench tab with prompt and generation sparklines, a run table
  (timestamp, target, prompt t/s, generation t/s, VRAM, load time, config hash)
  and full details for the selected run.
- `space` marks a run, `enter` compares the marked run with the selected one.
  The comparison uses the two-column field layout from M1.4 and adds throughput,
  VRAM and load-time deltas.
- `f` cycles the target filter, `D` opens the M1.4 config diff for the selected
  run's target when it still exists, and `B` jumps to the tab from anywhere.
- The store is bounded by record count: after an append pushes it over
  `DefaultMaxRecords` (5000), the oldest records are dropped and the newest are
  rewritten atomically. Writers serialize on a sibling `.lock` file.

**Acceptance.**

- [x] History survives restarts and can be filtered by target.
- [x] Each run shows timestamp, prompt t/s, generation t/s, VRAM, load time and
      config hash.
- [x] The store is bounded: rotate at a documented size or record count.
- [x] Two runs can be compared without leaving the TUI.

**Implementation notes.** `Store.BenchHistory` returns matching runs oldest
first and applies the newest-`limit` cut after filtering. `BenchTargets` and
`BenchTargetName` provide the filter list and display label. Rotation keeps the
newest 5000 records and runs best-effort after the append is durable, so a failed
compaction never loses the record that was just written. Compaction rewrites via
a temporary file and rename; the `.lock` file prevents an append in another wool
process from racing the rewrite. The TUI reloads history on its normal poll tick
and when the tab opens, so a `wool bench` run in another terminal appears without
restarting the UI.

**Risk.** A bounded store silently drops the oldest runs. The cap is high enough
for long-term tuning but low enough to keep startup and history scans quick.
Comparing two runs only compares the launch fields wool recorded; it is not a
statistical test, and a single benchmark remains noisy.
