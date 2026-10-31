# M5.7 — Hooks

Part of [M5](../roadmap.md#milestone-m5). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P3 · **Size** M · **Depends** M4.4

**Status** planned

**Problem.** Users want to update a reverse proxy, send a notification or
rotate a log when a server starts, stops or crashes.

**Proposal.**

```toml
[presets.<name>.hooks]
on_start = ["systemctl", "--user", "reload", "caddy"]
on_stop = []
on_crash = ["notify-send", "wool", "server crashed"]
```

Run hooks with a timeout, pass `WOOL_MODEL`, `WOOL_PRESET`, `WOOL_URL`,
`WOOL_EXIT_CODE` and `WOOL_CRASH_REASON`, and log failures without making the
launch fail. Prefer argv arrays over shell strings.

**Acceptance.**

- [ ] Each hook runs once per event.
- [ ] A failing hook is logged and does not stop the server.
- [ ] Timeouts are enforced.
- [ ] No shell interpolation by default.
- [ ] Tests use fake commands.

**M5 exit criteria:** NVIDIA users get telemetry and fit; multi-GPU flags work;
models can be discovered and added; a tuned preset can be exported; core
commands have stable JSON; config errors point at a line.
