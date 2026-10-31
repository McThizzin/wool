# M3.2 — Benchmark history and trends

Part of [M3](../roadmap.md#milestone-m3). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** M · **Depends** M3.1

**Status** planned

**Problem.** A single benchmark is a data point. Users need to see whether a
change helped over time.

**Proposal.** `wool bench history [TARGET] [--limit N] [--json]` and a TUI
Bench view with a throughput sparkline. Selecting two runs shows the config
diff between them (reuse M1.4).

**Acceptance.**

- [ ] History survives restarts and can be filtered by target.
- [ ] Each run shows timestamp, prompt t/s, generation t/s, VRAM, load time and
      config hash.
- [ ] The store is bounded: rotate at a documented size or record count.
- [ ] Two runs can be compared without leaving the TUI.
