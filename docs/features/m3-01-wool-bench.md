# M3.1 — `wool bench`

Part of [M3](../roadmap.md#milestone-m3). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** L · **Depends** M1.3

**Status** planned

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

- [ ] `wool bench` works without a separately installed `llama-bench`.
- [ ] Results are written to the measurement store with `kind: "bench"`.
- [ ] `--json` includes every recorded field and the raw runs.
- [ ] `Ctrl-C` and failures leave no orphan `llama-server`.
- [ ] The command reports the median, min and max, not just one run.
- [ ] Tests use a fake HTTP server and temporary state.

**Risk.** Benchmarks are noisy. Warm up, repeat, record the spread and the
machine load, and never claim statistical significance from three runs.
