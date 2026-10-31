# wool design

`wool` is a Go terminal UI and CLI for running `llama-server` on a
Linux workstation. It replaces the earlier `llama_launch` / `llama_router`
shell scripts while keeping their two useful modes:

- **single-server mode**: launch one preset or model, detached, and reattach
  to it from a later invocation;
- **router mode**: run `llama-server` in router mode with a generated
  `models.ini`, then load and unload models through the router API.

The TOML config is the single source of truth. There is intentionally no
importer for the old `presets.conf`; the initial `config.example.toml` is a
hand-translated starting point, not a compatibility layer.

## Goals and non-goals

Goals:

- one config file for models, presets, defaults and router settings;
- servers survive the TUI and CLI exiting;
- useful model browsing, VRAM-fit estimates and preset editing from the TUI;
- hardware telemetry for CPU, RAM and AMD GPUs;
- a hardware layer that can grow other backends later.

Non-goals for v1:

- a custom process supervisor for model children (the router does that);
- multi-user or remote-server management;
- downloading models;
- exact VRAM prediction (the estimate is deliberately conservative);
- NVIDIA/Intel sensor support (the interfaces exist, the Linux AMD backend is
  the only implementation today).

## Process model

```text
wool (TUI or CLI)
        │
        ├── daemon.Manager ──> detached llama-server --models-preset models.ini
        │                         └── router-managed model children
        │
        └── daemon.Manager ──> detached llama-server --model <gguf>
```

The TUI/CLI process is not the parent after startup. `Manager.Start` uses
`Setsid`, writes a state file, and returns. A later `wool` process
finds the state file, verifies the PID start time, and reconnects to the HTTP
API. Quitting the TUI never sends a signal to a server.

A single detached instance named `server` and at most one named `router` are
supported. The router itself owns the lifecycle of the models it loads.

## Package map

| Package | Responsibility |
| --- | --- |
| `cmd/wool` | CLI parsing, subcommands, TUI entry point |
| `internal/app` | Bubble Tea model, tabs, overlays, key handling, actions |
| `internal/config` | TOML schema, defaults, merge/resolve, validation, paths, comment-preserving edits |
| `internal/daemon` | detached process start/stop, state files, liveness checks, ports, log tailing |
| `internal/llamaserver` | single-server argv, router argv, `models.ini` generation, HTTP client |
| `internal/gguf` | GGUF metadata reader and VRAM estimate |
| `internal/sysinfo` | Linux `/proc` + AMD sysfs telemetry and graph history |
| `internal/doctor` | readiness checks and report rendering |
| `internal/ui` | ANSI-aware width fitting, panels, bars, sparklines |
| `internal/theme` | palettes, styles and icon sets |
| `ref/esp_term` | layout/keybinding reference, not imported |

## Configuration

### Location

The default config path is:

```text
$XDG_CONFIG_HOME/wool/config.toml
# or ~/.config/wool/config.toml
```

`wool config path` prints it. `wool config init` writes the
embedded example. `--config PATH` (or `-c PATH`) overrides it and is accepted
before or after the subcommand.

Runtime data lives under the XDG state directory:

```text
~/.local/state/wool/
  servers/server.json       # single-server state
  servers/router.json       # router state
  logs/server.log
  logs/router.log
  router/models.ini         # generated, never hand-edited
```

### Schema

