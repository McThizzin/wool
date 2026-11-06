# M5.5 — Flag catalog and fuzzy reference

Part of [M5](../roadmap.md#milestone-m5). See the
[roadmap](../roadmap.md) for sequencing and the [feature index](README.md).

**Priority** P2 · **Size** M · **Depends** M5.3, M5.4

**Status** planned

**Problem.** Users do not know which `llama-server` flags exist, what they do,
or which ones take a value. The current reference is a hand-captured table that
drifts from the installed binary, and a typo only fails at launch.

**Proposal.**

- Embed a catalog generated from `llama-server --help`: canonical name, aliases,
  value shape, description, default, environment variable, category, and the
  source `llama.cpp` version and commit.
- Add `tools/flagsgen` to regenerate the catalog from a local `llama.cpp`
  checkout, with curated overrides for help text that cannot be parsed
  reliably. Generate the Markdown reference from the same data.
- Add a fuzzy picker over the catalog, reachable from the editor's add-flag
  flow and from `/flags`. Search names, aliases and descriptions; show the
  description, default and value shape; prompt for a value when required; warn
  about conflicts with existing options.
- Add `wool flags [--json] [--search TEXT] [--check]`. `--check` parses the
  installed `llama-server --help` and reports new, missing and changed flags
  against the embedded catalog.
- Report the catalog version and installed `llama-server` version in Doctor.
- Keep catalog checks advisory by default: unknown and deprecated flags warn
  but still launch. Strict mode can promote warnings to errors.

**Scope.**

- In: catalog data model, generator, fuzzy picker, `wool flags`, generated
  reference documentation and Doctor version reporting.
- Out: downloading or building `llama.cpp`, background auto-updates, repeated
  and ordered flag arguments, and modelling arbitrary flags in the estimator.

**Acceptance.**

- [ ] Every catalog entry has a name, aliases, value shape, description,
      source version and commit.
- [ ] Fuzzy search matches names, aliases and descriptions with deterministic
      ranking.
- [ ] Selecting a flag adds the correct bare or value-bearing form and warns
      about conflicts.
- [ ] `wool flags --json` has a stable schema; `--check` reports new, missing
      and changed flags.
- [ ] Unknown or deprecated flags warn but do not block a launch unless strict
      mode is requested.
- [ ] The Markdown reference regenerates from the catalog without hand edits.
- [ ] Doctor shows the catalog and installed binary versions.
- [ ] Parser tests use fixtures from more than one help-text version.

**Risk.** `llama-server --help` is not a stable API. Parse conservatively, keep
curated overrides, and never turn the catalog into an allowlist. A stale
catalog must not stop a user from passing a newer flag.
