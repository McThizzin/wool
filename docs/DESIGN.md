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

- a custom process supervisor for router-managed model children (the router
  does that; standalone supervision is limited to `wool supervise`);
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
        ├── daemon.Manager ──> detached llama-server --model <gguf>
        │
        └── daemon.Manager ──> detached wool supervise --foreground
                                  └── llama-server --model <gguf>
```

The TUI/CLI process is not the parent after startup. `Manager.Start` uses
`Setsid`, writes a state file, and returns. A later `wool` process
finds the state file, verifies the PID start time, and reconnects to the HTTP
API. Quitting the TUI never sends a signal to a server.

A single detached instance named `server` and at most one named `router` are
supported. The router itself owns the lifecycle of the models it loads.
`wool supervise` adds one optional process layer for the standalone server; the
supervisor owns the child, and both stay in the same process group.

## Package map

| Package | Responsibility |
| --- | --- |
| `cmd/wool` | CLI parsing, subcommands, TUI entry point |
| `internal/app` | Bubble Tea model, tabs, overlays, key handling, actions |
| `internal/config` | TOML schema, defaults, merge/resolve, validation, paths, comment-preserving edits |
| `internal/daemon` | detached process start/stop, state files, liveness checks, ports, log tailing |
| `internal/llamaserver` | single-server argv, router argv, `models.ini` generation, HTTP client |
| `internal/gguf` | GGUF metadata reader and VRAM estimate |
| `internal/fit` | auto-fit solver, context search, fallback ranking and size parsing |
| `internal/measure` | observed-VRAM and benchmark JSONL store, launch capture |
| `internal/bench` | server and `llama-bench` benchmark runners, summaries, VRAM sampling, A/B comparison |
| `internal/reqlog` | best-effort `llama-server` request-log parser and bounded row tail |
| `internal/alerts` | alert rules, per-rule deduplication and optional desktop/webhook notifiers |
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
smoke_test = false           # send one tiny prompt after health
smoke_timeout = "30s"        # bounds the whole smoke request
restart = "never"            # never | on-failure | always; active policies retry unexpected deaths
restart_max = 3              # attempts inside restart_window; 0 disables
restart_window = "10m"       # sliding window for attempt counting
restart_backoff = "2s"       # first delay; doubles per attempt, capped at 1m

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

[alerts]
enabled = false              # opt-in
temp_c = 90                  # 0–150
vram_percent = 95            # 0–100
cooldown = "5m"              # per-rule deduplication window
notify = false               # desktop notification via notify-send
webhook = ""                 # optional JSON POST URL

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

Built-in defaults are a separate lowest layer, not values written into
`c.Defaults` while loading. That keeps an explicit `[defaults]` key
distinguishable from a built-in value. `config.ResolveDetailed` returns the
resolved options plus a source map (`built-in`, `defaults`, `model`, `preset`)
used by the editor and `wool config explain`.

`config.Diff` compares a resolved target with either the defaults layer or the
target's model and returns only fields whose values differ, in canonical option
order. A preset defaults to its model, which answers "what does this preset
change?"; a model defaults to the defaults layer. Each side keeps its value,
source and exact TOML layer, so sampling keys are compared individually.
`wool config diff` and the TUI `D` overlay use the same function and print
nothing when the list is empty.

After merging:

- `threads = 0` becomes `runtime.NumCPU()`;
- `threads_batch = 0` becomes the resolved `threads`;
- `gpu_layers >= block count` is treated as "all layers" by the estimator,
  zero means no offloaded layers, negative values mean all, and the value is
  passed through as the user wrote it (the example uses `999`);
- the alias falls back to the model ID;
- model and preset paths expand `~` and are made absolute.

### Validation and warnings

`config.Validate` rejects structural errors: bad IDs (they become router INI
sections), an empty model path, a preset referencing an unknown model, bad
sampling ranges, invalid ports, negative router limits, alert thresholds out
of range, a non-positive alert cooldown, and webhook URLs that are not HTTP or
HTTPS.

A missing model file is **not** a load error. It is reported by
`Config.Warnings()`, `wool doctor` and the TUI as a warning, so a
config can be prepared before the weights are downloaded.

### Comment-preserving edits

The TUI editor writes through `config.SetValue`, a small TOML-aware editor
that changes only the requested key and preserves the rest of the file's
comments and ordering. `config.SetValues` applies several key updates in one
atomic write; `SetValue` is its single-key wrapper. `config.RemoveValue` is the
counterpart: it deletes one scalar key, leaves an empty table header in place,
and refuses multi-line or inline collection values. After a save or reset the
config is reloaded and the relevant caches are invalidated. The TOML file
remains the source of truth; there is no hidden state database.

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
  "supervised": true,
  "preset": "qwen-cyber-32k",
  "model": "qwen3-4b-cyber",
  "host": "127.0.0.1",
  "port": 8080,
  "pid": 143344,
  "pgid": 143344,
  "start_ticks": 857391,
  "started_at": "2026-10-29T00:02:20-07:00",
  "log_path": ".../logs/server.log",
  "bin": "/usr/bin/wool",
  "args": ["supervise", "--foreground", "--", "--model", "..."]
}
```

