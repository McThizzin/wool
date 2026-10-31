# M4.7 — Router usage statistics

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** S/M · **Depends** M1.3, M4.1

**Status** planned

**Problem.** `sleep_idle_seconds` is hard to tune without knowing which models
are actually used and when.

**Proposal.** Track last used, load count and tokens served per router model
from the existing polls and metric deltas. Persist in the measurement store and
show in the Router tab.

**Acceptance.**

- [ ] Stats survive a TUI restart.
- [ ] Token counters are not double-counted across polls.
- [ ] A router restart is recorded rather than producing negative deltas.
- [ ] The UI distinguishes this session from lifetime values.
