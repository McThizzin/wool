# wool roadmap

Last updated: 2026-10-30
Status: living document. Milestones are ordered, not dated. A milestone ships
when its exit criteria are met, not on a calendar date.

**At a glance:** finish the baseline, then make the estimate explainable (M1),
turn it into a recommendation (M2), prove the recommendation with benchmarks
(M3), make the server safe to leave running (M4), and fit wool into model
management and scripts (M5). Chat, model downloads, remote hosts and additional
GPU vendors are deliberately after v1.0.

Feature scope and acceptance criteria live in [`docs/features/`](features/README.md).
This document owns sequence and priority.

## Product thesis

wool is the fastest path from a GGUF file on disk to a correctly sized, running
`llama-server` — and the place you go to find out whether it is running well.

The loop we optimize:

```text
choose -> fit -> launch -> observe -> measure -> tune -> persist
```

Today wool is strong at **launch** and **observe**, and weak at **fit**,
**measure** and **tune**. The roadmap closes that loop instead of adding
unrelated surface area. Every feature should make one of those steps faster,
more trustworthy, or measurable.

What wool is:

- a Linux-first control plane for one workstation;
- a TUI and CLI over a single TOML source of truth;
- a detached lifecycle that survives the TUI exiting;
- a decision aid for VRAM fit, not a process supervisor for model children.

What it is not, through v1.0:

- a multi-user or multi-tenant service;
- a cloud or fleet orchestrator;
- a model quality evaluator;
- an exact VRAM meter;
- a replacement for `llama-server`'s router;
- a cross-vendor or multi-GPU tool.

## Decisions

These are settled for the v1.0 path. Revisit only with evidence.

- **Primary persona:** one Linux workstation with a single 8–16 GB GPU. AMD is
  the only vendor available for testing today. Optimize defaults and estimates
  for that machine, not for a fleet.