For a supervised instance, `pid`, `pgid`, `start_ticks` and `bin`/`args`
describe the supervisor, not the child. `wool restart` therefore relaunches the
supervisor from the recorded argv and supervision survives the restart.
`Instance.Alive` is true when either the supervisor or its recorded group is
alive; `Instance.SupervisorAlive` distinguishes an orphaned child from a live
supervisor.

Liveness requires the PID to exist, not be a zombie, and to have the same
`starttime` field. This prevents signaling a recycled PID. For an unsupervised
instance, `Stop` sends `SIGTERM` to the process group, waits, then escalates to
`SIGKILL`. For a supervised instance it signals the supervisor first, so the
supervisor can stop its child without treating the exit as a crash; if the
supervisor is gone, the recorded group is signalled directly to clean up the
orphan. Logs are read with a bounded tail (up to 512 KiB read from the end), so
a very large log does not stall the TUI.

`status` and `logs` work from any directory because they only need the XDG
state directory. `status --json` exposes the full state, including argv.

## Crash detection and restart

The TUI compares each poll with the previous one. A state file that is present
but whose process is gone means the server died without going through wool.
`daemon.Manager.Stop` removes the state file, so a clean TUI or CLI stop is not
a crash; intentional restarts also set an `expectStop` guard for the interval
between the stop and the new launch.

`daemon.ClassifyCrash` scans only the newest launch header in the log and
applies precedence OOM > port > model > signal > unknown. The crash card shows
the reason, time, policy status and up to four relevant lines. `S` or
`/restart` relaunches the recorded argv; `x` or `/dismiss` hides the card,
cancels a pending automatic retry and keeps the state for `wool restart`.

In-session restart is opt-in with `[server] restart`. `restart_max` counts
attempts inside `restart_window`; an explicit `0` disables automatic attempts.
`restart_backoff` doubles per attempt and is capped at one minute. Successful
automatic attempts do not reset the window, so a crash loop is bounded; manual
starts, stops and restarts reset it. Dismissing a crash card also cancels its
pending retry. Router-managed model children are not supervised here because
`llama-server` owns them.

`wool supervise TARGET` is the detached variant. It records the supervisor as
the instance process and re-execs it as `wool supervise --foreground`, with the
child argv after `--`. The supervisor writes a launch header before each child
attempt, so crash classification only inspects the newest failure. It always
restarts an unexpected exit and ignores `[server] restart`; `restart_max`,
`restart_window` and `restart_backoff` still apply, and `restart_max = 0` gives
up after the first exit. On exhaustion the supervisor returns
`daemon.ErrSupervisorExhausted` and leaves the state file so the crash card and
`wool restart` still work. The supervisor is a session leader; the child
inherits its process group, so `wool stop` can clean up either process. The
supervisor reaps its own child, and the manager validates the recorded start
ticks before signalling, so recycled PIDs and zombies are not treated as live.
TUI start and restart actions rebuild the launch from the current config, so
they replace a supervised instance with an unsupervised one; a crash-card
restart reuses the recorded state and keeps supervision.

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

The Router tab predicts eviction from the count limit. The pinned router's
`pick_victim` selects the idle model with the oldest `last_used` when
`models_max` would be exceeded; `GET /models` does not expose `last_used`, so
the TUI approximates LRU from the first poll that saw each model resident and
labels the victim list `LRU approx.`. A busy model is never evicted, but the
API does not expose per-model request counts, so the prediction cannot account
for one. The memory margin is shown as configured; the pinned router evicts by
count, not by the margin, and `--fit-target` uses the margin when sizing each
child. Missing model estimates degrade to `unknown` instead of hiding the
prediction.

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
calls `/health` and `/models`, and scrapes `/metrics` and `/slots` for the
standalone server when it is alive and for the selected loaded router model.
HTTP requests have a two-second timeout, so an
unresponsive server cannot freeze the UI.

Fit and GGUF metadata are cached per target path; missing or unreadable files
are cached too, so a broken model does not cause a stat/read every frame. The
budget is cached by GPU total/used, reserve and the running server PID, so the
`fdinfo` scan only runs when one of those changes.

When alerts are enabled, the same background refresh tails the last 200 lines
of each running instance for the alert engine. The engine runs in `Update` with
data already gathered, so evaluation never blocks on disk or HTTP.
Notification delivery is returned as a Bubble Tea command and runs off the UI
goroutine.

### Tabs

| Tab | Contents |
| --- | --- |
| Dashboard | selected preset, resolved options, estimate breakdown and fit bar, log tail |
| Models | configured models, file size, VRAM estimate, alias |
| Presets | recipes with model, context and fit |
| Router | router state, model list, selected model stats and slots |
| Inference | live prompt/gen tokens/s, requests, slot occupancy and KV fill, speculative acceptance, sparklines |
| Hardware | CPU/RAM/GPU sparklines, VRAM, temperature, power, fan |
| Logs | structured request table by default, raw log fallback, follow/pause, model filter and scroll |
| Bench | recorded benchmark runs, trends, run comparison |

### Keys

