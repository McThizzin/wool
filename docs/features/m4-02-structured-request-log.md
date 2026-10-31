# M4.2 — Structured request log

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** M · **Depends** M4.1

**Status** planned

**Problem.** The Logs tab shows raw text. Users want to know which requests
were slow or cache-heavy.

**Proposal.** Parse the request completion lines that `llama-server` already
writes into a structured table: timestamp, model, prompt tokens, generated
tokens, prompt/generation time, cache hits and status. Keep a bounded in-memory
tail. If the format is unknown, fall back to the raw log and say so.

**Acceptance.**

- [ ] Known log fixtures parse into rows.
- [ ] Unknown lines are ignored, never turned into false rows.
- [ ] The view can filter by model and pause/follow.
- [ ] The UI labels the view “best effort” and links to the raw log.
- [ ] Parser tests cover at least two `llama-server` versions.

**Risk.** Log formats drift. Feature-detect with the version reported by
Doctor and keep the parser isolated.
