# M3.1 — `wool bench`

Part of [M3](../roadmap.md#milestone-m3). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** L · **Depends** M1.3

**Status** shipped

**Problem.** Users tune `cache_type_k`, batch size, ubatch size, threads and
context blind. There is no baseline and no evidence.

**Proposal.** Benchmark a preset or model with a built-in HTTP benchmark, and
optionally adapt `llama-bench`.

```text
wool bench TARGET [--runs 3] [--pp 512] [--tg 128] [--json]
wool bench TARGET --engine llama-bench
wool bench --url http://127.0.0.1:8080 --model ID
```

- Default engine uses `llama-server` itself: start an ephemeral child on a free
  port, wait for health, warm up, send a fixed prompt with `temperature = 0`
  and a fixed seed, then record prompt and generation throughput.
- Prefer the server-reported `timings` from the completion response; fall back
  to `/metrics` counter deltas when timings are absent.
- Record load time, peak VRAM delta, prompt tokens/s, generation tokens/s,
  total time, hardware, preset/config hash and `llama-server` version.
- If a managed `server` is already running and matches the target, attach to it
  with `--url` instead of starting another. Never overwrite the managed state.
- Kill the ephemeral child on completion, error or interrupt.

**Acceptance.**

- [x] `wool bench` works without a separately installed `llama-bench`.
- [x] Results are written to the measurement store with `kind: "bench"`.
- [x] `--json` includes every recorded field and the raw runs.
- [x] `Ctrl-C` and failures leave no orphan `llama-server`.
- [x] The command reports the median, min and max, not just one run.
- [x] Tests use a fake HTTP server and temporary state.

**Implementation notes.** The default engine starts an ephemeral server in a
temporary state/log directory so managed state is never touched. A matching
managed `server` is attached instead; matching compares the model identity and
a launch-config hash that ignores host and port. Prompt and generation
throughput prefer the completion `timings`, fall back to `/metrics` counter
deltas, and finally to wall time with the source recorded on each run. VRAM is
sampled in the background; the recorded delta subtracts the pre-launch baseline
or, for an attached managed server, the process's own `fdinfo` allocation. When
either side is unknown the record says `vram_known: false` instead of guessing.
The optional `llama-bench` engine parses `-o json` and is located next to the
configured `llama-server` or on `PATH`.

**Risk.** Benchmarks are noisy. Warm up, repeat, record the spread and the
machine load, and never claim statistical significance from three runs.
