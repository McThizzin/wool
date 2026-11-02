# wool

`wool` is a terminal UI and CLI for running [`llama-server`](https://github.com/ggml-org/llama.cpp) on a Linux workstation. It is a single control surface for standalone launches and llama.cpp router mode: every server runs detached, survives the UI, and can be reattached later.

- **Single-server mode** — launch one preset or model, detached, then inspect logs and status from a later invocation.
- **Router mode** — run `llama-server` in router mode with a generated `models.ini`, then load, unload, and inspect models through the router API.

```text
   [1] Dashboard  [2] Models  [3] Presets  [4] Router  [5] Hardware  [6] Logs      wool
╭─ Dashboard ──────────────────────────────────────────── server up ─╮ ╭─ Status ──────────╮
│ qwen-cyber-32k  preset                                             │ │ SERVER            │
│ /home/user/models/Qwen3-4B-cybersecurity-heretic.gguf              │ │ running           │
│ model qwen3-4b-cyber   ctx 32768   gpu layers 999                  │ │ ...               │
│ budget 4.1 GiB / 5.5 GiB  ██████████████░░░░  76% fits             │ │ ROUTER            │
╰────────────────────────────────────────────────────────────────────╯ ╰───────────────────╯
 press / to type a command                                              mocha
 tab switch · j/k move · enter start · x stop · r router · d doctor     120x40
```

## Features

- **Detached servers by default.** `run`, `start`, and router launches use `setsid`; quitting the TUI never stops inference. State is stored as JSON and processes are validated by PID start tick before they are signalled.
- **One TOML source of truth.** Models, presets, launch options, sampling, router settings, and themes all live in `config.toml`. There is no legacy `presets.conf` importer.
- **Effective-config diff.** `wool config diff TARGET` and the TUI `D` overlay show only the launch options that differ from the model or defaults layer, with the source of each side.
- **Full router control.** Start/stop the router, list models, load/unload, reload presets, and read per-model stats and slots.
- **VRAM-fit estimates.** Reads GGUF metadata, supports hybrid attention (Qwen3.5/Qwen3-Next), per-layer KV heads, and quantized KV cache types. The UI shows `weights + KV + overhead`, the budget after reserve and other GPU usage, headroom and the limiting term.
- **Observed vs. estimate.** After a launch, `wool` records used VRAM at 5s and 15s and shows it next to the estimate with a sample count.
- **Hardware telemetry.** CPU, RAM, AMD `amdgpu` VRAM, temperature, power, fan, and sparklines, with no extra daemon required.
- **Preset and model editing.** Edit resolved launch options in the TUI; changes are written back to TOML with comments and ordering preserved. Changing `ctx`, cache type, `gpu_layers` or `n_cpu_moe` previews the new estimate without saving.
- **Six tabs, themes, commands.** Dashboard, Models, Presets, Router, Hardware, Logs; built-in and custom themes; slash commands and completion.
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

`wool run TARGET` uses a preset name or a model ID. The process is started detached; `--no-wait` returns immediately, `--dry-run` prints the `llama-server` argv, and `--restart` replaces an existing standalone server.

## CLI

```text
wool [--config PATH]                 open the TUI
wool doctor [--json]                 check binary, models and VRAM fit
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

Successful launches also record one observation in `$XDG_STATE_HOME/wool/measurements/runs.jsonl`: a pre-launch VRAM baseline, samples at 5s and 15s after health, and the estimate for that exact launch. The dashboard and doctor show `estimate 6.1 GiB · observed 6.4 GiB (n=3)`. The observation is a baseline-subtracted delta, not an exact model footprint, and it is recorded even when the baseline is unknown (shown as `observed unknown`). No correction factor is applied.

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
| `1`..`6` | jump to tab |
| `j` / `k`, arrows | move selection or scroll logs |
| `g` / `G` | first / last entry; in logs, tail or follow |
| `enter` | start target, load router model, or switch log target |
| `s` / `x` | start / stop the standalone server |
| `S` | restart the selected target |
| `r` / `R` | start / stop the router |
| `l` / `u` | load / unload the selected router model |
| `m` | show stats for the selected router model |
| `L` | focus logs |
| `f` | toggle log follow |
| `e` | edit the selected preset or model; `backspace`/`delete` resets a row |
| `d` | run doctor |
| `t` | cycle theme |
| `ctrl+r` | reload config.toml |
| `/` or `:` | command line |
| `?` | help overlay |
| `q` / `ctrl+c` | quit without stopping servers |

Slash commands include `/start`, `/restart`, `/stop`, `/router`, `/load`, `/unload`, `/reload`, `/edit`, `/doctor`, `/theme`, `/tab`, `/logs`, `/help`, and `/quit`.

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
```

Logs are tailed from disk with a bounded read, so reattaching is cheap even for large logs.

## VRAM estimate

`wool` estimates VRAM from the GGUF key/value block: weights from the file size and offloaded layer fraction, KV cache from context, attention heads, cache type, and full-attention layer schedule, plus a fixed overhead for compute buffers and allocator slack.

Hybrid models such as Qwen3.5/Qwen3-Next only keep KV for every `full_attention_interval`-th layer; `wool` models that instead of charging KV for every block. The estimate is deliberately conservative and does not include MoE CPU offload, unused MTP tensors, or speculative decoding buffers, so it is a fit warning rather than an exact measurement.

## Project layout

```text
cmd/wool/          CLI parsing, subcommands, TUI entry point
internal/app/      Bubble Tea model, views, actions, editor
internal/config/   TOML schema, merging, path handling, config edits
internal/daemon/   detached process lifecycle, state, ports, logs
internal/llamaserver/ argv/INI generation and HTTP API client
internal/gguf/     GGUF metadata reader and VRAM estimate
internal/measure/  observed VRAM JSONL store and launch capture
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

Tests cover config parsing/merging/editing, effective-config diffs, GGUF metadata and hybrid KV estimation, measurement storage and capture, argv and INI generation, router metrics, daemon lifecycle and PID validation, sysinfo parsing, themes, and TUI rendering at multiple terminal sizes.

## Limitations

- Only AMD sysfs telemetry is implemented; NVIDIA/Intel need new backends.
- One standalone server and one router are managed per state directory.
- The editor can reset launch-option overrides, but not `model`, `path`, `alias` or `extra` keys.
- Standalone servers are not automatically restarted; router-managed models are supervised by `llama-server` itself.
- VRAM estimates remain conservative for MoE CPU offload and speculative decoding.
- Observations are system-wide VRAM deltas; background GPU work makes them noisy. They are never presented as exact usage.

See [`docs/DESIGN.md`](docs/DESIGN.md) for the full design, lifecycle, and config reference, and [`docs/roadmap.md`](docs/roadmap.md) for what is planned and in what order.
