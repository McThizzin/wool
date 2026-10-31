# M5.3 — Export a preset as a service

Part of [M5](../roadmap.md#milestone-m5). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** S/M · **Depends** none

**Status** planned

**Problem.** A tuned launch should be able to become a persistent service
without copying flags by hand.

**Proposal.** `wool export TARGET --systemd|--script|--compose [--out PATH]`
emits:

- a systemd user unit with a quoted `ExecStart`, `Restart=on-failure` and the
  resolved environment;
- a shell script ending in `exec`;
- a Docker Compose fragment with configurable image and model volume.

**Acceptance.**

- [ ] Generated units parse with `systemd-analyze verify` when available.
- [ ] Paths and arguments are quoted correctly.
- [ ] No secrets are embedded.
- [ ] Golden tests cover each format.
