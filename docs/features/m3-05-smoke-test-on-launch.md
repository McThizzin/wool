# M3.5 — Smoke test on launch

Part of [M3](../roadmap.md#milestone-m3). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** S · **Depends** none

**Status** shipped

**Problem.** A server can report healthy and still fail to generate because of
a bad chat template, missing tensor or incompatible flags.

**Proposal.** Optionally send a tiny prompt after health and record whether the
preset generated a non-empty response.

- Config: `[server] smoke_test = false` (opt-in), `smoke_timeout = "30s"`.
- `wool run --smoke` forces it on; `--strict` makes failure fatal.
- Record `verified_at`, latency and a short failure reason in the measurement
  store. Show a verified badge in the TUI.

**Acceptance.**

- [x] Smoke test is off by default and documented.
- [x] A failure warns but does not stop the server unless `--strict`.
- [x] Timeout is enforced and the child is cleaned up.
- [x] Tests use a fake server for success, empty output and timeout.

**Implementation notes.** `internal/measure/smoke.go` owns the check. `RunSmoke`
sends one tiny non-streaming completion (`SmokePrompt`, eight tokens,
temperature 0) through `llamaserver.Client.Complete`. The timeout covers the
whole request, including connection setup and generation; `DefaultSmokeTimeout`
is 30s. Failures are reduced to a short reason: `timeout`, `empty response`, or
a one-line HTTP error capped at 120 characters.

`[server] smoke_test` defaults to `false`; `smoke_timeout` defaults to `"30s"`
and must parse as a positive duration. `wool run --smoke` forces the check on
for one launch, while `--strict` requires it to be enabled and stops the child
after printing the log tail when it fails. `--smoke --no-wait` is rejected
because there is no health wait to hook into; a config-enabled check is skipped
with a warning when `--no-wait` is used.

A record gains `verified_at` (set only on success), `smoke_ms` and
`smoke_error`. `Store.LatestVerification(shape)` ignores records with neither
field, so an ordinary observation does not hide the last attempt. The TUI runs
the same check in the background after health and shows
`✓ verified 01-02 15:04 · N ms` or `smoke test failed: …` in the dashboard fit
line. Tests use `httptest` fake servers for success, empty output and timeout.

**M3 exit criteria:** a preset can be benchmarked repeatedly, history and
comparison work, calibration is visible and conservative, and a launch can be
verified with one opt-in prompt.
