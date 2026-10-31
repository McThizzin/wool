# M4.5 — Router eviction visualization

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** M · **Depends** M2.1

**Status** planned

**Problem.** With `models_max` and a memory margin, loading a new model can
evict one the user still needs. The router does not make the choice obvious in
the TUI.

**Proposal.** Show resident models, the limit, the margin and a best-effort
prediction of which model the next load would evict, with the estimate behind
the prediction. Label it as predicted unless the router exposes a definitive
policy.

**Acceptance.**

- [ ] Shows resident count, limit, margin and candidate.
- [ ] The prediction is labeled and never presented as a guarantee.
- [ ] Missing estimates degrade to “unknown”.
- [ ] Tests cover at-limit, over-limit and empty states.
