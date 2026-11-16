# M4.8 — Endpoint helper

Part of [M4](../roadmap.md#milestone-m4). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P3 · **Size** S · **Depends** none

**Status** shipped

**Problem.** After a server starts, users still have to assemble a base URL and
client snippet by hand.

**Proposal.** `wool endpoint [server|router|MODEL] [--json]` and a TUI panel
with the base URL, model ID and copy-paste snippets for `curl`, the OpenAI
Python client, Continue and Open WebUI. Use the existing clipboard dependency.

**Acceptance.**

- [x] URLs and model IDs match the running instance.
- [x] Copy works or reports a clear clipboard error.
- [x] No API keys or secrets are printed.
- [x] `--json` is stable.

**Implementation notes.**

- `internal/endpoint` owns the formatting so the CLI and the TUI cannot drift.
  `Build` normalizes the base URL, derives `base_url/v1`, and returns the
  versioned `Endpoint` with four snippets in a fixed order: `curl`,
  `openai-python`, `continue`, `open-webui`.
- The CLI resolves a target against the daemon records: no target prefers the
  standalone server and then the router, `server` and `router` are explicit, and
  anything else is a router model ID. An unknown model fails with the router's
  own answer (`router does not list ID`) instead of guessing.
- The standalone model ID comes from the live `/v1/models` response (1s
  timeout), falling back to the config alias and then the model recorded with
  the daemon. The router picks a `loaded` or `sleeping` model, otherwise the
  first ID after sorting, so repeated calls are deterministic. An explicit
  model ID is accepted even when unloaded, because it is still a valid endpoint.
- Snippets use the visible placeholder `not-needed`; llama-server ignores the
  key, so no credential is read, stored or printed. Tests reject `Bearer `,
  `sk-` and `Authorization:` in the rendered output.
- `--json` prints `endpoint.Endpoint` version 1: `version`, `source`,
  `base_url`, `api_base`, `model` and `snippets` (name/text pairs).
- The TUI overlay opens with `E` or `/endpoint [server|router|MODEL]`.
  `tab`/`shift+tab` or `1`..`4` select a snippet, `c` copies it, `u` copies the
  base URL, `y` copies the model ID, `j`/`k` scroll and `esc` closes. A
  clipboard failure is reported in the overlay footer (`clipboard error: …`)
  rather than silently doing nothing.

**M4 exit criteria:** a server can run for a day with live inference visibility,
alerts, crash detection and an explicit restart policy; router groups and usage
stats are usable; the endpoint helper works.
