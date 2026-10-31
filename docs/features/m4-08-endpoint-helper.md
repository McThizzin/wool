# M4.8 — Endpoint helper

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P3 · **Size** S · **Depends** none

**Status** planned

**Problem.** After a server starts, users still have to assemble a base URL and
client snippet by hand.

**Proposal.** `wool endpoint [server|router|MODEL] [--json]` and a TUI panel
with the base URL, model ID and copy-paste snippets for `curl`, the OpenAI
Python client, Continue and Open WebUI. Use the existing clipboard dependency.

**Acceptance.**

- [ ] URLs and model IDs match the running instance.
- [ ] Copy works or reports a clear clipboard error.
- [ ] No API keys or secrets are printed.
- [ ] `--json` is stable.

**M4 exit criteria:** a server can run for a day with live inference visibility,
alerts, crash detection and an explicit restart policy; router groups and usage
stats are usable; the endpoint helper works.
