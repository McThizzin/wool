# M4.7 — Router usage statistics

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** S/M · **Depends** M1.3, M4.1

**Status** shipped

**Problem.** `sleep_idle_seconds` is hard to tune without knowing which models
are actually used and when.

**Proposal.** Track last used, load count and tokens served per router model
from the existing polls and metric deltas. Persist in the measurement store and
show in the Router tab.

**Acceptance.**

- [x] Stats survive a TUI restart.
- [x] Token counters are not double-counted across polls.
- [x] A router restart is recorded rather than producing negative deltas.
- [x] The UI distinguishes this session from lifetime values.

**Implementation notes.**

- `measure.Usage` is a snapshot with a router identity and one entry per model:
  loads, prompt tokens, generated tokens and last used. It is stored as a
  `usage` record in the same JSONL file as observations and benchmarks.
  Compaction keeps only the newest usage record and applies `maxRecords` to the
  other kinds, so frequent flushes cannot evict history.
- The tracker scrapes `/metrics?model=ID` for every resident router model on
  each poll. The selected model's scrape is reused instead of fetched twice.
  A failed `/models` poll is ignored rather than treated as an empty resident
  set, so a transient error cannot invent a load.
- A sleeping model serves cached metrics, so usage polling does not wake it.
  The selected model normally uses `ScrapeInference`, but a sleeping selection
  uses `/metrics` only: llama-server's `/slots` handler waits for the model to
  wake.
- Token totals come from deltas of the cumulative counters. The first scrape
  for a model only seeds the baseline. A decrease reseeds instead of recording
  a negative delta. A new router PID/start-tick key clears the baselines and
  increments `Restarts`; models already resident under the new process count as
  fresh loads.
- A model that is already resident when the TUI starts is seeded without
  counting a load, so restarting the TUI does not inflate the load count. The
  persisted router key lets a restart that happened while the TUI was closed be
  detected on the next start.
- Session counters live in memory and start at zero for each TUI process;
  lifetime counters come from the persisted snapshot. The Router tab shows
  lifetime `LOADS`, `TOKENS` and `LAST` columns and prints both lifetime and
  session values for the selected model. Usage is flushed at most every 15
  seconds while dirty and once more on a clean exit.
