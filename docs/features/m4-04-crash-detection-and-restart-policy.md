# M4.4 — Crash detection and restart policy

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** L · **Depends** none

**Status** planned

**Problem.** A standalone server can die silently. The user finds out when a
request fails, and the log tail rarely says why in one line.

**Proposal.** Split into detection and supervision.

- **Detection:** when the recorded process dies, mark the instance crashed,
  scan the log tail for OOM, port conflict, model-load failure, signal or
  unknown, and show a crash card with the last relevant lines and a manual
  restart action.
- **In-session restart:** optional `[server] restart = "never" | "on-failure" |
  "always"` with backoff, max attempts and a time window. This works while a
  wool process is attached.
- **Detached supervision:** if in-session restart is not enough, add a small
  `wool supervise TARGET` helper that owns the child, restarts it with backoff
  and writes the state file. `wool stop` stops the supervisor and the child.

**Acceptance.**

- [ ] `kill -9` of the server is detected within one poll interval.
- [ ] The crash card classifies the reason and shows the relevant log lines.
- [ ] Manual restart works from the TUI and CLI.
- [ ] Restart policy respects backoff, max attempts and the window.
- [ ] A clean `wool stop` never triggers a restart.
- [ ] The supervisor leaves no zombies and validates PID start ticks.
- [ ] Tests use short-lived fake children.

**Risk.** Supervision is the point where wool starts becoming a process
manager. Keep the helper minimal, do not supervise router-managed model
children, and prefer exporting a systemd unit for users who want real service
management.
