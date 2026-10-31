# M3.5 — Smoke test on launch

Part of [M3](../roadmap.md#milestone-m3). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** S · **Depends** none

**Status** planned

**Problem.** A server can report healthy and still fail to generate because of
a bad chat template, missing tensor or incompatible flags.

**Proposal.** Optionally send a tiny prompt after health and record whether the
preset generated a non-empty response.

- Config: `[server] smoke_test = false` (opt-in), `smoke_timeout = "30s"`.
- `wool run --smoke` forces it on; `--strict` makes failure fatal.
- Record `verified_at`, latency and a short failure reason in the measurement
  store. Show a verified badge in the TUI.

**Acceptance.**

- [ ] Smoke test is off by default and documented.
- [ ] A failure warns but does not stop the server unless `--strict`.
- [ ] Timeout is enforced and the child is cleaned up.
- [ ] Tests use a fake server for success, empty output and timeout.

**M3 exit criteria:** a preset can be benchmarked repeatedly, history and
comparison work, calibration is visible and conservative, and a launch can be
verified with one opt-in prompt.
