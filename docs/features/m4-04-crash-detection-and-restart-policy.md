# M4.4 — Crash detection and restart policy

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** L · **Depends** none

**Status** shipped (detection, in-session restart, manual restart and detached
`wool supervise`)

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
- **Detached supervision:** `wool supervise TARGET` starts a small supervisor
  that owns the child, restarts it with backoff and keeps the state file
  pointing at the supervisor. `wool stop` stops the supervisor and the child.

**Acceptance.**

- [x] `kill -9` of the server is detected within one poll interval.
- [x] The crash card classifies the reason and shows the relevant log lines.
- [x] Manual restart works from the TUI and CLI.
- [x] Restart policy respects backoff, max attempts and the window.
- [x] A clean `wool stop` never triggers a restart.
- [x] The supervisor leaves no zombies and validates PID start ticks.
- [x] Tests use short-lived fake children.

**Implementation notes.**

- Detection compares a previously live instance with a state file that is
  present but dead. `daemon.Manager.Stop` removes the state file, so a clean
  TUI or CLI stop never looks like a crash. A manual restart sets an
  `expectStop` guard for the same reason.
- `daemon.ClassifyCrash` scans only the newest launch section and applies
  precedence OOM > port > model > signal > unknown.
- The crash card shows the reason, time, policy status and up to four relevant
  lines. `S` (or `/restart`) relaunches the recorded argv; `x` (or
  `/dismiss`) hides the card, cancels a pending automatic retry and keeps the
  state for `wool restart`.
- `[server] restart` defaults to `never`. Both `on-failure` and `always`
  retry an unexpected death; a clean stop is never restarted. The distinction
  is reserved for future exit-status handling. `restart_max` defaults to 3,
  and an explicit `0` disables automatic attempts without changing the policy.
  `restart_window` defaults to `10m`; `restart_backoff` defaults to `2s` and
  doubles per attempt, capped at one minute.
- Successful automatic attempts do not reset the attempt window, so a crash
  loop is bounded. A manual start, stop or restart resets it.
- `wool restart [server|router] [--no-wait]` reloads the recorded state with
  `daemon.SpecFromState`, reusing the original binary, argv and port instead of
  re-resolving the current config.
- `wool supervise TARGET` records the supervisor as the instance PID and keeps
  the child argv after `--` in the state. The supervisor re-execs as
  `wool supervise --foreground`; its child inherits the supervisor's process
  group, and the supervisor writes a launch header per child attempt so crash
  classification still sees only the newest failure.
- The supervisor always restarts an unexpected exit; `[server] restart` is not
  consulted. `restart_max`, `restart_window` and `restart_backoff` still apply,
  and `restart_max = 0` gives up after the first exit. Exhaustion returns
  `daemon.ErrSupervisorExhausted` and leaves the state file for the crash card
  and `wool restart`.
- `wool stop` signals the supervisor, not the group, so the supervisor can stop
  its child without treating the exit as a crash. If the supervisor is already
  gone, the recorded group is signalled directly to clean up an orphaned child.
- `wool status` labels supervised instances and reports an orphaned child when
  the supervisor is dead but its process group is still alive.
- TUI start and restart actions rebuild the launch from the current config, so
  they replace a supervised instance with an unsupervised one. Use
  `wool supervise` again to restore supervision. A crash-card restart reuses
  the recorded state and keeps supervision.
- Router-managed model children are not supervised; `llama-server` owns them.

**Risk.** Supervision is the point where wool starts becoming a process
manager. Keep the helper minimal, do not supervise router-managed model
children, and prefer exporting a systemd unit for users who want real service
management.
