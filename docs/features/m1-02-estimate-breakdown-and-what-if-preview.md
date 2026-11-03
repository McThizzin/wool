# M1.2 — Estimate breakdown and what-if preview

Part of [M1](../roadmap.md#milestone-m1). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P0 · **Size** M · **Depends** none

**Status** shipped

**Problem.** The UI shows one total and a bar. When a model does not fit, users
cannot tell whether weights, KV cache or overhead is responsible, and tuning
requires saving a value and looking again.

**Proposal.** Surface the existing `gguf.Estimate` decomposition and preview
changes before they are written.

- Dashboard and Doctor show `weights + KV + overhead = total`, the budget, and
  the limiting term.
- The editor recomputes the estimate as the user types, showing the delta from
  the current value. `Enter` still commits; `Esc` discards.
- Show the GPU’s total and currently used VRAM separately. The budget for a new
  server is `total - reserve - other_used`; do not silently subtract the
  running server’s own memory.

**Acceptance.**

- [x] Fit bar is segmented by weights, KV and overhead.
- [x] Changing `ctx`, `cache_type_k/v`, `gpu_layers` or `n_cpu_moe` updates the
      preview without writing the file.
- [x] The preview names the limiting term and the headroom.
- [x] `wool doctor --json` exposes the breakdown.
- [x] Tests cover the arithmetic and the editor preview path.

**Implementation notes.** `gguf.ComputeBudget` derives
`available = total - reserve - other_used`; `ProcessVRAM` reads
`drm-memory-vram` from `/proc/<pid>/fdinfo` so a running standalone server's
own allocation is not counted twice. At the time of this feature `n_cpu_moe`
was not modelled: the preview said so instead of pretending the estimate
changed. [M2.1](m2-01-moe-expert-offload-modeling.md) now models it when tensor
metadata is available and reports approximation reasons otherwise.