| Key | Action |
| --- | --- |
| `tab` / `shift+tab` | next / previous tab |
| `1`..`8` | jump to tab |
| `j` / `k`, arrows | move selection or scroll logs |
| `g` / `G` | first / last entry; in logs, tail or follow |
| `enter` | start selected target, load selected router model, or switch log target |
| `s` / `x` | start / stop the standalone server (x dismisses a crash card) |
| `S` | restart the selected target or the crashed server |
| `r` / `R` | start / stop the router |
| `l` / `u` | load / unload the selected router model |
| `i` | focus the Inference panel |
| `m` | cycle the Inference source (server / router model), or the Logs model filter; elsewhere, show router-model stats |
| `v` | toggle the Logs tab between structured requests and the raw log |
| `L` | focus logs |
| `B` | focus the Bench tab |
| `space` | mark a benchmark run for comparison |
| `f` | toggle log follow, or cycle the Bench target filter |
| `e` | edit the selected preset or model |
| `F` | recommend settings that fit VRAM in the editor |
| `d` | run doctor |
| `D` | show what the selected target changes |
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
/restart                restart the selected target or the crashed server
/dismiss                hide a crash card and cancel its pending retry
/stop [server|router|all]
/router [start|stop|reload|models]
/load ID                load a router model
/unload ID              unload a router model
/reload                 reload config.toml
/edit                   open the option editor
/fit                    open the option editor and fit overlay
/doctor                 run readiness checks
/theme [name]           cycle or set a theme
/tab NAME               dashboard, models, presets, router, inference, hardware, logs, bench
/logs [server|router]   show a log
/help                   key and command reference
/quit                   exit
```

### Inference panel

The Inference tab renders one `infSource` at a time: either the standalone
server or the selected router model. `infResolve` picks the source — the
user's pinned choice (`m` cycles it) while its instance is alive, otherwise
the standalone server, otherwise the router — without mutating the model;
`applyTick` drops a pin whose instance died, and `cycleInfSource` resets the
panel immediately so the old source's numbers never appear under the new
header.

Rates come from `llamaserver.Rates`, a counter-delta tracker keyed by the
source. `updateInference` runs on every tick for the resolved source only,
so switching targets invalidates the baseline (`key` change) instead of
computing deltas across two different servers. A decreasing counter means
the server restarted: the tracker re-seeds, reports `Reset`, and no sample is
pushed, so the sparklines never draw a dip that never happened. History
pushes are gated on a completed window (`Window > 0`), which also skips the
seeding scrape. Token rates prefer the server's own busy counters
(`prompt_seconds_total`, `tokens_predicted_seconds_total`) so idle time is
not counted against throughput; wall clock is the fallback.

A metrics scrape that fails (404 = `metrics = false`, unreachable) leaves
the source in `infNoMetrics`: the panel shows the reason and the fix hint
instead of zeros. A router-model scrape that targeted a model other than the
now-selected one (a selection move while the request was in flight) is
discarded rather than attributed. The panel's right header mirrors the state:
`live`, `no metrics`, `not loaded`, `down`.

### Structured request log

`internal/reqlog` turns the request-completion lines `llama-server` already
writes into `Row` values: timestamp, model/port, prompt and generated tokens,
prompt and generation milliseconds, total milliseconds, cache hits and status.
It is deliberately isolated and stateful, keyed by `(slot, task)`, because the
prompt size, timings and HTTP status arrive on different lines. Two layouts are
recognised — older logs print `slot print_timing:` on its own line followed by
bare `prompt eval time`, `eval time` and `total time` lines with
`n_prompt_tokens`; newer logs put `prompt eval time` on the `print_timing` line
and use `task.n_tokens`. Progress `print_timing` lines with a payload are
ignored and never start a timing block.

A row is emitted when `total time` closes the block, with status 200. Cache
hits are `prompt tokens - prompt-eval tokens`, clamped at zero. A non-2xx
`log_server_r` line has no slot/task to join to a block, so it becomes an
error row on its own. Anything unrecognised is ignored. `Recognized()` reports
whether any known line was seen; when it is false the TUI shows the raw log
with an `unrecognised` note instead of an empty table.

The TUI feeds new lines by diffing the re-read tail (`newLogLines`): the
overlap with the previous tail is skipped, and a shorter tail or no overlap
resets the parser first, so truncation and rotation cannot leave stale pending
state. `logRowModel` attributes standalone rows to the running preset/model;
router rows use the child `[port]` prefix and the router model list's
`--port` argument, falling back to `router`. The table keeps
`reqlog.DefaultRows` (200) completed rows. `wool logs --json` runs the same
parser over `-n` tail lines and prints the stable `logsSnapshot` (version 1)
with `source`, `best_effort`, `recognized` and `rows`; it is rejected with
`-f`.

### Alerts

`internal/alerts` keeps rule evaluation pure: `Engine.Evaluate` takes one
observation (GPU temperature and VRAM, instance health and start times, router
model status and log tails) and returns only alerts that are past their
cooldown. Deduplication is keyed by `rule|subject`, so a hot GPU and a full GPU
are tracked separately, as are the server and router. The TUI calls the engine
from `Update` after the background refresh, keeps the newest 20 alerts and
renders the newest two at the top of the Status panel.

Rules:

| Rule | Condition |
| --- | --- |
| `temperature` | primary GPU temperature at or above `alerts.temp_c` |
| `vram` | primary GPU VRAM usage at or above `alerts.vram_percent` |
| `health` | a running instance reports a health status other than `ok`, `loading`, `loading model` or `starting` for 15 seconds after launch |
| `oom` | the newest tail line matches `out of memory`, `oom` or `failed to allocate` |
| `model_load` | a router model reports `status.failed` |
| `restart_loop` | three observed `StartedAt` changes within ten minutes |

The health grace is anchored at the instance start time when it is known, and
otherwise at the first failed observation. Log rules remember the matching line
fingerprint and do not re-fire while the same line remains in the tail. A
disappearing line clears the fingerprint so a later identical event can alert
again. The engine keeps only in-memory state; restarting the TUI resets it.

Optional delivery is separate from evaluation. `DesktopNotifier` shells out to
`notify-send` and returns `ErrUnavailable` when the binary is missing;
`WebhookNotifier` sends one version-1 JSON object per alert with a three-second
client timeout. Both are ignored on failure: the status bar already has the
alert. Webhook errors are deliberately generic and never include the URL, so a
token embedded in it cannot leak into a toast or log line. `wool alerts
[--json]` runs the same evaluation once, prints the stable `alertsSnapshot`
(version 1) with `enabled` and `alerts`, and never dispatches notifications.

### Editor

The editor shows the resolved values for the selected preset or model. Every
field is labelled with its source: `built-in`, `defaults`, `model` or
`preset`. Changing a field writes the key (and, for sampling, the nested
table) into the TOML file through `config.SetValue`, reloads the config, and
reopens the editor at the same selection. Booleans toggle directly; numbers
and strings use a text input with validation. `backspace` or `delete` on a
field owned by the current table calls `config.RemoveValue`, so the value
falls back to the next layer. Fields from a lower layer cannot be reset from
this table. Because the editor writes the file, edits are visible to the CLI
and survive a restart.

`F` opens the fit overlay for the current draft. It solves against the live
budget, lists the recommendation and alternatives, and shows each estimate
breakdown, headroom, limiting term and changed fields. `enter` applies the
selection to the in-memory draft and marks it dirty; the normal field edits are
suspended while a fit draft is pending. `ctrl+s` writes only the changed
fit-controlled options to the target's existing table; `esc` discards the draft
and restores the saved values.

`p` in the fit overlay opens a preset-name prompt pre-filled with
`<model>-<ctx>`. `enter` writes the selected recommendation as a new preset and
reloads the config without closing the overlay; an existing name switches to a
confirm step that requires `P` to replace it, and `esc` cancels. Saving uses
`config.SavePreset`, which writes `model`, `ctx`, `gpu_layers`, `n_cpu_moe`,
`cache_type_k` and `cache_type_v` through `config.SetValues`. Existing comments
and unrelated sections are preserved; a replaced preset keeps its other
options.

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

`gguf.ReadMeta` reads the GGUF key/value block and the tensor-info table,
enough to get the architecture, block count, embedding size, attention heads,
training context, `full_attention_interval`, the sliding-window/shared-KV
metadata and the size of every tensor. Tensor data is never read.
`EstimateVRAM` then computes:

```text
gpu weights = offloaded block tensors + offloaded output/global tensors, minus moved experts
cpu weights = CPU-resident block tensors + input tensors + host-resident output/global tensors + moved experts
kv          = per-layer KV for ctx (SWA capped at the window), full-attention layers, head counts, cache type
overhead    = 512 MiB (compute buffers and allocator slack)
total       = gpu weights + kv + overhead
```

`gpu weights` follows llama.cpp's placement, not a flat file-size fraction.
`gpu_layers` counts the output layer: the last N of `block_count + 1` layers are
placed on the GPU, so `gpu_layers = 1` offloads the output layer alone.
Negative values and values above `block_count + 1` mean all layers. Tensor
names are classified once: block tensors follow their layer, input tensors
(`token_embd`, `position_embd`, per-layer token embeddings and similar) always
stay on the CPU, output tensors follow the output layer, and repeating/global
tensors follow layer 0. The output layer is layer `block_count`.

`n_cpu_moe = N` then moves the routed-expert tensors (`blk.<n>.ffn_*_exps`) of
the first N layers to `cpu weights`, matching llama.cpp. Routers (`ffn_gate_inp`)
and shared experts (`ffn_*_shexp`) stay with their layer. `Total` remains the
VRAM term; the host weight term is reported alongside it and is not added to the
GPU total.

KV is computed per layer instead of charging every block:

- Hybrid architectures such as `qwen35` (Qwen3.5/Qwen3-Next) interleave
  recurrent layers with full-attention layers. Only every
  `full_attention_interval`-th layer (1-indexed: layers 4, 8, 12, ... for an
  interval of 4) allocates a KV cache; recurrent state is small and constant
  size, so it is covered by the overhead term.
- Sliding-window layers are charged `min(ctx, sliding_window)` and use
  `key_length_swa`/`value_length_swa` when the model provides them. Global
  layers keep the full context. An explicit per-layer pattern array wins; the
  scalar form follows llama.cpp's default (every Nth layer is dense, so `N=0`
  means all SWA and `N=1` all dense).
- Gemma 2, Gemma 3 and Gemma 3n defaults are applied when the GGUF omits the
  pattern: window 4096 with every 2nd layer dense, every 6th layer dense, and
  every 5th layer dense with shared KV from layer 20 respectively.
- Shared-KV models allocate a cache only for the first
  `block_count - shared_kv_layers` layers; trailing layers reuse an earlier
  cache.

When the tensor table is absent, a tensor type is unknown, a tensor name falls
outside the placement table, a block tensor is outside `block_count`, or a model
declares experts without recognised expert tensors, the estimator falls back to
`file size * (offloaded layers / block count)` and sets `Approximate` with a
short reason list. The file-size fallback treats `gpu_layers` as transformer
blocks and clamps the fraction at one. A sliding window without a known layer
pattern is charged full context and also marked approximate. The same applies
to parsing problems such as a computed tensor total larger than the file.
`wool doctor --json` exposes the byte terms, the flag and the reasons.

Placement and the Gemma defaults were checked against llama.cpp `0.6.0-dev`
(build 11429, commit `d812350493`). These are implementation details, so revisit
the table when the supported `llama-server` version changes.

The estimate is intentionally approximate. It does not model context
checkpoints, unused MTP tensors in some GGUFs, speculative decoding buffers,
tensor fragmentation or driver overhead. When a term is approximate, Doctor
appends `estimate is approximate` with the reasons instead of claiming an exact
fit. The UI uses `!` for an over-budget estimate, not a hard launch block.

`gguf.ComputeBudget` turns live VRAM into a budget for a *new* server:

```text
budget = total - reserve - other_used
reserve = router.memory_margin_mib
other_used = GPU used - running standalone server's own usage
```

The server's own usage is read best-effort from `drm-memory-vram` in
`/proc/<pid>/fdinfo`; router child allocations are not separated, so they
count as other usage. If the process cannot be measured, all current usage
counts as other usage. Headroom is `budget - estimate` and may be negative.

The dashboard and editor draw a segmented bar (`GPU weights`, `KV`, `overhead`)
against the card's total VRAM, show the CPU-resident weight term alongside it,
and name the largest VRAM term as the limiting one. The editor recomputes the
estimate from the draft value while typing; it does not write the file until
`Enter`. `Esc` restores the resolved value. Changing `n_cpu_moe` moves expert
bytes between the GPU and CPU terms when tensor metadata is available; when it
is not, the preview says the estimate is approximate and why.

## Auto-fit solver

`internal/fit.Solve` inverts `EstimateVRAM`: given a budget it finds the largest
context that fits. The solver searches a 256-token grid between the 2048-token
minimum and the model's native context, using `gguf.EstimateVRAM` as the cost
function. Every recommendation it returns satisfies
`estimate.Total <= budget.Available`; the solver never reports an over-budget
configuration as a recommendation.

The requested settings are evaluated first. If they can hold at least the
minimum context, that candidate wins even when a fallback would allow more
context. Otherwise the solver steps through fallback tiers in this order:

1. lower-precision symmetric KV cache types, down to `q4_0`/`iq4_nl`;
2. CPU offload: `n_cpu_moe` at 25/50/75/100% of the block count for MoE models,
   then `gpu_layers` at 75/50/25% of `block_count + 1` (all-CPU only as a last
   resort for MoE);
3. the most compressed cache combined with the offload placements.

Candidates are deduplicated and ranked deterministically: requested settings
before fallbacks, then larger context, more GPU layers, less `n_cpu_moe`, and a
less compressed cache at the same context. `Result` carries the primary
recommendation, up to five alternatives, the estimate breakdown, headroom, the
limiting term, the changed fields and short notes. Each recommendation also
reports whether the estimate is approximate and why.

The CLI accepts `--vram SIZE` as the total card size. The solver subtracts
`router.memory_margin_mib` and deliberately ignores live usage from other
processes, so repeated runs are deterministic. Without `--vram`, the budget
comes from live telemetry and excludes a running standalone server's own
allocation, matching the dashboard and doctor. `fitBudget` maps both paths onto
the same `gguf.Budget`.

The editor's `F` (or `/fit`) opens a picker over the recommendation and its
alternatives. `enter` copies `ctx`, `gpu_layers`, `n_cpu_moe`,
`cache_type_k` and `cache_type_v` into the in-memory draft and recomputes the
preview; `ctrl+s` writes only the values that differ from the saved config;
`esc` discards the draft. The solver itself never writes TOML. Saving a
recommendation as a preset is an explicit action: `wool fit --save-preset NAME`
or `p` in the picker calls `config.SavePreset`, which refuses an existing name
unless forced. Replacing a preset updates only the fit-controlled keys.

## Observed measurements

`internal/measure` keeps one append-only JSONL record per successful launch at
`$XDG_STATE_HOME/wool/measurements/runs.jsonl`. A record carries the model
identity and file mtime, architecture, launch shape (`ctx`, cache types, GPU
layers, `n_cpu_moe`), GPU PCI ID and driver, `llama-server --version`, the
estimate, and the observed delta.

The baseline is sampled immediately before the process starts. After `/health`
first reports `ok`, the capture samples system-wide used VRAM at 5s and 15s,
and subtracts the baseline from the last successful sample. Raw samples and the
sample count are kept. If no baseline or no sample is available,
the record says `baseline_known: false` and the UI does not present the delta as
truth. Restarts re-sample the baseline after the old process stops.

`wool run` performs the capture synchronously after health. The TUI runs the
same capture as a background command so the 15s window never blocks input.
Records are matched by model, context, cache types, GPU layers and
`n_cpu_moe`; observations from a different launch shape are not compared.
A corrupt line is skipped.

`Store.Calibration` groups observations by that launch shape and the current
environment: model size and modtime, GPU PCI ID, GPU driver and
`llama-server --version`. A sample expires when a field known on both sides
differs; unknown fields never expire a sample because the difference cannot be
proven. Records with an unknown baseline or zero observed/estimate bytes are
counted separately and never contribute a ratio.

With at least three usable samples the correction is the median
`observed/estimate` ratio, clamped to `[0.5, 2.0]`. The spread is
`(max-min)/median`. The raw estimate and sample count are always shown; the
corrected value is additive and is only applied when the user asks for it with
`wool fit --calibrated`. Expired-only state is reported as expired instead of
silently reusing stale samples. Calibration is a hint: other GPU users,
mmap faulting and sampling lag all confound the observed delta.

## Smoke test

A launch can be healthy and still unable to generate. With
`[server] smoke_test = true` or `wool run --smoke`, wool sends one
non-streaming completion after `/health`: a tiny fixed prompt, eight tokens and
temperature 0. The whole request is bounded by `smoke_timeout` (default 30s)
through a context, and the client has no separate HTTP timeout, so the
configured value is the only limit.

`RunSmoke` reduces the outcome to success, `empty response`, `timeout` or a
short one-line error. The result is written into the same observation record as
`verified_at` (success only), `smoke_ms` and `smoke_error`; a failed check is
still recorded. `Store.LatestVerification` returns the newest attempt for a
launch shape and ignores records without either field, so later plain
observations do not hide it. The dashboard fit line shows `✓ verified` with the
time and latency, or the failure reason.

A failure warns by default and leaves the server running. `--strict` requires
smoke to be enabled, prints the log tail, records the failure and stops the
child. `--no-wait` skips the check because there is no health wait; combining
it with `--smoke` is a usage error. The TUI uses the same config flag and runs
the check in the background before capturing the observation.

## Benchmarks

`wool bench` reuses the same store with `kind: "bench"` records. The default
`server` engine starts an ephemeral `llama-server` through
`daemon.NewManagerWithDirs` using a temporary state and log directory, so the
managed `server` state is never read or overwritten. The child is started on a
free port and stopped on success, error or `Ctrl-C`; a deferred stop also
covers early returns.

Before starting, the command checks the managed `server`. If it is alive and its
model ID and `llamaserver.ConfigHash` of the stored argv match the argv wool
would generate for the target, the benchmark attaches instead of starting a
second copy. The hash ignores host and port. Attachment also waits for health
but does not restart or stop the server.

Each run sends the same completion request: a repeated fixed sentence sized to
the requested prompt token count, `temperature: 0`, `seed: 42`, prompt caching
off and streaming off. Warm-up is one short completion unless `--no-warmup`.
Throughput comes from the response's `timings` when present, then from
`/metrics` counter deltas around the request, and finally from wall time. The
wall fallback cannot separate prompt from generation, so both rates are
conservative; every raw run records its `source`.

`internal/bench` keeps the raw runs and computes the median, minimum and maximum
for prompt tokens/s, generation tokens/s and total time. A VRAM sampler polls
the primary GPU every 100ms during the run and records the peak. For an
ephemeral or `llama-bench` run the baseline is sampled before launch, so the
delta is the model's growth. For an attached managed server the baseline
subtracts the server's own per-process VRAM when it is known; otherwise
`vram_known` is false. An arbitrary `--url` server has no baseline and therefore
never reports a delta. The record also carries the load time (ephemeral only),
`llama-server` or `llama-bench` version, hardware, target and config hash.

`--engine llama-bench` runs the external tool with `-o json`, passing the
resolved model, prompt/generation sizes, runs, threads, GPU layers,
`n_cpu_moe`, batch sizes, cache types and flash attention. The binary is looked
up next to the configured `llama-server`, then on `PATH`. The default engine
does not need it. `--url URL --model ID` skips config resolution when no config
file exists; an unreadable config still fails. It cannot be combined with a
target or `--engine llama-bench`.

## Benchmark history

`Store.BenchHistory` filters the same JSONL file to `kind: "bench"` records,
sorts oldest first, and applies the newest-`limit` cut after filtering. A target
matches either the recorded target name or the model ID, so a preset and the
model it references can be queried separately. `BenchTargets` and
`BenchTargetName` provide the TUI filter list and display label. The CLI prints
a tabwriter table; `--json` prints the same `[]measure.Record` the store
returned.

The store is bounded by record count rather than bytes: `DefaultMaxRecords` is
5000. `Append` writes the line, then compacts best-effort while holding a
sibling `runs.jsonl.lock` file with `syscall.Flock`. Compaction reads the file,
keeps the newest records, writes a temporary file in the same directory, and
renames it over the original. A failed compaction leaves a larger file but does
not turn a durable append into an error. The lock matters because compaction
rewrites the whole file and must not race an append from another wool process.

The TUI Bench tab reloads on the normal poll tick and when the tab opens. It
shows prompt and generation sparklines over the filtered runs and a table with
timestamp, target, prompt t/s, generation t/s, VRAM, load time and config hash.
`space` marks a run and `enter` compares the marked run with the selected one;
the comparison reuses the M1.4 two-column field layout for the recorded launch
fields and adds throughput, VRAM and load-time deltas. `f` cycles the target
filter. `D` opens the M1.4 diff for the selected run's target when that preset
or model is still in the config, so a historical run can be checked against the
current configuration.

## A/B comparison

`wool bench compare A B` shares the bench parser but requires exactly two
different config targets and rejects `--url`/`--model`. `--runs N` is rounds per
target, so the command performs `2N` measured completions. `interleavedOrder`
alternates the target each round (`A B B A A B …`) so neither side always runs
on the hotter machine.

Each round calls `runBenchCompareRound`, which starts a fresh ephemeral server,
takes one measured completion and stops it before the next round. The two models
therefore never occupy VRAM together. `compareSide` aggregates a target's rounds
into a `bench.Side`: samples keep their interleaved order, load is the median
across rounds, and VRAM comes from the round with the largest known delta so the
stored record stays conservative. Each side is also written as a normal
`kind: "bench"` record, which keeps history and trends working unchanged.

The shared model lives in `internal/bench/compare.go`. `ComparisonVersion` is 1
and the JSON shape is stable. `SpreadOf` computes `(max-min)/median` per series;
`BuildWarnings` adds a warning when prompt or generation spread exceeds
`NoiseThreshold` (10%), and when the GPU, GPU driver or `llama-server` version
differs between sides. `Delta` is `(B-A)/A`. These are coarse guards, not a
significance test.

The TUI builds sides with `bench.SideFromRecord`, so it can compare stored runs
without a live server. `/bench compare A B` looks up the newest recorded run for
each target and opens the same overlay used by mark/enter, including spread, raw
runs and warnings.

## Doctor

`wool doctor` checks:

- config parse and summary;
- `llama-server` lookup and `--version`;
- hardware and VRAM;
- model directories and GGUF files;
- duplicate model paths (the router would expose the same weights twice);
- per-model and per-preset fit estimates, including the GPU/CPU weights, KV and
  overhead breakdown, budget, headroom, limiting term and approximation
  reasons;
- the calibration for the matching launch shape and environment: sample count,
  median ratio, spread and expiry state, when one exists.

Warnings do not make the command fail; failures do. `wool doctor --json`
exposes the same report. The TUI shows the plain-text report in a scrollable
overlay.

## CLI reference

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
wool supervise [options] TARGET      run a target under a detached supervisor
wool restart [server|router] [--no-wait]  relaunch the recorded instance
wool stop [server|router|all]        stop detached servers
wool status [name] [--json]          show detached server state
wool stats [MODEL] [options]         live prompt/gen t/s, requests, slots
wool logs [name] [-n N] [-f] [--json] show a server log, or parsed requests
wool alerts [--json]                 evaluate alert rules against current state
wool router [start|stop|status|models|load ID|unload ID|reload|ini]
wool config path                     print the config path
wool config init [--force] [PATH]    write a sample config
wool config explain TARGET [--json]  show each value and its source layer
wool config diff TARGET [--against defaults|model] [--json]
wool version
```

