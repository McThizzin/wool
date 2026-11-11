# wool

`wool` is a terminal UI and CLI for running [`llama-server`](https://github.com/ggml-org/llama.cpp) on a Linux workstation. It is a single control surface for standalone launches and llama.cpp router mode: every server runs detached, survives the UI, and can be reattached later.

<p align="center">
  <img src="assets/wool_logo.png" width="100%">
</p>

- **Single-server mode** — launch one preset or model, detached, then inspect logs and status from a later invocation.
- **Router mode** — run `llama-server` in router mode with a generated `models.ini`, then load, unload, and inspect models through the router API.

<p align="center">
  <img src="assets/wool_screenshot.png" width="100%">
</p>

## Features

- **Detached servers by default.** `run`, `start`, and router launches use `setsid`; quitting the TUI never stops inference. State is stored as JSON and processes are validated by PID start tick before they are signalled.
- **One TOML source of truth.** Models, presets, launch options, sampling, router settings, and themes all live in `config.toml`. There is no legacy `presets.conf` importer.
- **Effective-config diff.** `wool config diff TARGET` and the TUI `D` overlay show only the launch options that differ from the model or defaults layer, with the source of each side.
- **Full router control.** Start/stop the router, list models, load/unload, reload presets, and read per-model stats and slots.
- **VRAM-fit estimates.** Reads GGUF metadata and the tensor table, models llama.cpp's tensor placement, Gemma-style sliding-window/shared KV, hybrid attention (Qwen3.5/Qwen3-Next), per-layer KV heads, and quantized KV cache types. The UI shows `weights + KV + overhead`, the budget after reserve and other GPU usage, headroom and the limiting term.
- **Auto-fit recommendations.** `wool fit TARGET` binary-searches the largest context that fits the budget, capped at the model's native context. When the requested settings cannot hold a useful context it ranks fallbacks: lower KV cache precision first, then CPU offload (using `n_cpu_moe` for MoE models). Every recommendation is within budget and includes weights, KV, overhead, headroom, the limiting term and the changes it makes. `--save-preset NAME` writes the result as a new preset.
- **Benchmarks and history.** `wool bench TARGET` starts an ephemeral server, warms up, and records prompt and generation throughput over repeated runs with median/min/max and peak VRAM. It can attach to a matching managed server or any `--url`, and optionally drive `llama-bench`. `wool bench history` and the Bench tab show past runs, trends and a two-run comparison. `wool bench compare A B` runs two targets interleaved and reports the median, spread and percentage delta.
- **Observed vs. estimate.** After a launch, `wool` records used VRAM at 5s and 15s and shows it next to the estimate with a sample count. With at least three matching samples, the dashboard and doctor also show a median correction, clamped to 0.5–2.0; the raw estimate is always kept. Samples expire when the model file, GPU, driver or `llama-server` version changes.
- **Hardware telemetry.** CPU, RAM, AMD `amdgpu` VRAM, temperature, power, fan, and sparklines, with no extra daemon required.
- **Preset and model editing.** Edit resolved launch options in the TUI; changes are written back to TOML with comments and ordering preserved. Changing `ctx`, cache type, `gpu_layers` or `n_cpu_moe` previews the new estimate without saving.
- **Seven tabs, themes, commands.** Dashboard, Models, Presets, Router, Hardware, Logs, Bench; built-in and custom themes; slash commands and completion.
- **Doctor.** Checks the binary, model files, hardware, and VRAM fit before you launch.

## Requirements

- Linux (AMD `amdgpu` telemetry is implemented first; the telemetry layer is pluggable).
- Go 1.27+ to build.
- `llama-server` from llama.cpp in `PATH`, or set `app.llama_server` in the config.
- GGUF model files. The example config points at `~/models`.

## Build and install

```sh
go build -o wool ./cmd/wool
install -m 0755 wool ~/.local/bin/wool
```

For a quick development build:

```sh
go run ./cmd/wool --config config.example.toml
```

## Quick start

```sh
# Create ~/.config/wool/config.toml from the example.
wool config init

# Edit model paths, then verify the setup.
$EDITOR ~/.config/wool/config.toml
wool doctor

# Open the TUI.
wool

# Or drive a launch directly.
wool run qwen-cyber-32k
wool status --json
wool logs server -n 20
wool stop server
```

`wool run TARGET` uses a preset name or a model ID. The process is started detached; `--no-wait` returns immediately, `--dry-run` prints the `llama-server` argv, and `--restart` replaces an existing standalone server. `--smoke` sends one tiny prompt after health to prove the preset can generate; `--strict` makes a failed check stop the server and exit non-zero. The check is off by default and can be enabled persistently with `[server] smoke_test = true`.

## CLI

```text
wool [--config PATH]                 open the TUI
wool doctor [--json]                 check binary, models and VRAM fit
wool fit TARGET [options]            recommend settings that fit a VRAM budget
wool bench TARGET [options]          benchmark prompt and generation throughput
wool bench --url URL --model ID      benchmark an already-running server
wool bench history [TARGET]          show recorded benchmark runs
wool bench compare A B [options]     interleaved A/B benchmark comparison
wool list [--json]                   list models and presets
wool run TARGET [options]            start a model or preset detached
wool stop [server|router|all]        stop detached servers
wool status [name] [--json]          show detached server state
wool logs [name] [-n N] [-f]         show a server log
wool router [start|stop|status|models|load ID|unload ID|reload|ini]
wool config path                     print the config path
wool config init [--force] [PATH]    write a sample config
wool config explain TARGET [--json]  show each value and where it came from
wool config diff TARGET [--against defaults|model] [--json]
wool version
```

Router start accepts `--dry-run`, `--restart`, `--port N`, and `--no-wait`.

`wool fit TARGET` accepts a preset name or model ID plus `--vram SIZE`,
`--cache-type TYPE`, `--gpu-layers N`, `--n-cpu-moe N`, `--save-preset NAME`,
`--force`, `--calibrated` and `--json`. It prints a ranked recommendation and
alternatives, the estimate breakdown, headroom, the limiting term and any
changes from the requested settings. `--vram 7G` means a 7 GiB card: the solver
subtracts `router.memory_margin_mib` and ignores live usage from other
processes, so output is deterministic. Without `--vram` it uses the live GPU
budget and excludes a running standalone server's own allocation.

`--calibrated` attaches the median observed/estimate correction for each
recommendation's launch shape and labels the output as calibrated. The raw
estimate and solver verdict stay visible; the corrected value and headroom are
additional. Below three matching samples no correction is applied.

`--save-preset NAME` writes the primary recommendation as `[presets.NAME]`
with the model reference plus `ctx`, `gpu_layers`, `n_cpu_moe`,
`cache_type_k` and `cache_type_v`. The rest of the TOML file, including
comments and other presets, is preserved. An existing name is only replaced
with `--force`; `--force` without `--save-preset` is rejected.

```sh
wool fit qwen-cyber-32k --vram 8G
wool fit lfm25-24b-a2b --vram 8G --json
wool fit lfm3-1-7b --vram 8G --cache-type q8_0 --gpu-layers 32
wool fit lfm3-1-7b --vram 8G --save-preset lfm-fitted
wool fit lfm3-1-7b --vram 8G --save-preset lfm-fitted --force
```

The minimum useful context is 2048 tokens and recommendations snap to a
256-token grid. If the requested settings cannot fit 2048 tokens, the solver
tries lower KV cache precision, then CPU offload, then both. For MoE models it
prefers `n_cpu_moe`; all-CPU placement is a last resort. If no combination
fits, the command exits with an actionable error instead of returning an
over-budget configuration. Recommendations are advisory: use `F` in the editor
to apply one to the draft, then `ctrl+s` to save the target's options, or `p`
in the fit overlay to save it as a new preset.

`wool bench TARGET` accepts a preset name or model ID plus `--runs N`
(default 3), `--pp N` (prompt tokens, default 512), `--tg N` (generated
tokens, default 128), `--engine server|llama-bench`, `--no-warmup` and
`--json`. The default engine starts an ephemeral `llama-server` on a free port,
waits for health, warms up, and sends the same temperature-0/seed-42 prompt for
each run. If a managed `server` is already running with the same model and
launch shape, it attaches instead of starting another. `--url URL --model ID`
benchmarks an existing server without starting one. The ephemeral child is
stopped on completion, error or `Ctrl-C`.

Throughput prefers the completion response's `timings`, falls back to
`/metrics` counter deltas, and finally to wall time; each raw run records which
source was used. The command prints the median, minimum and maximum and appends
one record with `kind: "bench"` to the measurement store. `--json` prints that
record, including every raw run. The optional `llama-bench` engine uses the
binary next to the configured `llama-server` or on `PATH`; the default engine
does not need it.

```sh
wool bench qwen-cyber-32k
wool bench lfm3-1-7b --runs 5 --pp 1024 --tg 256
wool bench qwen-cyber-32k --engine llama-bench
wool bench --url http://127.0.0.1:8080 --model qwen-cyber-32k --json
```

`wool bench history [TARGET]` reads the same store and prints the newest runs
(default 20) with timestamp, target, prompt and generation tokens/s, VRAM, load
time and config hash. A target may be a preset name or model ID; omit it to show
every benchmark. `--limit N` changes the row count and `--json` prints the
matching records. The store keeps the newest 5000 records: after an append pushes
it over the cap, the oldest lines are dropped and the file is rewritten
atomically. Writers serialize on `runs.jsonl.lock`, so a benchmark in one process
cannot race a rotation in another.

```sh
wool bench history
wool bench history qwen-cyber-32k --limit 10
wool bench history --json
```

`wool bench compare A B` measures two presets or models in the same session.
`--runs N` is the number of rounds per target (default 3), so it performs `2N`
measured completions. Rounds alternate order (`A B B A …`) to reduce thermal
drift, and both targets use the same prompt, seed and generation length. Each
round starts a fresh ephemeral server, so the two models never occupy VRAM
together; each side is stored as its own `kind: "bench"` record. The report shows
the median, relative spread and percentage delta for prompt and generation
throughput, the raw generation runs, and warnings when the spread is above 10%
or the GPU, driver or `llama-server` version changed between sides. `--json`
prints the stable comparison schema (version 1). `--url` and `--model` are not
supported here; compare resolves both targets from the config.

```sh
wool bench compare qwen-cyber-32k qwen-cyber-65k
wool bench compare lfm3-1-7b lfm3-1-7b-q8 --runs 5 --json
```

In the TUI, `B` opens the Bench tab. It shows prompt and generation sparklines
over the filtered runs, a run table, and full details for the selected run.
`space` marks a run, `enter` compares the marked and selected runs, `f` cycles
the target filter, and `D` opens the M1.4 config diff for the selected run's
target. `/bench compare A B` opens the newest recorded run of each target in the
same comparison overlay.

## Configuration

The default config is `~/.config/wool/config.toml` (XDG-aware). `config.example.toml` is a complete, commented starting point.

```toml
[app]
theme = "mocha"              # built-in or a [themes.<name>] custom theme
nerd_fonts = true
model_dirs = ["~/models"]
llama_server = "llama-server"
log_lines = 10000

[server]
host = "127.0.0.1"
port = 8080
auto_port = true
startup_timeout = "120s"
poll_interval = "5s"         # TUI refresh; clamped to 250ms..1m
smoke_test = false           # send one tiny prompt after health
smoke_timeout = "30s"        # bounds the whole smoke request

[defaults]
gpu_layers = 999
threads = 0                  # 0 = nproc
cache_type_k = "q4_0"
cache_type_v = "q4_0"
flash_attn = "on"

[models.qwen3-4b-cyber]
path = "~/models/Qwen3-4B-cybersecurity-heretic.gguf"
alias = "Qwen-Cyber"
ctx = 65536

[presets.qwen-cyber-32k]
model = "qwen3-4b-cyber"
ctx = 32768

[router]
models_max = 1
memory_margin_mib = 512
autoload = false
sleep_idle_seconds = -1
metrics = true
```

Launch options are inherited in this order:

```text
built-in defaults -> [defaults] -> [models.<id>] -> [presets.<name>]
```

Unset keys inherit; explicit zero/false values are preserved. `threads = 0` resolves to `runtime.NumCPU()`, and `threads_batch = 0` follows `threads`.

`wool config explain TARGET` shows every effective launch option with the layer that supplied it (`built-in`, `defaults`, `model` or `preset`). Add `--json` for `value`, `source` and `layer` fields. In the editor, the same source is shown next to each row; `backspace` or `delete` removes that override and falls back to the next layer.

`wool config diff TARGET [--against defaults|model]` shows only the options whose resolved value differs from the comparison layer. A preset defaults to its model, so the result is what the preset actually changes; a model defaults to the defaults layer. `--json` includes the value, source and layer for both `target` and `against`, and `fields` is empty when nothing differs. The TUI `D` key opens the same view for the selected model or preset.

The dashboard and `wool doctor` decompose the estimate into weights, KV cache and overhead, then compare it with the budget for a new server: GPU total minus `router.memory_margin_mib` minus VRAM used by other processes. A running standalone server's own allocation is excluded so a restart is not charged twice. `wool doctor --json` includes the same `estimate`, `budget`, `headroom_bytes` and `limiting_term` fields. While editing, the preview updates as you type; `enter` commits and `esc` discards.

Successful launches also record one observation in `$XDG_STATE_HOME/wool/measurements/runs.jsonl`: a pre-launch VRAM baseline, samples at 5s and 15s after health, and the estimate for that exact launch. The dashboard and doctor show `estimate 6.1 GiB · observed 6.4 GiB (n=3)`; with at least three matching samples they add `corrected 6.4 GiB (n=3, ±8%)`. The observation is a baseline-subtracted delta, not an exact model footprint, and it is recorded even when the baseline is unknown (shown as `observed unknown`). The correction is the median observed/estimate ratio, clamped to 0.5–2.0, and it is display-only unless `wool fit --calibrated` is used. Samples are ignored when the model file, GPU, driver or `llama-server` version changed. A smoke test adds `verified_at` on success plus `smoke_ms` and `smoke_error`; the dashboard shows `✓ verified` or the short failure reason for the newest attempt. `wool bench` appends records with `kind: "bench"` to the same file; they carry the raw runs, throughput summary and peak VRAM, and are ignored by calibration.

### Custom themes

```toml
[themes.dusk]
base = "tokyo"               # built-in or another custom theme

[themes.dusk.colors]
accent = "#ff9e64"
border = "#565f89"
```

Supported color keys: `border`, `accent`, `text`, `dim`, `badge_fg`, `red`, `yellow`, `green`, `cyan`, `blue`, `mauve`, `pink`, `orange`, `teal`. Colors accept `#rgb`, `#rrggbb`, `#rrggbbaa`, or a terminal index `0`–`255`. Use `t` to cycle themes or `/theme <name>` to select one.

## TUI

| Key | Action |
| --- | --- |
| `tab` / `shift+tab` | next / previous tab |
| `1`..`7` | jump to tab |
| `j` / `k`, arrows | move selection or scroll logs |
| `g` / `G` | first / last entry; in logs, tail or follow |
| `enter` | start target, load router model, switch log target, or compare bench runs |
| `s` / `x` | start / stop the standalone server |
| `S` | restart the selected target |
| `r` / `R` | start / stop the router |
| `l` / `u` | load / unload the selected router model |
| `m` | show stats for the selected router model |
| `L` | focus logs |
| `B` | focus the Bench history tab |
| `space` | mark a benchmark run for comparison |
| `f` | toggle log follow, or cycle the Bench target filter |
| `e` | edit the selected preset or model; `backspace`/`delete` resets a row |
| `F` | recommend settings that fit VRAM in the editor |
| `p` | save the selected recommendation as a preset (in the fit overlay; `P` replaces) |
| `d` | run doctor |
| `D` | show the selected config diff, or the Bench run's target diff |
| `t` | cycle theme |
| `ctrl+r` | reload config.toml |
| `/` or `:` | command line |
| `?` | help overlay |
| `q` / `ctrl+c` | quit without stopping servers |

Slash commands include `/start`, `/restart`, `/stop`, `/router`, `/load`, `/unload`, `/reload`, `/edit`, `/fit`, `/doctor`, `/theme`, `/tab`, `/bench`, `/logs`, `/help`, and `/quit`.

## Router mode

Router mode writes a deterministic `models.ini` with one section per `[models.*]` entry. `[defaults]` becomes `[*]`, and each model section contains its resolved options. The file is regenerated on router start, `router reload`, and the TUI reload action.

Only router-level options are passed on the command line:

```text
--host H --port P --models-preset models.ini --models-max N
[--models-autoload | --no-models-autoload]
[--sleep-idle-seconds N] [--metrics] [--fit-target MiB]
```

```sh
wool router start
wool router models
wool router load qwen3-4b-cyber
wool router unload qwen3-4b-cyber
wool router reload
wool router stop
```

## Runtime files

```text
~/.local/state/wool/
  servers/server.json       # standalone server state
  servers/router.json       # router state
  logs/server.log
  logs/router.log
  router/models.ini         # generated, do not edit
  measurements/runs.jsonl   # observations and benchmarks, append-only
  measurements/runs.jsonl.lock  # writer lock for safe rotation
```

Logs are tailed from disk with a bounded read, so reattaching is cheap even for large logs. The measurement store keeps the newest 5000 records; older lines are compacted away after an append.

## VRAM estimate

`wool` estimates VRAM from the GGUF key/value block and tensor-info table: each tensor is sized from its type and dimensions, then classified as a block, input, output or repeating tensor. Placement follows llama.cpp: `gpu_layers` counts the output layer, input embeddings always stay in host memory, output tensors follow the output layer, and repeating tensors follow layer 0. `n_cpu_moe` moves expert bytes independently.

The KV cache comes from context, attention heads, cache type and the layer schedule. Hybrid models such as Qwen3.5/Qwen3-Next only keep KV for every `full_attention_interval`-th layer. Sliding-window layers are charged `min(ctx, sliding_window)` with their SWA head widths, while global layers keep the full context; trailing shared-KV layers allocate nothing. Gemma 2/3/3n defaults are applied when the file omits the pattern. A sliding window without a known pattern is charged full context and marked approximate.

For MoE models, `n_cpu_moe = N` keeps the routed-expert tensors (`ffn_*_exps`) of the first `N` layers on the CPU; routers and shared experts stay with their layer. If the tensor table is missing, a quantisation type or tensor name is unsupported, or expert tensors cannot be identified, `wool` falls back to the file-size fraction and marks the estimate approximate. Doctor and the editor preview explain why. A fixed overhead covers compute buffers and allocator slack. The estimate is deliberately conservative and still omits unused MTP tensors and speculative decoding buffers, so it is a fit warning rather than an exact measurement.

`wool fit` inverts that estimate. It searches for the largest context on a 256-token grid that fits the budget, up to the model's native context. The requested settings are tried first; when they cannot hold at least 2048 tokens, the solver steps down through cache precision, CPU offload and finally both. The result is deterministic: the same config, model file and `--vram` value produce the same ranking. Add `--calibrated` to attach the measured correction to each recommendation; the raw estimate and fit verdict remain the primary result. In the editor, `F` opens the picker against the live budget; `enter` applies the selection to the draft, and `ctrl+s` writes only the changed `ctx`, `gpu_layers`, `n_cpu_moe`, `cache_type_k` and `cache_type_v` values. `p` saves the selected recommendation as a new preset without touching the draft; an existing name requires `P` to replace it.

## Project layout

```text
cmd/wool/          CLI parsing, subcommands, TUI entry point
internal/app/      Bubble Tea model, views, actions, editor
internal/config/   TOML schema, merging, path handling, config edits
internal/daemon/   detached process lifecycle, state, ports, logs
internal/llamaserver/ argv/INI generation and HTTP API client
internal/gguf/     GGUF metadata reader and VRAM estimate
internal/fit/      auto-fit solver, fallback ranking and size parsing
internal/measure/  observed VRAM and benchmark JSONL store, launch capture
internal/bench/    server and llama-bench runners, summaries, VRAM sampling, A/B comparison
internal/sysinfo/  /proc and AMD sysfs telemetry
internal/doctor/   readiness checks
internal/theme/    palettes, styles, icons, custom themes
internal/ui/       panel, bar, sparkline and layout helpers
docs/DESIGN.md     architecture and behavior details
docs/roadmap.md    sequenced milestones, priorities and exit criteria
docs/features/     one spec per planned feature, plus the feature index
docs/RELEASING.md  release checklist and tagging steps
.github/workflows/ CI and tagged release automation
```

## Testing

```sh
gofmt -l .
go build ./...
go vet ./...
go test -count=1 ./...
```

Tests cover config parsing/merging/editing, effective-config diffs, GGUF metadata and tensor parsing, hybrid KV and MoE expert-offload estimation, dense/hybrid/MoE auto-fit solving and fallback ranking, measurement storage and capture, benchmark history filtering and store rotation, benchmark runners and `llama-bench` parsing, interleaved A/B comparison and its noise warnings, argv and INI generation, router metrics, daemon lifecycle and PID validation, sysinfo parsing, themes, and TUI rendering at multiple terminal sizes. Benchmark tests use a fake HTTP server and temporary state; none require `llama-bench`.

## Limitations

- Only AMD sysfs telemetry is implemented; NVIDIA/Intel need new backends.
- One standalone server and one router are managed per state directory.
- The editor can reset launch-option overrides, but not `model`, `path`, `alias` or `extra` keys.
- Standalone servers are not automatically restarted; router-managed models are supervised by `llama-server` itself.
- VRAM estimates fall back to a file-size approximation, and say so, when tensor metadata, a quantisation type or a tensor name is unsupported, or when a sliding-window layer pattern is unknown. Speculative decoding buffers are not modelled.
- `wool fit` is advisory and inherits the estimator's approximation. It never recommends an over-budget configuration, but the recommendation is a starting point with headroom, not a guarantee.
- `wool fit --save-preset` and the editor's `p` action write a preset from the fit-controlled values only. Replacing an existing preset updates those keys and leaves its other options and comments alone; there is no preset-diff preview before writing.
- Observations and benchmark peak VRAM are system-wide deltas; background GPU work makes them noisy. They are never presented as exact usage, and an arbitrary `--url` server cannot report a VRAM delta.
- Benchmarks are single-stream and short by default. They measure one fixed prompt rather than concurrent load, and prompt length is approximated by repeating a sentence instead of tokenizing.
- A/B comparison warns when spread is above 10%, but that is a coarse guard, not a significance test. Small deltas on a shared machine can still be noise; use more rounds before trusting them.
- The measurement store is bounded at 5000 records. The oldest runs are dropped without warning; export anything you need to keep with `wool bench history --json`.

See [`docs/DESIGN.md`](docs/DESIGN.md) for the full design, lifecycle, and config reference, and [`docs/roadmap.md`](docs/roadmap.md) for what is planned and in what order.
