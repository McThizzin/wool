# Releasing wool

Releases are cut from `main` with an annotated tag. The tag triggers
[`.github/workflows/release.yml`](../.github/workflows/release.yml), which runs
the tests, cross-builds Linux binaries, writes checksums and publishes a GitHub
release with generated notes.

## Versioning

- `v0.x.y` while the roadmap milestones are in progress. The minor version
  tracks the milestone theme (`v0.2` is M1, `v0.3` is M2, and so on).
- Patch releases are fixes that do not change the config schema or CLI surface.
- `v1.0.0` is reserved for the daily-driver exit criteria in
  [`roadmap.md`](roadmap.md).

## Before tagging

1. `main` is green in CI.
2. The working tree is clean and up to date with `origin/main`.
3. The roadmap and feature index reflect what shipped.
4. `README.md` and `docs/DESIGN.md` describe the current behavior, not the
   previous release.
5. Run the checks locally:

   ```sh
   gofmt -l .
   go vet ./...
   go build ./...
   go test -count=1 ./...
   ```

6. Verify a clean clone still builds and the quick start works:

   ```sh
   git clone "$(git remote get-url origin)" /tmp/wool-release-check
   cd /tmp/wool-release-check
   go build -o wool ./cmd/wool
   XDG_CONFIG_HOME=/tmp/wool-release-config \
   XDG_STATE_HOME=/tmp/wool-release-state \
     ./wool version
   XDG_CONFIG_HOME=/tmp/wool-release-config \
   XDG_STATE_HOME=/tmp/wool-release-state \
     ./wool config init
   XDG_CONFIG_HOME=/tmp/wool-release-config \
   XDG_STATE_HOME=/tmp/wool-release-state \
     ./wool config path
   ```

   `wool doctor` and `wool run` need `llama-server` and a real model; run them
   separately on a machine that has both.

## Cut the release

```sh
git checkout main
git pull --ff-only
git tag -a v0.1.0 -m "wool v0.1.0"
git push origin main
git push origin v0.1.0
```

Watch the release workflow:

```sh
gh run watch "$(gh run list --workflow=release.yml --limit=1 --json databaseId --jq '.[0].databaseId')"
```

Then confirm the published release and the version string:

```sh
gh release view v0.1.0
curl -fsSL https://github.com/McThizzin/wool/releases/download/v0.1.0/wool-linux-amd64 \
  -o /tmp/wool-linux-amd64
chmod +x /tmp/wool-linux-amd64
/tmp/wool-linux-amd64 version
# wool v0.1.0 (commit <sha>, built <timestamp>)
```

## If the release fails

- Fix the problem on `main`, then delete and recreate the tag:

  ```sh
  git tag -d v0.1.0
  git push origin :refs/tags/v0.1.0
  git tag -a v0.1.0 -m "wool v0.1.0"
  git push origin v0.1.0
  ```

- Delete the draft or partial GitHub release before re-running.
- Never move a tag that users may already have downloaded; cut a patch release
  instead.

## Release notes

The workflow uses GitHub's generated notes. Before publishing, edit the release
to add:

- the milestone and its user-facing outcome;
- breaking config or CLI changes, if any;
- known limitations that affect the primary persona (one 8–16 GB GPU).