`run` options: `--dry-run`, `--restart`, `--port N`, `--no-wait`.
`supervise` options: `--restart`, `--port N`, `--no-wait`; it always restarts
unexpected exits within the configured `restart_max`, `restart_window` and
`restart_backoff`.
`fit` options: `--vram SIZE`, `--cache-type TYPE`, `--gpu-layers N`,
`--n-cpu-moe N`, `--save-preset NAME`, `--force`, `--json`. `--force` is only
valid with `--save-preset`; saving happens before output so a failure is
reported without a success line. In JSON output, `saved_preset` is present when
a preset was written.
`bench` options: `--runs N`, `--pp N`, `--tg N`, `--engine server|llama-bench`,
`--url URL`, `--model ID`, `--no-warmup`, `--json`. `--url` requires
`--model` and cannot be combined with `TARGET` or `--engine llama-bench`.
`--json` prints the record that was appended, including every raw run.
`bench history` options: `--limit N` (default 20) and `--json`; `TARGET` is
optional and filters by target name or model ID.
`bench compare` takes exactly two different targets and accepts `--runs N`
(rounds per target, default 3), `--pp N`, `--tg N`,
`--engine server|llama-bench`, `--no-warmup` and `--json`. It rejects `--url` and
`--model`. `--json` prints the stable `Comparison` (version 1), not a store
record; the two per-side bench records are still appended.
`router start` accepts the same options as `run`. Unless `--no-wait` or
`--dry-run` is used, `wool run` records one observation after health, so the
command stays alive through the 5s/15s settle window.
`stats` takes an optional router MODEL and accepts `--window D` (default 1s)
and `--json`. It scrapes `/metrics` and `/slots` twice, window apart, and
computes rates from the counter deltas with `llamaserver.Rates`, the same
tracker the TUI Inference tab uses. The target is the explicit model (the
router must be running it), else the standalone server, else the router's
first loaded or sleeping model. `--json` prints the stable `statsSnapshot`
(version 1); a metrics scrape failure exits non-zero with the reason instead
of printing zeros.
`logs` takes an optional `server` or `router` name, `-n N` (tail lines to read,
default 100), `-f` to follow, and `--json` to print parsed request rows. The
JSON shape is the stable `logsSnapshot` (version 1): `source`, `best_effort`,
`recognized` and `rows`. It parses the same `-n` tail and cannot be combined
with `-f`. Without a name it picks the running standalone server, then the
router. Raw output is unchanged when `--json` is omitted.
`alerts` evaluates the configured rules once against current hardware, running
instances, router model status and log tails. It prints `alerts disabled` when
the section is off and `no alerts` when nothing is active. `--json` prints the
stable `alertsSnapshot` (version 1) with `enabled` and `alerts`; it never sends
desktop or webhook notifications.

