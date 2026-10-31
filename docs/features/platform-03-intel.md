# PLAT-3 — Intel GPU telemetry backend

Part of the [platform track](../roadmap.md#platform-track). This is
not on the v1.0 path; see the [feature index](README.md).

**Priority** P3 · **Size** M · **Depends** none

**Status** blocked — needs Intel GPU hardware or a contributor who can test it.

**Problem.** The hardware view is AMD-only. Intel Arc and integrated GPUs are
common on Linux laptops, but the telemetry surface is fragmented between i915
and xe.

**Proposal.** Add an Intel backend behind the existing `sysinfo.Backend`
interface. Prefer sysfs (`/sys/class/drm/card*/device/...`) where it exposes
memory, busy, temperature and power; fall back to `intel_gpu_top` parsing when
sysfs is incomplete. Do not add a hard dependency on the tool.

**Acceptance.**

- [ ] AMD behavior is unchanged.
- [ ] Fixtures cover i915 and xe paths, including missing fields.
- [ ] A missing sensor is not an error.
- [ ] Doctor names the active backend and vendor.
- [ ] No new cgo dependency.

**Risk.** Intel telemetry varies widely by generation and driver. Treat this as
community-supported until there is hardware to test on.
