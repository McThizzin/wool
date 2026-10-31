# PLAT-2 — Multi-GPU awareness

Part of the [platform track](../roadmap.md#platform-track). This is
not on the v1.0 path; see the [feature index](README.md).

**Priority** P3 · **Size** L · **Depends** PLAT-1, M2.1

**Status** blocked — needs multi-GPU hardware and a working NVIDIA backend.

**Problem.** The hardware view and estimator assume one primary GPU.

**Proposal.** Show per-device VRAM and allow fit and launch to target a device
or a tensor split.

- Add `main_gpu` and `tensor_split` to the resolved options and argv.
- `wool fit --gpu 0` uses that device’s budget; without `--gpu`, use the
  aggregate budget only when a tensor split is provided.
- Warn when a model exceeds one card but fits across cards.
- Start with explicit pass-through flags; refine per-device modeling later.

**Acceptance.**

- [ ] Hardware tab shows every GPU.
- [ ] `main_gpu` and `tensor_split` appear in the generated command.
- [ ] Fit respects the selected device.
- [ ] The estimator labels split estimates as approximate.
- [ ] Tests cover flag generation and a two-GPU fixture.