## Testing

- `go test ./...` covers config parsing/merging/edits, provenance, key removal
  and effective-config diffs, GGUF metadata and estimates, dense/hybrid/MoE
  auto-fit solving and fallback ranking, argv/INI generation, router metrics
  parsing, rate tracking and scrape classification, daemon lifecycle and PID
  validation, sysinfo parsing, benchmark runners, history filtering and store
  rotation, `llama-bench` parsing, interleaved A/B comparison and its noise
  warnings, `wool stats` text and JSON output, request-log parsing against old
  and new `llama-server` fixtures, the bounded row tail, and TUI rendering at
  multiple sizes, including the Inference panel's seeding, counter-reset and
  source-switch behaviour and the Logs tab's structured view, fallback and
  model filter.
- Alert tests inject a fake clock and a fake notifier. They cover thresholds,
  cooldown, health grace and recovery, loading states, OOM line
  deduplication, model-load failure, restart loops, missing `notify-send`, the
  webhook payload and URL redaction.
- One view test renders every tab at 120x40, 100x30, 80x24, 60x20 and 40x12
  and asserts that no line exceeds the terminal width.
- Editor tests write to a temporary config and reload it to verify the TOML
  round trip.
- The daemon tests use short-lived `sleep`-style processes and temporary state
  directories; they do not touch the real XDG state.
