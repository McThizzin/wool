Wool already has a strong foundation: detached lifecycle, a single TOML source of truth, and a VRAM estimator. These features build on those three things rather than adding unrelated surface area.

## Close the loop on the estimator

The VRAM estimate is the most distinctive asset, and right now it's a prediction that's never checked against reality.

- **Calibration from observed usage.** After a server comes up, compare the estimate to actual amdgpu VRAM delta and store the error per model/quant/ctx. Over time you can show "estimate: 6.1 GiB, last observed: 6.4 GiB" and apply a learned correction factor.
- **Auto-fit solver.** Given a model and a VRAM budget, solve for the max context, or the best KV cache type, or the minimum number of offloaded layers. Expose it as `wool fit qwen3-4b-cyber --vram 7G` and as a key in the preset editor ("fit ctx to available").
- **What-if sliders in the editor.** Live-updating VRAM bar as you change ctx, cache type, or gpu_layers, before you save anything.
- **MoE expert offload modeling.** You list it as a known gap. Since it's where most people with 8 GB cards need help, modeling `--n-cpu-moe` / tensor overrides would make the estimator a lot more useful.

## Make the server's health visible

Hardware telemetry exists, but inference-level observability is mostly in the router metrics.

- **Live inference panel.** Tokens/sec (prompt vs generation), slot occupancy, queue depth, and KV cache fill, as sparklines. Pull from `/metrics` and `/slots`.
- **Request log view.** A structured tail of recent requests (prompt tokens, gen tokens, latency, cache hit) instead of just raw log text.
- **Alerts.** Thermal throttling, VRAM near ceiling, server unresponsive, or OOM in the log. Surface in the status bar, optionally via a desktop notification or a webhook.
- **Crash detection and optional auto-restart.** You note standalone servers aren't restarted. An opt-in `restart = "on-failure"` with backoff and a crash-reason summary pulled from the log tail would be a nice middle ground without becoming a supervisor.

## Benchmarking and comparison

This is probably the highest "wow" per line of code, since people constantly tune presets blind.

- **`wool bench PRESET`.** Run `llama-bench` or a built-in prompt/generation test, record pp/tg speeds, VRAM used, and load time.
- **Results history per preset.** Store runs in a small JSON or SQLite file and show trends. Then answer "did changing cache_type_k to q8_0 actually hurt speed?"
- **A/B compare view.** Pick two presets, run the same benchmark, show a side-by-side table including quality proxies like perplexity if you want to go further.
- **Sweep mode.** Vary one parameter (batch size, ubatch, threads, ctx) across a range and plot the result. Finds the sweet spot automatically.

## Model management

Right now models are paths you configure by hand.

- **Model discovery.** Scan `model_dirs`, read GGUF metadata, and offer to generate `[models.*]` entries (architecture, param count, quant, native context, chat template).
- **Hugging Face integration.** Search and download GGUFs from inside the TUI with progress, resume, and a pre-download VRAM-fit check ("this quant will not fit; here's one that will"). This ties your estimator directly into a decision people make before spending bandwidth.
- **Disk and orphan view.** Show which models are unused by any preset, total disk usage, duplicates.
- **Quant recommender.** Given a model family and your VRAM, rank available quants by fit and expected quality.

## Router mode depth

- **Memory-aware eviction visualization.** Show which model would be evicted on the next load, and why, given `models_max` and the margin.
- **Warm sets / profiles.** Named groups of models ("coding", "chat+embed") you can load together with one key.
- **Idle and usage stats per model.** Last used, total tokens served, load count. This helps you tune `sleep_idle_seconds`.
- **Endpoint helper.** A panel showing the base URL, plus copy-paste snippets (curl, OpenAI client config, Continue/Open WebUI config) for the running models.

## Built-in chat and testing

A small playground removes the need to leave the app to check that a preset works.

- **Quick chat tab.** Stream a conversation against the running server, with a per-session system prompt, sampling overrides, and tokens/sec shown live.
- **Smoke test on launch.** After a server reports healthy, optionally send a tiny prompt to confirm it generates, and flag the preset as verified.
- **Sampling tuning.** Adjust temperature/top_p/min_p live and save the winner back into the preset.

## Config and workflow ergonomics

- **Remove-override action.** You already list this as a limitation. A "reset to inherited" key in the editor, plus showing which layer each value came from (default / model / preset), would make the inheritance chain much easier to reason about.
- **Effective-config diff.** Show how a preset differs from its model and from defaults.
- **Preset cloning and templates.** `wool preset clone`, or starting a preset from a template like "max context", "max speed", or "low VRAM".
- **Config validation with line numbers.** Surface TOML errors inline in the TUI instead of failing on reload.
- **Export.** Emit a systemd user unit, a shell script, or a docker/compose snippet for any preset, so a tuned launch can become a persistent service.

## Platform and extensibility

- **NVIDIA and Intel telemetry backends.** The biggest adoption unlock. Your layer is already pluggable, and NVML or `nvidia-smi` parsing is a well-trodden path.
- **Multi-GPU awareness.** Per-device VRAM, tensor split suggestions, and main-GPU selection.
- **Remote hosts.** Manage a llama-server on another machine over SSH, with the same TUI. It's a big step, but it turns wool from a workstation tool into a small fleet tool.
- **Machine-readable surface.** A stable `--json` everywhere plus a small local HTTP or socket API, so scripts and other tools can drive wool.
- **Hooks.** `on_start` / `on_stop` / `on_crash` commands per preset, for things like updating a reverse proxy or pinging a notification service.

## Suggested priorities

1. **Auto-fit solver plus calibration.** It deepens the strongest differentiator and is mostly pure logic, which is easy to test.
2. **Benchmark command with history.** It makes tuning evidence-based and gives something screenshot-worthy.
3. **Override provenance and reset-to-inherited.** It's a small change that fixes a real usability wart and sets up the diff view.
4. **NVIDIA telemetry backend.** It multiplies your potential user base.
5. **Live inference panel.** It makes the dashboard feel alive and pairs naturally with benchmarking.

A theme running through all of these: wool is currently great at *launching and watching*, and the next step is *helping you decide*, meaning what to run, with what settings, and whether it was worth it.

