# PLAT-1 — NVIDIA telemetry backend

Part of the [platform track](../roadmap.md#platform-track). This is
not on the v1.0 path; see the [feature index](README.md).

**Priority** P3 · **Size** M/L · **Depends** none

**Status** blocked — needs NVIDIA hardware or a contributor who can test it.

**Problem.** Only AMD sysfs is implemented. NVIDIA is the largest missing
audience for the hardware and fit views.

**Proposal.** Add a backend selected at startup:

- AMD: existing sysfs collector.
- NVIDIA: parse `nvidia-smi --query-gpu=... --format=csv,noheader,nounits`
  with `LC_ALL=C`; avoid cgo for the first version.
- Fake: existing test double.

Add `Vendor` to `GPU`, normalize fan as percent or RPM, and make Doctor report
the detected backend.

**Acceptance.**

- [ ] AMD behavior is unchanged.
- [ ] NVIDIA fixtures parse correctly, including multi-GPU and missing fields.
- [ ] A missing `nvidia-smi` is not an error.
- [ ] No cgo dependency.
- [ ] Doctor names the active backend and vendor.

**Risk.** `nvidia-smi` output varies by driver and container. Keep parsing
defensive and consider NVML later for fields the CLI does not expose.