```toml
[app]
theme = "mocha"              # built-in or [themes.<name>] custom theme
nerd_fonts = false
model_dirs = ["~/models"]
llama_server = "llama-server"
log_lines = 10000

[server]
host = "127.0.0.1"
port = 8080
auto_port = true             # omitted means true
startup_timeout = "120s"
poll_interval = "1s"         # TUI refresh; clamped to 250ms..1m

[defaults]                   # shared launch options
ctx = 32768
gpu_layers = 999
n_cpu_moe = 0
threads = 0                  # 0 = runtime.NumCPU()
threads_batch = 0            # 0 = threads
batch_size = 4096
ubatch_size = 256
parallel = 1
flash_attn = "on"
cache_type_k = "q4_0"
cache_type_v = "q4_0"
jinja = true
metrics = true

[defaults.sampling]
temp = 1.0
top_p = 0.95
top_k = 20
min_p = 0.05
repeat_penalty = 1.1
presence_penalty = 0.0
frequency_penalty = 0.0
typical_p = 1.0
mirostat = 0
seed = -1

[models.<id>]
path = "~/models/model.gguf" # required
alias = "display-name"
# any launch option, plus [models.<id>.sampling] and [models.<id>.extra]

[presets.<name>]
model = "<model id>"         # required
# any launch option, plus [presets.<name>.sampling] and [presets.<name>.extra]

[router]
models_max = 1
memory_margin_mib = 512
autoload = false
sleep_idle_seconds = -1
metrics = true
extra_args = []

[themes.<name>]              # optional custom palette
base = "tokyo"               # built-in or another custom theme
[themes.<name>.colors]       # partial override
accent = "#ff9e64"
border = "#565f89"
```

Models are one section per GGUF. Presets are standalone launch recipes that
reference a model; they are how context variants and one-off overrides stay
out of the router. The router gets exactly one section per `[models.*]`
entry.

Custom themes are registered at startup and on `/reload`. A theme may base
itself on a built-in (`mocha`, `tokyo`, `gruvbox`, `nord`) or another custom
theme; dependencies are resolved to any depth and cycles, unknown bases,
duplicate names and attempts to redefine a built-in are rejected. Only the
keys listed in the example need to be set. `t` cycles every registered theme
and `/theme <name>` selects one directly.

### Inheritance

Launch options are merged in this order:

```text
built-in defaults -> [defaults] -> [models.<id>] -> [presets.<name>]
```

Option fields are pointers internally. An unset TOML key means "inherit",
while an explicitly set zero or false is preserved. Sampling is merged field
by field the same way, so a preset can change `temp` without restating the
other sampler values.

After merging:

- `threads = 0` becomes `runtime.NumCPU()`;
- `threads_batch = 0` becomes the resolved `threads`;
- `gpu_layers >= block count` is treated as "all layers" by the estimator,
  and passed through as the user wrote it (the example uses `999`);
- the alias falls back to the model ID;
- model and preset paths expand `~` and are made absolute.

### Validation and warnings

`config.Validate` rejects structural errors: bad IDs (they become router INI
sections), an empty model path, a preset referencing an unknown model, bad
sampling ranges, invalid ports and negative router limits.

A missing model file is **not** a load error. It is reported by
`Config.Warnings()`, `wool doctor` and the TUI as a warning, so a
config can be prepared before the weights are downloaded.

### Comment-preserving edits

The TUI editor writes through `config.SetValue`, a small TOML-aware editor
that changes only the requested key and preserves the rest of the file's
comments and ordering. After a save the config is reloaded and the relevant
caches are invalidated. The TOML file remains the source of truth; there is
no hidden state database.

## Single-server launches

`run` accepts either a preset name or a model ID. `config.Resolve` produces a
`Resolved` struct, then `llamaserver.SingleArgs` builds argv:

```text
--model <path> --host <host> --port <port> [--alias <alias>]
[-c <ctx>] --gpu-layers N [--n-cpu-moe N]
[--threads N] [--threads-batch N] [--batch-size N] [--ubatch-size N]
[--parallel N] [--flash-attn MODE] [--cache-type-k TYPE] [--cache-type-v TYPE]
[--jinja | --no-jinja] [--metrics]
[--temp ... --top-p ... --top-k ... --min-p ... --repeat-penalty ...
 --presence-penalty ... --frequency-penalty ... --typical-p ...
 --mirostat ... --seed ...]
<extra keys as --flag value>
```

`[models.<id>.extra]` / `[presets.<name>.extra]` keys become flags. Keys may
be written with or without leading dashes; an empty value produces a bare
flag (for example `spec-type = "draft-mtp"` becomes
`--spec-type draft-mtp`).

Port selection is in `daemon.ChoosePort`:

- `auto_port = false`: the configured port must be free;
- `auto_port = true`: walk up to 50 ports from the configured one, then ask
  the kernel for an ephemeral port.

