# M2.2 — Architecture-aware estimate

Part of [M2](../roadmap.md#milestone-m2). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** L · **Depends** M2.1

**Status** planned

**Problem.** The estimate assumes every byte in the GGUF is a GPU-resident
weight and charges full context to every attention layer. That is close for
dense models with uniform blocks, but it is wrong for architectures with large
non-block tensors or sliding-window attention.

A measured `gemma-4-heretic.gguf` run at 65k context makes the gap concrete:

```text
file size                 4.97 GiB
transformer blocks        2.24 GiB
token/per-layer embeddings 2.72 GiB   (llama.cpp kept these out of VRAM)
KV cache, estimated       3.00 GiB
KV cache, actual          ~0.50 GiB   (512-token sliding window)
wool estimate             8.40 GiB
observed server VRAM      3.20 GiB
```

The UI therefore reports a comfortable 65k preset as over budget by 1.5 GiB.
`ornith-1.5-9b` looks accurate because almost all of its file is block,
output and token tensors, so the file-size approximation happens to hold.

**Proposal.** Parse the GGUF tensor table and the architecture metadata needed
to model placement and KV allocation, then split the estimate into GPU and CPU
weight terms.

- Extend `gguf.ReadMeta` to read tensor names, dimensions, types, offsets and
  computed byte sizes without reading tensor data.
- Classify tensors as block weights (`blk.<n>.*`), token embeddings, output,
  per-layer embeddings/projections, or unknown.
- Model which tensors `llama-server` places on the GPU for the supported
  version. Block tensors follow `gpu_layers`; non-block tensors that are not
  offloaded move to a CPU-memory term instead of the GPU weight term.
- Read architecture KV metadata: `sliding_window`, `sliding_window_pattern`,
  `shared_kv_layers`, and the `key_length_swa`/`value_length_swa` variants.
- Compute KV per layer: full context on global layers, at most the sliding
  window on local layers, and honour shared-KV semantics.
- Add `Approximate` and a short reason list to `gguf.Estimate` when the model
  uses tensor names or metadata wool does not understand.

**Scope.**

- In: tensor-aware weight placement, Gemma-style sliding-window/shared KV,
  preserving the existing qwen35 hybrid-attention behaviour, and a visible
  approximate flag.
- Out: MoE expert/`n_cpu_moe` modelling (M2.1), calibration from observations
  (M3.4), multi-GPU splits (PLAT-2), and exact driver/allocator accounting.

**Implementation notes.** Keep the tensor classification table in one place,
next to `internal/gguf`, and test it with real metadata from Gemma 4, Qwen3.5
and a dense Q4 model. Reuse the tensor-info reader added for M2.1 rather than
parsing the table twice. `Estimate` should expose `GPUWeights`, `CPUWeights`,
`KVBytes`, `Overhead` and `Total`, so the UI and `wool doctor --json` can show
which tensors are on the host. Unknown architectures and missing tensor names
must fall back to today’s behaviour with `Approximate: true`, not a guess that
silently changes the fit verdict. Document the supported llama.cpp version and
revisit placement rules when the estimator is calibrated against observations.

**Acceptance.**

- [ ] A synthetic Gemma 4 fixture produces the expected block, non-block and
      CPU-resident byte totals.
- [ ] Sliding-window layers use `min(ctx, sliding_window)` and global layers
      use the full context; the KV total matches a hand calculation.
- [ ] Shared-KV metadata is either modelled or explicitly reported as
      approximate.
- [ ] A Gemma 4 estimate at 65k is within 20% of an observed run on the
      reference 8 GiB card.
- [ ] qwen35 hybrid-attention estimates do not regress.
- [ ] Unknown architectures fall back with a visible `approximate` flag and
      reason.
- [ ] `wool doctor --json` exposes GPU/CPU weight terms and the flag.
- [ ] README and `docs/DESIGN.md` describe the placement and KV rules.

**Risks.** Placement rules are `llama.cpp` implementation details and can
change between builds. Keep the mapping versioned, prefer the conservative
fallback, and validate against the measurement store once M1.3 exists. Do not
let a missing tensor name turn a fitting preset into a hard over-budget
verdict.

**M2 exit criteria:** `wool fit` works for dense and MoE models, the editor can
preview and apply a recommendation, and a saved preset launches.
