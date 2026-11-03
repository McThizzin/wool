# M2.1 — MoE expert offload modeling

Part of [M2](../roadmap.md#milestone-m2). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P1 · **Size** L · **Depends** none

**Status** shipped

**Problem.** `n_cpu_moe` is ignored by the estimator, so a MoE model with
experts offloaded to CPU can show as over-VRAM even when it fits comfortably.
That is exactly the case where 8 GB cards need help.

**Proposal.** Model expert tensors and CPU offload.

- Extend the GGUF reader to parse the tensor-info table: name, dimensions,
  type, offset and computed byte size. It already reads the tensor count and
  stops after the KV block.
- Identify expert tensors by architecture-specific naming and sum their bytes
  per layer.
- For `n_cpu_moe = N`, move the expert bytes of the offloaded layers from the
  GPU weight term to a CPU-memory term, matching the `llama-server` semantics
  for the supported version. Keep KV cache on the GPU.
- If the architecture or tensor names are unknown, fall back to the current
  behavior and mark the estimate `approximate`; Doctor says
  “MoE detected but not modeled”.

**Acceptance.**

- [x] Tensor metadata is parsed without reading tensor data.
- [x] A synthetic MoE GGUF fixture produces the expected expert byte total.
- [x] Increasing `n_cpu_moe` monotonically lowers the GPU estimate.
- [x] The estimate reports GPU and CPU memory separately.
- [x] Unknown architectures degrade to the old estimate with a visible flag.
- [x] The “estimate ignores n_cpu_moe” Doctor note disappears when modeled.

**Implementation notes.** `gguf.Meta` carries the tensor-info table and
`gguf.Estimate` exposes `GPUWeights`, `CPUWeights`, `Approximate` and a reason
list. Routed experts are matched by `blk.<n>.` names containing `.ffn_` and
`_exps.`; shared experts and routers stay with their layer. When the table is
missing, a type is unknown, or a model declares experts without recognised
expert tensors, wool falls back to the file-size fraction and marks the
estimate approximate. Non-block tensor placement and sliding-window KV are
left to [M2.2](m2-02-architecture-aware-estimate.md).

**Risk.** Tensor naming varies across architectures and llama.cpp versions.
Keep the mapping in one table, test it against real metadata, and prefer a
conservative fallback over a wrong number.