`run` waits for `GET /health` by default using `server.startup_timeout` and
`server.poll_interval`. `--no-wait` returns immediately; `--dry-run` prints
the command; `--restart` stops the existing `server` first. The TUI starts
detached and does not block on health: its refresh loop picks up the state.

## Detached lifecycle

`daemon.Manager` is a deliberately small supervisor boundary:

1. Validate the instance name and re-use check from its state file.
2. Choose a port and open the append-only log.
3. Write a header with timestamp, kind and the fully quoted command.
4. Start the binary with `Setsid: true`.
5. Read the PID, PGID and `/proc/<pid>/stat` start tick.
6. Atomically write `servers/<name>.json`.
7. Reap the child while the launching process is alive.

The state file records:

```json
{
  "name": "server",
  "kind": "server",
  "preset": "qwen-cyber-32k",
  "model": "qwen3-4b-cyber",
  "host": "127.0.0.1",
  "port": 8080,
  "pid": 143344,
  "pgid": 143344,
  "start_ticks": 857391,
  "started_at": "2026-10-29T00:02:20-07:00",
  "log_path": ".../logs/server.log",
  "bin": "/usr/bin/llama-server",
  "args": ["--model", "..."]
}
```

Liveness requires the PID to exist, not be a zombie, and to have the same
`starttime` field. This prevents signaling a recycled PID. `Stop` sends
`SIGTERM` to the process group, waits, then escalates to `SIGKILL`. Logs are
read with a bounded tail (up to 512 KiB read from the end), so a very large
log does not stall the TUI.

`status` and `logs` work from any directory because they only need the XDG
state directory. `status --json` exposes the full state, including argv.

## Router mode

### Generated models.ini

`llamaserver.RenderRouterINI` writes deterministic output:

```ini
# Generated by wool. Edits here are overwritten.
[*]
ctx-size = 32768
gpu-layers = 999
threads = 16
...
jinja = true
temp = 1.0
...

[gemma-4-heretic]
model = /home/user/models/gemma-4-heretic.gguf
alias = gemma-heretic
ctx-size = 32768
gpu-layers = 999
...
```

The `[*]` section holds `[defaults]`; each model section holds its resolved
options and extras. Sections are sorted by model ID. The file is written to a
temporary file, chmod `0600`, and renamed atomically. It is regenerated on
router start, `router reload`, and the TUI's reload action.

Only host, port, models-max, margin, autoload, sleep and metrics are passed on
the router command line:

```text
--host H --port P --models-preset models.ini --models-max N
[--models-autoload | --no-models-autoload]
[--sleep-idle-seconds N] [--metrics] [--fit-target MiB]
<router.extra_args>
```

Per-model flags are intentionally not passed on the command line: router CLI
args override preset INI values, which would flatten all model-specific
settings into one configuration.

Do not also pass `--models-dir`; using both it and `--models-preset` exposes
duplicate model IDs.

### Router API

`internal/llamaserver` wraps the root-level management endpoints:

- `GET /health`
- `GET /models` and `GET /models?reload=1`
- `POST /models/load` / `POST /models/unload` for an ID
- `GET /props?model=ID`
- `GET /slots?model=ID`
- `GET /metrics?model=ID`

`/slots` and `/metrics` require the `model` query parameter; without it the
router returns `model name is missing from the request`. `/metrics` must be
enabled explicitly, which the config does by default. `?reload=1` refreshes
the preset list after `models.ini` changes without restarting the router.

Router status strings observed from the server are `unloaded`, `loading`,
`loaded`, `sleeping`, `downloading` and `failed`. The TUI treats `loaded` and
`sleeping` as resident when counting models.

The default `models_max = 1` and 512 MiB margin suit the 8 GiB AMD card used
during development. Autoload is off and sleep-on-idle is disabled, so an
explicit load stays loaded until explicitly unloaded.

## TUI

### Layout

The layout follows `ref/esp_term`:

```text
┌    [1] Dashboard  ...  [6] Logs                                  wool ┐
│ ┌ main panel (active tab) ──────────────┐ ┌ Status ──────────────┐ │
│ │                                      │ │ server / router      │ │
│ │                                      │ ├ Machine ─────────────┤ │
│ │                                      │ │ CPU, RAM, GPU        │ │
│ └──────────────────────────────────────┘ └──────────────────────┘ │
│ command line / toast                                          theme │
│ footer: key hints                                      widthxheight │
└─────────────────────────────────────────────────────────────────────┘
```

