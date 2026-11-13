# M4.3 — Alerts

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** M · **Depends** M4.1

**Status** done

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

**Implementation.** `internal/alerts` keeps the rules pure: `Engine.Evaluate`
takes one observation (hardware, instance health, router model status and log
tails) and returns only alerts that are past their per-rule cooldown. The
deduplication key is `rule|subject`, so temperature and VRAM can both warn for
the GPU while a failing router model is tracked separately. The TUI evaluates
the same input on every poll, keeps the newest 20 alerts in memory and shows
the newest two in the Status panel. Log tails are read in the existing
background refresh only while alerts are enabled, and notification delivery
runs as a Bubble Tea command, so neither disk reads nor `notify-send` or an
HTTP POST can block the UI.

Health failures wait 15 seconds from the instance start time (or the first
failed observation when the start time is unknown) to avoid launch false
positives; `loading` and `starting` are not failures. Log rules fingerprint the
newest matching line, so a stale OOM line that remains in the tail does not
re-fire after every cooldown. A restart loop is three observed `StartedAt`
changes within ten minutes. `wool alerts [--json]` performs a one-shot
evaluation with the same rules and prints the stable `alertsSnapshot`
(version 1) with `enabled` and `alerts`; it does not send notifications.

`notify = true` uses `notify-send` when it is installed and degrades silently
when it is not. The optional webhook receives one JSON POST per alert (version
1 fields `version`, `at`, `rule`, `severity`, `subject`, `message`) with a
three-second client timeout; errors never include the URL, so tokens embedded
in it cannot leak into a toast or log line. Thresholds are validated on load:
`temp_c` 0–150, `vram_percent` 0–100, a positive `cooldown`, and an HTTP(S)
webhook URL.

**Acceptance.**

- [x] Thresholds are configurable and validated.
- [x] One condition produces one alert per cooldown window.
- [x] A missing notification binary degrades silently to the status bar.
- [x] Webhook calls have a short timeout and never block the UI.
- [x] Secrets in webhook URLs are not logged.
- [x] Tests use a fake notifier and clock.

**Risk.** Alert rules can become noisy or hide a real problem behind a
deduplicated one. Keep the engine pure and the rule set small; the status bar
always shows the alert even when optional delivery fails.
