# M4.3 — Alerts

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** M · **Depends** M4.1

**Status** planned

**Problem.** Thermal throttling, VRAM pressure, OOM and an unresponsive server
are only visible if the user is staring at the right panel.

**Proposal.** Configurable alert rules with deduplication:

```toml
[alerts]
enabled = true
temp_c = 90
vram_percent = 95
cooldown = "5m"
notify = true          # desktop notification
webhook = ""           # optional JSON POST
```

- Conditions: temperature threshold, VRAM threshold, health failure, OOM or
  “out of memory” in the log tail, failed model load, restart loop.
- Surface in the status bar, optionally via `notify-send` and a webhook.
- Deduplicate per rule and respect the cooldown.

**Acceptance.**

- [ ] Thresholds are configurable and validated.
- [ ] One condition produces one alert per cooldown window.
- [ ] A missing notification binary degrades silently to the status bar.
- [ ] Webhook calls have a short timeout and never block the UI.
- [ ] Secrets in webhook URLs are not logged.
- [ ] Tests use a fake notifier and clock.