- Benchmark tests drive an `httptest` server for timings, metrics and wall-time
  fallbacks, and use temporary XDG state for the CLI integration test. No test
  requires `llama-bench`.

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

- The VRAM estimate is conservative. Hybrid recurrent layers and MoE expert
  offload are modelled, but non-block tensor placement, sliding-window KV,
  unused MTP tensors and speculative decoding buffers are not; sysfs VRAM right
  after load can read lower before mmap pages are faulted. Use the estimate as
  a fit warning, not a measurement.
- Only AMD sysfs telemetry is implemented; NVIDIA/Intel need new backends.
- The TUI manages one standalone server and one router, not arbitrary named
  instances. The daemon layer already uses names and can grow this later.
- The editor can reset launch-option overrides, but not `model`, `path`,
  `alias` or `extra` keys.
- `wool fit` is advisory and inherits the estimator's approximation. It never
  recommends an over-budget configuration. Saving a preset is explicit and
  updates only the fit-controlled keys; applying a recommendation to an
  existing target remains a separate draft edit.
- In-session crash restart is opt-in and only runs while a wool process is
  attached. `wool supervise` is the detached option for the standalone server;
  it is not used for router-managed models, which `llama-server` supervises
  itself. A supervised instance is still one wool process plus one child, not a
  system service; use systemd for durable service management.
- Logs are tailed from disk on a timer rather than streamed; this is simple
  and works across reattachments. The structured request table is best effort:
  `llama-server` log formats drift, unknown lines are ignored, and a format
  change shows the raw log rather than guessed rows. It keeps the newest 200
  requests in memory only, so history before the current tail is not parsed.
- Alerts are best effort and opt-in. They evaluate the current poll rather than
  a persisted history, deduplicate per rule and subject, and reset when the TUI
  restarts. A missing `notify-send` is ignored, and webhook delivery has a
  short timeout; neither is a durable notification channel.
- Benchmarks are single-stream and short by default. They measure one fixed
  prompt, not concurrent load, and prompt length is approximated by repeating a
  sentence rather than tokenizing. Peak VRAM is a sampled system-wide delta and
  is noisy when other GPU work runs; an arbitrary `--url` server cannot report a
  delta at all.
- The measurement store is capped at 5000 records. Old runs are dropped without
  warning, and a two-run comparison only diffs the launch fields wool recorded;
  it is not a statistical test. A/B comparison uses the same coarse 10% spread
  guard for its noise warning, so a small delta on a shared machine can still be
  noise even when no warning is shown.