- **Platform breadth:** NVIDIA, Intel and multi-GPU are not on the v1.0 path.
  They live in the [platform track](#platform-track) and are blocked on
  hardware or a contributor. Keep the backend interface clean; do not pretend
  to support hardware that cannot be tested.
- **Measurement storage:** JSONL under
  `$XDG_STATE_HOME/wool/measurements/`. It is append-only, versioned and easy
  to inspect. Migrate to SQLite only if query or retention needs outgrow it.
- **Fit is advisory first.** `wool fit` recommends; `--save-preset` writes a
  preset explicitly. No silent config mutation.
- **Milestones are ordered, not dated.** Ship a milestone when its exit
  criteria are met.
- **One feature, one spec.** Each planned feature has a document under
  [`docs/features/`](features/README.md). The roadmap does not duplicate scope.

## Where we are

Shipped foundation:

| Area | Today |
| --- | --- |
| Launch | Detached single-server and router modes, health wait, auto-port, PID/start-tick validation |
| Config | TOML schema, layered inheritance, validation, comment-preserving scalar edits |
| Fit | Dense and hybrid-attention VRAM estimate, GGUF metadata reader, AMD telemetry |
| Observe | Dashboard, hardware sparklines, log tail, router `/metrics` and `/slots` |
| Operate | Start/stop/restart, stale-state cleanup, log tailing |
| CLI | `run`, `stop`, `status`, `logs`, `router`, `list`, `doctor`, `config`, JSON for some commands |

Known gaps that shape the sequence:

| Gap | Consequence |
| --- | --- |
| The estimate is never checked against observed VRAM | Users cannot tell whether to trust it |
| `n_cpu_moe` is not modeled | MoE models on 8 GB cards look unusable when they may fit |
| No benchmark or history | Preset tuning is folklore |
| The editor writes concrete values but cannot remove overrides | Inheritance is one-way and hard to undo |
| No crash detection, restart policy or alerts | A server can die silently |
| Models are configured by hand | Setup friction before the first launch |
| One standalone server and one router | No named instances or fleets |

## How work is prioritized

Priority describes importance and dependency order, not the current sprint.
The current milestone is the schedule.

| Priority | Meaning |
| --- | --- |
| **P0** | Foundation or trust issue. Do before work that depends on it. |
| **P1** | Core-loop differentiator. Target before v1.0. |
| **P2** | Valuable, but after prerequisites or lower urgency. Target before v1.0 if capacity allows. |
| **P3** | Trigger-based or post-1.0. Revisit at each milestone boundary. |

Sizes are rough, for one focused maintainer:

| Size | Meaning |
| --- | --- |
| **S** | Up to 2 days |
| **M** | 3–5 days |
| **L** | 6–10 days |
| **XL** | More than 10 days or needs a spike first |

Sequencing rules:

1. Prefer work that makes later work cheaper: the measurement store,
   provenance, GGUF tensor metadata and stable JSON all pay for themselves.
2. Ship vertical slices: CLI + TUI + `--json` + tests + docs, not a backend with
   no user-facing path.
3. Every feature must degrade gracefully when telemetry, metrics or the API is
   unavailable. A missing sensor is not an error.
4. Do not start the next milestone before the current one meets its exit
   criteria. The only parallel track is platform work that does not touch the
   core loop, and that track is blocked today.
5. Config schema changes are additive within a minor release. Old configs must
   keep loading.

Definition of done for any feature:

- [ ] CLI path and TUI path where both make sense.
- [ ] `--json` output for anything scripts may consume.
- [ ] Unit tests; no network or real XDG state in tests.
- [ ] Docs updated (`README.md`, `docs/DESIGN.md`, the feature spec, this file).
- [ ] Graceful behavior with no GPU, no metrics, or a missing binary.
- [ ] No new runtime dependency without a written justification.

## Milestones at a glance

| # | Release | Theme | User outcome | Size |
| --- | --- | --- | --- | --- |
| M0 | v0.1.x | Baseline | The repo is testable and releasable | S |
| M1 | v0.2 | Explain | “I know where each setting came from and why the estimate is what it is.” | M |
| M2 | v0.3 | Recommend | “Tell me settings that fit my GPU, and explain the trade-off.” | M |
| M3 | v0.4 | Measure | “Prove that a preset change helped, and see how wrong the estimate was.” | L |
| M4 | v0.5 | Operate | “Leave it running all day and recover from failures.” | L |
| M5 | v0.6 | Integrate | “Finds my models and fits into my scripts and services.” | M/L |
| v1.0 | Daily driver | Trust | The loop works end to end without hand-editing TOML | — |

The order is deliberate: explain the numbers, then recommend settings, then
measure whether the recommendation worked, then make the server safe to leave
running, then fit wool into the rest of the workflow.

Why this order:

- **Explain before recommend.** A recommendation the user cannot inspect or
  undo is not trustworthy. Provenance and a decomposed estimate are cheap and
  make the solver explainable.
- **Recommend before measure.** The solver gives users something concrete to
  test. Benchmarks then validate whether the recommendation was right.
- **Measure before operate.** Once a preset is tuned, the next question is
  whether it stays healthy. Live inference, alerts and crash handling build on
  the same polling and measurement plumbing.
- **Integrate last.** Model discovery, export and JSON make a trusted tool
  easier to use. They are not a substitute for a trustworthy core.
- **Chat and downloads are later.** They are large, network-dependent surfaces
  that do not improve the core fit/measure loop.

<a id="milestone-m0"></a>

## M0 — Baseline (v0.1.x)

**Outcome:** the current foundation is a reproducible release, so later
milestones have a known-good starting point.

- [x] GitHub Actions workflow: `gofmt -l .`, `go vet ./...`, `go build ./...`,
      `go test -count=1 ./...` on push and pull request.
- [ ] Tag `v0.1.0` with `-ldflags` setting `version`, `commit` and `date`.
- [x] Verify the README quickstart from a clean clone and a fresh XDG directory.
- [x] Add a short release checklist (tag, build, notes, artifact).

**Exit criteria:** green CI on `main`; `v0.1.0` tagged; a clean clone builds and
passes tests; `wool version` reports the tag.

<a id="milestone-m1"></a>

## M1 — Explain the numbers (v0.2)

**Outcome:** “I can see where every effective setting came from, why the
estimate is what it is, and what changed after my last run.”

This milestone is mostly pure logic and has no dependency on live inference. It
builds trust in the estimator before asking users to act on it.

| ID | Feature | Priority | Size |
| --- | --- | --- | --- |
| M1.1 | [Override provenance and reset](features/m1-01-override-provenance-and-reset-to-inherited.md) | P0 | M |
| M1.2 | [Estimate breakdown and what-if preview](features/m1-02-estimate-breakdown-and-what-if-preview.md) | P0 | M |
| M1.3 | [Observed-vs-estimate capture](features/m1-03-observed-vs-estimate-capture.md) | P1 | S/M |
| M1.4 | [Effective-config diff](features/m1-04-effective-config-diff.md) | P2 | S |

**Exit criteria:** provenance and reset work for every editor field; the
estimate is decomposed in the UI and JSON; at least one observed measurement is
recorded and displayed; the diff view works; all tests pass.

<a id="milestone-m2"></a>

## M2 — Recommend settings (v0.3)

**Outcome:** “Tell me the settings that fit my GPU, and explain the trade-off.”

M2 turns the estimate from a warning into a recommendation. It depends on M1’s
breakdown and on better GGUF metadata for MoE models.

| ID | Feature | Priority | Size |
| --- | --- | --- | --- |
| M2.1 | [MoE expert offload modeling](features/m2-01-moe-expert-offload-modeling.md) | P1 | L |
| M2.2 | [Auto-fit solver](features/m2-02-auto-fit-solver.md) | P1 | M/L |
| M2.3 | [Save a recommendation as a preset](features/m2-03-save-a-recommendation-as-a-preset.md) | P2 | S |

**Exit criteria:** `wool fit` works for dense and MoE models, the editor can
preview and apply a recommendation, and a saved preset launches.

<a id="milestone-m3"></a>

## M3 — Measure it (v0.4)

**Outcome:** “I can prove that a preset change helped, and see how wrong the
estimate was.”

This is the highest-value milestone for tuning. It reuses the M1 measurement
store and the M2 fit recommendation.

| ID | Feature | Priority | Size |
| --- | --- | --- | --- |
| M3.1 | [`wool bench`](features/m3-01-wool-bench.md) | P1 | L |
| M3.2 | [Benchmark history and trends](features/m3-02-benchmark-history-and-trends.md) | P1 | M |
| M3.3 | [A/B comparison](features/m3-03-a-b-comparison.md) | P2 | M |
| M3.4 | [Calibration correction](features/m3-04-calibration-correction.md) | P1 | M |
| M3.5 | [Smoke test on launch](features/m3-05-smoke-test-on-launch.md) | P2 | S |

**Exit criteria:** a preset can be benchmarked repeatedly, history and
comparison work, calibration is visible and conservative, and a launch can be
verified with one opt-in prompt.

<a id="milestone-m4"></a>

## M4 — Operate it (v0.5)

**Outcome:** “I can leave wool running all day, see problems as they happen,
and recover without guessing.”

| ID | Feature | Priority | Size |
| --- | --- | --- | --- |
| M4.1 | [Live inference panel](features/m4-01-live-inference-panel.md) | P1 | M |
| M4.2 | [Structured request log](features/m4-02-structured-request-log.md) | P2 | M |
| M4.3 | [Alerts](features/m4-03-alerts.md) | P1 | M |
| M4.4 | [Crash detection and restart policy](features/m4-04-crash-detection-and-restart-policy.md) | P1 | L |
| M4.5 | [Router eviction visualization](features/m4-05-router-eviction-visualization.md) | P2 | M |
| M4.6 | [Warm sets and profiles](features/m4-06-warm-sets-and-profiles.md) | P2 | M |
| M4.7 | [Router usage statistics](features/m4-07-router-usage-statistics.md) | P2 | S/M |
| M4.8 | [Endpoint helper](features/m4-08-endpoint-helper.md) | P3 | S |

**Exit criteria:** a server can run for a day with live inference visibility,
alerts, crash detection and an explicit restart policy; router groups and usage
stats are usable; the endpoint helper works.

<a id="milestone-m5"></a>

## M5 — Integrate (v0.6)

**Outcome:** “Finds my models and fits into my scripts and services.”

This milestone removes setup friction and makes wool scriptable. It is not a
platform milestone; additional GPU vendors stay blocked.

| ID | Feature | Priority | Size |
| --- | --- | --- | --- |
| M5.1 | [Model discovery](features/m5-01-model-discovery.md) | P1 | M |
| M5.2 | [Disk and orphan view](features/m5-02-disk-and-orphan-view.md) | P2 | S |
| M5.3 | [Export a preset as a service](features/m5-03-export-a-preset-as-a-service.md) | P2 | S/M |
| M5.4 | [Config validation with line numbers](features/m5-04-config-validation-with-line-numbers.md) | P2 | S/M |
| M5.5 | [Preset cloning and templates](features/m5-05-preset-cloning-and-templates.md) | P3 | M |
| M5.6 | [Machine-readable surface](features/m5-06-machine-readable-surface.md) | P2 | M/L |
| M5.7 | [Hooks](features/m5-07-hooks.md) | P3 | M |

**Exit criteria:** models can be discovered and added; a tuned preset can be
exported; core commands have stable JSON; config errors point at a line;
cloning and hooks work.

## v1.0 — Daily driver

v1.0 is not a feature milestone. It is the point where the loop works end to
end for the primary persona:

- A new user on a single 8–16 GB AMD GPU can install wool, run
  `wool config init`, discover models, fit a preset, launch it, benchmark it,
  and export a service without hand-editing TOML.
- The estimate shows its raw value, observed history and calibrated correction
  when enough data exists. Fit never recommends an over-budget configuration.
- A server survives the TUI exiting. Crash detection, restart policy and alerts
  are available and opt-in.
- Core read commands have stable JSON; the config schema is documented and old
  configs keep loading.
- CI runs the full test suite and the multi-size view tests; releases are
  tagged and reproducible.
- No known data-loss or security issues in config editing, hooks or the local
  API.
- Unsupported hardware fails with a clear message, not a misleading dashboard.

<a id="platform-track"></a>

## Platform track (P3, blocked)

Not scheduled. The primary target is one AMD GPU with 8–16 GB. NVIDIA, Intel
and multi-GPU work is blocked on hardware or a contributor. Keep
`sysinfo.Backend` and the fake backend clean so a backend can land without a
refactor, but do not implement or advertise a vendor that cannot be tested.

| ID | Feature | Trigger | Size |
| --- | --- | --- | --- |
| PLAT-1 | [NVIDIA telemetry backend](features/platform-01-nvidia.md) | Maintainer gets NVIDIA hardware, or a contributor can test and maintain it | M/L |
| PLAT-2 | [Multi-GPU awareness](features/platform-02-multi-gpu.md) | PLAT-1 lands and there is real multi-GPU use | L |
| PLAT-3 | [Intel GPU telemetry](features/platform-03-intel.md) | A contributor with Intel Arc or iGPU hardware owns it | M |

Merge bar for any platform feature: fixtures for the vendor paths, AMD
behavior unchanged, missing sensors degrade gracefully, no new cgo dependency,
and docs that state what was actually tested.

## Later bets

These are deliberately outside the v1.0 path. Each has a trigger that would
move it up. Keeping them here prevents the roadmap from becoming a wishlist.
Promote one only when its trigger happens.

| ID | Bet | Why later | Trigger to promote | Size |
| --- | --- | --- | --- | --- |
| L1 | **Quick chat and live sampling tuning** | Needs a streaming client, chat view and conversation state; large surface for a tool that already proves presets with `bench` | Bench and the live panel are shipped, and users ask to test prompts without leaving wool | L |
| L2 | **Hugging Face search and download** | Network, auth, resumable downloads, disk checks, licenses and a quant recommender all at once | Model discovery shows that most discovered models are missing or unconfigured | XL |
| L3 | **Quant recommender** | Depends on discovery plus a catalog of available quants and a fit model | HF integration exists, or a local quant catalog is available | L |
| L4 | **Parameter sweep** | Multiplies benchmark runtime and needs plotting; easy to overfit to noise | Bench history has enough runs to make a sweep comparison meaningful | L |
| L5 | **Remote hosts over SSH** | Changes the lifecycle, state and transport model; a fleet tool, not a workstation tool | Repeated requests for remote management, or a second machine in the maintainer’s own workflow | XL |
| L6 | **Detached supervisor** | M4.4 covers detection and in-session restart; a detached supervisor is a second process model | Users report servers dying while wool is closed, and systemd export is not enough | L |
| L7 | **Quality/perplexity A/B** | Needs an evaluation dataset and a defensible methodology | Speed A/B is established and users want quality trade-offs | XL |
| L8 | **Named multi-instance servers** | The daemon layer already uses names, but the TUI and CLI assume `server` and `router` | One standalone server becomes a real limitation in daily use | L |

## Cross-cutting prerequisites

These are not user-facing features, but they are the cheapest way to make the
milestones above smaller.

| Prerequisite | Why | Unblocks | Milestone |
| --- | --- | --- | --- |
| CI and release baseline | Every later change needs a known-good build | All | M0 |
| `internal/measure` JSONL store | One place for observations, benchmarks and usage | M1.3, M3.x, M4.7 | M1 |
| `config.ResolveDetailed` + `RemoveValue` | Provenance, diff and reset | M1.1, M1.4 | M1 |
| GGUF tensor-info table | Expert bytes, better quant metadata | M2.1, M5.1 | M2 |
| Completion timings and streaming client | Bench and later chat | M3.1, L1 | M3 |
| Log/event scanner | Crash reasons, alerts, request rows | M4.2–M4.4 | M4 |
| Stable JSON schemas | Scripting, API, golden tests | M5.6 | M5 |

## Risks and open questions

Risks:

- **Calibration is confounded.** Other GPU users, mmap faulting and driver
  accounting can make observed VRAM noisy. Store baselines, show sample counts,
  clamp corrections and never hard-block a launch from a corrected estimate.
- **`llama-server` API drift.** `/metrics`, `/slots`, router endpoints and log
  formats are not a stable public contract. Feature-detect, degrade gracefully,
  and record the tested version in Doctor.
- **MoE tensor naming.** Architectures differ. Keep the mapping in one place,
  test against real metadata, and fall back conservatively.
- **Benchmark noise.** Thermals and background load dominate small differences.
  Warm up, interleave A/B runs, report spread and avoid significance claims.
- **Config editing corruption.** The line-based writer is deliberately small.
  Add tests for every new key type and refuse multi-line values.
- **Supervisor complexity.** M4.4 can turn into a process manager. Keep the
  helper minimal and point users at systemd for real service management.
- **JSONL growth.** Observations and benchmark runs accumulate. Rotate at a
  documented size or record count; migrate to SQLite only when needed.
- **Persona drift.** The target is one 8–16 GB GPU. Resist multi-GPU and
  multi-user complexity until the primary loop is trusted.
- **Platform claims.** Do not list NVIDIA or Intel as supported until there is
  hardware and a test path. A broken dashboard is worse than no dashboard.
- **Single-maintainer bandwidth.** One milestone at a time; vertical slices;
  ship v0.2 before starting M3.
- **Scope creep from chat and downloads.** Keep them in Later Bets until the
  core loop is measured and trusted.

Open questions to resolve before or during M1:

1. What is the minimum supported `llama-server` version? Doctor should report
   it and the roadmap should name it.
2. Should calibration be on by default for display, or opt-in? Current plan:
   display-only when enough samples exist, opt-in for fit.
3. Is a detached supervisor acceptable, or should restart always be delegated
   to systemd? Current plan: in-session restart in M4, detached supervisor only
   if users need it.
4. Which notification targets matter: `notify-send`, a webhook, or both?
5. Does the router expose a definitive eviction policy? Verify before M4.5;
   otherwise label the view as a prediction.
6. What default memory reserve should `wool fit` use for an 8 GB card versus a
   16 GB card? The current router default is 512 MiB.
7. Should model discovery follow symlinks, or skip them by default to avoid
   loops and duplicate paths?

## Appendix: backlog at a glance

Status values: `planned`, `in progress`, `shipped`, `blocked`, `dropped`.
Update this table and the [feature index](features/README.md) at each milestone
boundary.

| ID | Item | Milestone | Priority | Size | Depends | Status |
| --- | --- | --- | --- | --- | --- | --- |
| M1.1 | [Override provenance and reset](features/m1-01-override-provenance-and-reset-to-inherited.md) | M1 | P0 | M | — | planned |
| M1.2 | [Estimate breakdown and what-if](features/m1-02-estimate-breakdown-and-what-if-preview.md) | M1 | P0 | M | — | planned |
| M1.3 | [Observed-vs-estimate capture](features/m1-03-observed-vs-estimate-capture.md) | M1 | P1 | S/M | — | planned |
| M1.4 | [Effective-config diff](features/m1-04-effective-config-diff.md) | M1 | P2 | S | M1.1 | planned |
| M2.1 | [MoE expert offload modeling](features/m2-01-moe-expert-offload-modeling.md) | M2 | P1 | L | — | planned |
| M2.2 | [Auto-fit solver](features/m2-02-auto-fit-solver.md) | M2 | P1 | M/L | M1.2, M2.1 | planned |
| M2.3 | [Save recommendation as a preset](features/m2-03-save-a-recommendation-as-a-preset.md) | M2 | P2 | S | M2.2 | planned |
| M3.1 | [`wool bench`](features/m3-01-wool-bench.md) | M3 | P1 | L | M1.3 | planned |
| M3.2 | [Benchmark history and trends](features/m3-02-benchmark-history-and-trends.md) | M3 | P1 | M | M3.1 | planned |
| M3.3 | [A/B comparison](features/m3-03-a-b-comparison.md) | M3 | P2 | M | M3.1, M3.2 | planned |
| M3.4 | [Calibration correction](features/m3-04-calibration-correction.md) | M3 | P1 | M | M1.3, M3.1 | planned |
| M3.5 | [Smoke test on launch](features/m3-05-smoke-test-on-launch.md) | M3 | P2 | S | — | planned |
| M4.1 | [Live inference panel](features/m4-01-live-inference-panel.md) | M4 | P1 | M | — | planned |
| M4.2 | [Structured request log](features/m4-02-structured-request-log.md) | M4 | P2 | M | M4.1 | planned |
| M4.3 | [Alerts](features/m4-03-alerts.md) | M4 | P1 | M | M4.1 | planned |
| M4.4 | [Crash detection and restart policy](features/m4-04-crash-detection-and-restart-policy.md) | M4 | P1 | L | — | planned |
| M4.5 | [Router eviction visualization](features/m4-05-router-eviction-visualization.md) | M4 | P2 | M | M2.1 | planned |
| M4.6 | [Warm sets and profiles](features/m4-06-warm-sets-and-profiles.md) | M4 | P2 | M | M4.5 | planned |
| M4.7 | [Router usage statistics](features/m4-07-router-usage-statistics.md) | M4 | P2 | S/M | M1.3, M4.1 | planned |
| M4.8 | [Endpoint helper](features/m4-08-endpoint-helper.md) | M4 | P3 | S | — | planned |
| M5.1 | [Model discovery](features/m5-01-model-discovery.md) | M5 | P1 | M | — | planned |
| M5.2 | [Disk and orphan view](features/m5-02-disk-and-orphan-view.md) | M5 | P2 | S | M5.1 | planned |
| M5.3 | [Export as a service](features/m5-03-export-a-preset-as-a-service.md) | M5 | P2 | S/M | — | planned |
| M5.4 | [Config validation with line numbers](features/m5-04-config-validation-with-line-numbers.md) | M5 | P2 | S/M | — | planned |
| M5.5 | [Preset cloning and templates](features/m5-05-preset-cloning-and-templates.md) | M5 | P3 | M | M1.1 | planned |
| M5.6 | [Machine-readable surface](features/m5-06-machine-readable-surface.md) | M5 | P2 | M/L | M1.3, M3.1 | planned |
| M5.7 | [Hooks](features/m5-07-hooks.md) | M5 | P3 | M | M4.4 | planned |
| PLAT-1 | [NVIDIA telemetry backend](features/platform-01-nvidia.md) | Platform | P3 | M/L | — | blocked |
| PLAT-2 | [Multi-GPU awareness](features/platform-02-multi-gpu.md) | Platform | P3 | L | PLAT-1, M2.1 | blocked |
| PLAT-3 | [Intel GPU telemetry](features/platform-03-intel.md) | Platform | P3 | M | — | blocked |

## Maintaining this document

- The roadmap owns sequence and priority; feature documents own scope,
  implementation notes and acceptance criteria.
- At the start of a milestone, set its features to `in progress` in the
  [feature index](features/README.md) and in each spec.
- At the end, update “Where we are”, mark features `shipped`, and re-check the
  Later Bets triggers.
- If a feature does not fit its milestone, move it to the next one rather than
  expanding the milestone. The sequence is the plan.
- To promote a Later Bet, copy [`features/_template.md`](features/_template.md),
  assign the next ID, add it to the feature index, and add it to a milestone
  here. If the trigger has not happened, do not promote it.