Below 84 columns the sidebar disappears and the main panel uses the full
width. Panels use ANSI-aware fitting and are clipped to the terminal height.
The editor and doctor/help overlays replace the normal body and keep the
footer.

### Refresh loop

`Init` schedules an immediate refresh. Every `tickMsg` applies a snapshot and
schedules the next one after `server.poll_interval` (clamped to 250ms..1m);
actions schedule a faster refresh.
The background command samples `/proc` and `/sys`, checks both state files,
calls `/health`, `/models`, and for the selected loaded router model
`/metrics` and `/slots`. HTTP requests have a two-second timeout, so an
unresponsive server cannot freeze the UI.

Fit and GGUF metadata are cached per target path; missing or unreadable files
are cached too, so a broken model does not cause a stat/read every frame.

### Tabs

| Tab | Contents |
| --- | --- |
| Dashboard | selected preset, resolved options, fit bar, log tail |
| Models | configured models, file size, VRAM estimate, alias |
| Presets | recipes with model, context and fit |
| Router | router state, model list, selected model stats and slots |
| Hardware | CPU/RAM/GPU sparklines, VRAM, temperature, power, fan |
| Logs | server or router log, follow/pause and scroll |

### Keys

| Key | Action |
| --- | --- |
| `tab` / `shift+tab` | next / previous tab |
| `1`..`6` | jump to tab |
| `j` / `k`, arrows | move selection or scroll logs |
| `g` / `G` | first / last entry; in logs, tail or follow |
| `enter` | start selected target, load selected router model, or switch log target |
| `s` / `x` | start / stop the standalone server |
| `S` | restart the selected target |
| `r` / `R` | start / stop the router |
| `l` / `u` | load / unload the selected router model |
| `m` | show stats for the selected router model |
| `L` | focus logs |
| `f` | toggle log follow |
| `e` | edit the selected preset or model |
| `d` | run doctor |
| `t` | cycle theme |
| `ctrl+r` | reload config.toml |
| `/` or `:` | command line |
| `?` | help overlay |
| `q` / `ctrl+c` | quit without stopping servers |

### Commands

Commands are entered after `/` and support history (`up`/`down`) and `tab`
completion for command names and model/preset IDs.

```text
/start [name]           start a preset or model (selected target if omitted)
/restart                restart the selected target
/stop [server|router|all]
/router [start|stop|reload|models]
/load ID                load a router model
/unload ID              unload a router model
/reload                 reload config.toml
/edit                   open the option editor
/doctor                 run readiness checks
/theme [name]           cycle or set a theme
/tab NAME               dashboard, models, presets, router, hardware, logs
/logs [server|router]   show a log
/help                   key and command reference
/quit                   exit
```

### Editor

The editor shows the resolved values for the selected preset or model. Fields
that are inherited from a lower layer are marked `inherited`. Changing a
field writes the key (and, for sampling, the nested table) into the TOML file
through `config.SetValue`, reloads the config, and reopens the editor at the
same selection. Booleans toggle directly; numbers and strings use a text
input with validation. Because the editor writes the file, edits are visible
to the CLI and survive a restart.

The TUI editor writes a concrete value even when the field was inherited; to
remove an override later, delete the key in the TOML file. This is a v1
trade-off that keeps the editor simple.

## Hardware telemetry

`sysinfo.Collector` reads:

- `/proc/meminfo` for RAM and swap;
- `/proc/loadavg` and `/proc/uptime`;
- `/proc/stat` for cumulative CPU jiffies, converted to a percentage between
  samples;
- `/sys/class/drm/card*/device/...` for AMD `amdgpu` data: temperature,
  VRAM total/used, busy percent, power and fan RPM.

Missing sensors are not errors; each GPU field has a `Has*` flag. The primary
GPU is the one with the most VRAM, so a discrete card is preferred over an
iGPU. `History` keeps 120 samples for the TUI graphs.

