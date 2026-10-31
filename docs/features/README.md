# Feature specs

Each planned feature has its own document. The [roadmap](../roadmap.md) owns
sequence and priority; these documents own scope, implementation notes and
acceptance criteria. Update the status here and in the feature document when
work starts or ships.

Status values: `planned`, `in progress`, `shipped`, `blocked`, `dropped`.

| Feature | Priority | Size | Depends | Status | Spec |
| --- | --- | --- | --- | --- | --- |
| M1.1 — Override provenance and reset-to-inherited | P0 | M | none | shipped | [m1-01-override-provenance-and-reset-to-inherited.md](m1-01-override-provenance-and-reset-to-inherited.md) |
| M1.2 — Estimate breakdown and what-if preview | P0 | M | none | planned | [m1-02-estimate-breakdown-and-what-if-preview.md](m1-02-estimate-breakdown-and-what-if-preview.md) |
| M1.3 — Observed-vs-estimate capture | P1 | S/M | none | planned | [m1-03-observed-vs-estimate-capture.md](m1-03-observed-vs-estimate-capture.md) |
| M1.4 — Effective-config diff | P2 | S | M1.1 | planned | [m1-04-effective-config-diff.md](m1-04-effective-config-diff.md) |
| M2.1 — MoE expert offload modeling | P1 | L | none | planned | [m2-01-moe-expert-offload-modeling.md](m2-01-moe-expert-offload-modeling.md) |
| M2.2 — Auto-fit solver | P1 | M/L | M1.2, M2.1 | planned | [m2-02-auto-fit-solver.md](m2-02-auto-fit-solver.md) |
| M2.3 — Save a recommendation as a preset | P2 | S | M2.2 | planned | [m2-03-save-a-recommendation-as-a-preset.md](m2-03-save-a-recommendation-as-a-preset.md) |
| M3.1 — `wool bench` | P1 | L | M1.3 | planned | [m3-01-wool-bench.md](m3-01-wool-bench.md) |
| M3.2 — Benchmark history and trends | P1 | M | M3.1 | planned | [m3-02-benchmark-history-and-trends.md](m3-02-benchmark-history-and-trends.md) |
| M3.3 — A/B comparison | P2 | M | M3.1, M3.2 | planned | [m3-03-a-b-comparison.md](m3-03-a-b-comparison.md) |
| M3.4 — Calibration correction | P1 | M | M1.3, M3.1 | planned | [m3-04-calibration-correction.md](m3-04-calibration-correction.md) |
| M3.5 — Smoke test on launch | P2 | S | none | planned | [m3-05-smoke-test-on-launch.md](m3-05-smoke-test-on-launch.md) |
| M4.1 — Live inference panel | P1 | M | none | planned | [m4-01-live-inference-panel.md](m4-01-live-inference-panel.md) |
| M4.2 — Structured request log | P2 | M | M4.1 | planned | [m4-02-structured-request-log.md](m4-02-structured-request-log.md) |
| M4.3 — Alerts | P1 | M | M4.1 | planned | [m4-03-alerts.md](m4-03-alerts.md) |
| M4.4 — Crash detection and restart policy | P1 | L | none | planned | [m4-04-crash-detection-and-restart-policy.md](m4-04-crash-detection-and-restart-policy.md) |
| M4.5 — Router eviction visualization | P2 | M | M2.1 | planned | [m4-05-router-eviction-visualization.md](m4-05-router-eviction-visualization.md) |
| M4.6 — Warm sets and profiles | P2 | M | M4.5 | planned | [m4-06-warm-sets-and-profiles.md](m4-06-warm-sets-and-profiles.md) |
| M4.7 — Router usage statistics | P2 | S/M | M1.3, M4.1 | planned | [m4-07-router-usage-statistics.md](m4-07-router-usage-statistics.md) |
| M4.8 — Endpoint helper | P3 | S | none | planned | [m4-08-endpoint-helper.md](m4-08-endpoint-helper.md) |
| M5.1 — Model discovery | P1 | M | none | planned | [m5-01-model-discovery.md](m5-01-model-discovery.md) |
| M5.2 — Disk and orphan view | P2 | S | M5.1 | planned | [m5-02-disk-and-orphan-view.md](m5-02-disk-and-orphan-view.md) |
| M5.3 — Export a preset as a service | P2 | S/M | none | planned | [m5-03-export-a-preset-as-a-service.md](m5-03-export-a-preset-as-a-service.md) |
| M5.4 — Config validation with line numbers | P2 | S/M | none | planned | [m5-04-config-validation-with-line-numbers.md](m5-04-config-validation-with-line-numbers.md) |
| M5.5 — Preset cloning and templates | P3 | M | M1.1 | planned | [m5-05-preset-cloning-and-templates.md](m5-05-preset-cloning-and-templates.md) |
| M5.6 — Machine-readable surface | P2 | M/L | M1.3, M3.1 | planned | [m5-06-machine-readable-surface.md](m5-06-machine-readable-surface.md) |
| M5.7 — Hooks | P3 | M | M4.4 | planned | [m5-07-hooks.md](m5-07-hooks.md) |
| PLAT-1 — NVIDIA telemetry backend | P3 | M/L | none | blocked | [platform-01-nvidia.md](platform-01-nvidia.md) |
| PLAT-2 — Multi-GPU awareness | P3 | L | PLAT-1, M2.1 | blocked | [platform-02-multi-gpu.md](platform-02-multi-gpu.md) |
| PLAT-3 — Intel GPU telemetry backend | P3 | M | none | blocked | [platform-03-intel.md](platform-03-intel.md) |

## Adding a feature

Copy [`_template.md`](_template.md), assign the next ID for its milestone,
and add a row above. Do not create a feature document for a Later Bet until
its trigger in the roadmap has happened.

## Platform track

The `PLAT-*` documents are intentionally blocked. Do not schedule them until
the hardware or a contributor exists; see the roadmap for the trigger.