The TUI samples on `server.poll_interval` (clamped to 250ms..1m), so a slower
interval also means a calmer graph. The small CPU readout is additionally
smoothed with an exponential moving average; the sparkline keeps the raw
samples.

The `Backend` interface is the extension point for other platforms or for
test doubles.

## VRAM estimate

`gguf.ReadMeta` reads only the GGUF key/value block, enough to get the
architecture, block count, embedding size, attention heads, training context
and, for hybrid models, `full_attention_interval`. `EstimateVRAM` then
computes:

```text
weights  = file size * (offloaded layers / block count)
kv       = KV bytes for ctx, full-attention layers, head counts, cache type
overhead = 512 MiB (compute buffers and allocator slack)
total    = weights + kv + overhead
```

Hybrid architectures such as `qwen35` (Qwen3.5/Qwen3-Next) interleave
recurrent layers with full-attention layers. Only every
`full_attention_interval`-th layer (1-indexed: layers 4, 8, 12, ... for an
interval of 4) allocates a KV cache; recurrent state is small and constant
size, so it is covered by the overhead term. Counting KV for every block
overestimated these models badly.

The estimate is intentionally approximate. It does not model MoE CPU offload,
context checkpoints, unused MTP tensors in some GGUFs, tensor fragmentation or
driver overhead, so a model with `n_cpu_moe` can still show as over-VRAM.
Doctor adds the note `estimate ignores n_cpu_moe` in that case. The UI uses
`!` for an over-budget estimate, not a hard launch block.

## Doctor

`wool doctor` checks:

- config parse and summary;
- `llama-server` lookup and `--version`;
- hardware and VRAM;
- model directories and GGUF files;
- duplicate model paths (the router would expose the same weights twice);
- per-model and per-preset fit estimates.

Warnings do not make the command fail; failures do. The TUI shows the same
report in a scrollable overlay.

## CLI reference

```text
wool [--config PATH]                 open the TUI
wool doctor                          check binary, models and VRAM fit
wool list [--json]                   list models and presets
wool run TARGET [options]            start a model or preset detached
wool stop [server|router|all]        stop detached servers
wool status [name] [--json]          show detached server state
wool logs [name] [-n N] [-f]         show a server log
wool router [start|stop|status|models|load ID|unload ID|reload|ini]
wool config path                     print the config path
wool config init [--force] [PATH]    write a sample config
wool version
```

`run` options: `--dry-run`, `--restart`, `--port N`, `--no-wait`.
`router start` accepts the same options.

## Testing

- `go test ./...` covers config parsing/merging/edits, GGUF metadata and
  estimates, argv/INI generation, router metrics parsing, daemon lifecycle
  and PID validation, sysinfo parsing, and TUI rendering at multiple sizes.
- One view test renders every tab at 120x40, 100x30, 80x24, 60x20 and 40x12
  and asserts that no line exceeds the terminal width.
- Editor tests write to a temporary config and reload it to verify the TOML
  round trip.
- The daemon tests use short-lived `sleep`-style processes and temporary state
  directories; they do not touch the real XDG state.

Manual smoke test used during development:

```text
wool run qwen-cyber-32k
wool status --json
wool logs server -n 6
wool stop server
wool router start
wool router models
wool router load qwen3-4b-cyber
wool router reload
wool router unload qwen3-4b-cyber
wool router stop
```

## Known limitations and next steps

See [`roadmap.md`](roadmap.md) for the sequenced plan, priorities and exit
criteria. The limitations below are the current state, not the plan.

- The VRAM estimate is conservative. Hybrid recurrent layers are modelled,
  but MoE CPU offload, unused MTP tensors and speculative decoding buffers are
  not; sysfs VRAM right after load can read lower before mmap pages are
  faulted. Use the estimate as a fit warning, not a measurement.
- Only AMD sysfs telemetry is implemented; NVIDIA/Intel need new backends.
- The TUI manages one standalone server and one router, not arbitrary named
  instances. The daemon layer already uses names and can grow this later.
- Editing always writes concrete values; there is no "remove override" action.
- There is no automatic restart/supervision of the standalone server in v1.
  Router-managed models are supervised by `llama-server` itself.
- Logs are tailed from disk on a timer rather than streamed; this is simple
  and works across reattachments.
