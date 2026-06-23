# Workflows

Four workflows live in this directory.

## `tag-on-merge.yml` — semantic versioning

Runs on every push to `main`. Parses the commit subject (= squashed PR
title, since the `main` ruleset forces squash + `PR_TITLE` subject):

| Conventional prefix | Bump | Release? |
|---|---|---|
| `feat:` (or `feat(scope):`) | minor (`0.x.0`) | yes — instant |
| `fix:` `perf:` `revert:` `deps:` | patch (`0.x.y`) | yes — instant |
| `feat!:` / `BREAKING CHANGE:` in body | major, **capped to minor while `< 1.0.0`** | yes — instant |
| `docs:` `chore:` `ci:` `style:` `refactor:` `test:` `build:` | none | no |
| Anything that isn't a conventional commit | none | no |

On a release-worthy commit it: looks up the latest `vX.Y.Z` tag, computes
the next version, generates GitHub's "What's Changed" auto-release notes
(grouped by PR + contributor) since the previous tag, creates an
annotated tag + GitHub Release, then dispatches `release.yml` against
the new tag to build + sign + publish cross-platform binaries.

No CHANGELOG file is maintained — the per-release auto-generated notes
are the canonical changelog. No release PR — the merge of the original
PR *is* the release.

Source of truth for the next version: `git tag --list 'v*' --sort=-v:refname`.

## `pr-title.yml` — conventional-commits enforcement

Runs on every PR open / edit / synchronize. Validates the PR title against
the conventional-commits spec and the type list above. Required by the
`main` branch ruleset, so a non-conventional title cannot merge.

Because the `main` branch settings squash with `squash_merge_commit_title=PR_TITLE`,
the PR title becomes the merge-commit subject verbatim — which is exactly
what release-please reads to compute the next version. Single point of truth.

## `pr.yml` — pull-request gate

Runs on every PR against `main` (and on direct pushes to `main` to keep the
default-branch badge live).

Jobs:

| Job | Runner | Purpose |
|---|---|---|
| `lint-go` | ubuntu | `go vet`, `golangci-lint`, codegen drift, all `scripts/ci/check-*` privacy + isolation guards |
| `test-go` | ubuntu | `go test ./core/... -race -count=1 -short` |
| `test-frontend` | ubuntu | `npm ci` + `vitest --run` + typecheck (advisory) |
| `build-smoke` | ubuntu | `wails build` for `linux/amd64` to catch compile regressions before merge; uploads a 7-day artifact |

`build-smoke` waits for the three checks to pass; the rest run in parallel.

## `release.yml` — tagged binary publish

Runs **only** for proper SemVer tags. Three entry points:
- `release-please` dispatches it via `gh workflow run release.yml --ref vX.Y.Z` after a release PR is merged (the primary path).
- A human creates a Release in the GitHub UI (rare manual path).
- `workflow_dispatch` with an explicit `version=vX.Y.Z` (escape hatch / re-run).

There is **no rolling pre-release channel** — non-versioning merges (chore/docs/ci/style/etc.) do not produce binaries. PR-time `build-smoke` covers build-health verification on every commit.

Jobs:

| Job | Runner | Purpose |
|---|---|---|
| `derive-version` | ubuntu | Resolves the version label and whether this is a stable release |
| `build` (matrix) | ubuntu / macos-13 / macos-14 / windows | Native build per platform: `linux-amd64`, `darwin-amd64`, `darwin-arm64`, `windows-amd64`. Each archives binaries + writes sha256 |
| `release` | ubuntu | Downloads all matrix artifacts, creates / updates the GitHub Release, generates `manifest.json` listing every asset with URL + sha256 + size |
| `notify-docs` | ubuntu | Fires a `repository_dispatch` event at `kameas-ai/kenaz-docs` so the docusaurus site picks up the new download URLs |

### Secrets you need to configure on the repo

| Secret | Where | Purpose |
|---|---|---|
| `GITHUB_TOKEN` | Auto-provided | Used by `release` to create / upload to releases |
| `DOCS_DISPATCH_TOKEN` | **You add it** | Personal Access Token (classic) with `repo` scope on `kameas-ai/kenaz-docs`. Used by `notify-docs` to send the repository_dispatch event. If not set, the dispatch step skips with a friendly note. |

**Add `DOCS_DISPATCH_TOKEN`**:
1. Generate at https://github.com/settings/tokens (classic, `repo` scope).
2. Repo → Settings → Secrets and variables → Actions → New repository secret.

### docusaurus side (kenaz-docs)

Add a workflow at `.github/workflows/kenaz-harness-release.yml` in
`kameas-ai/kenaz-docs` that listens for `repository_dispatch` of type
`kenaz-harness-release`:

```yaml
on:
  repository_dispatch:
    types: [kenaz-harness-release]
```

The payload is:

```json
{
  "version": "v0.0.0-main-...",
  "tag": "main",
  "release_url": "...",
  "manifest_url": "https://github.com/kameas-ai/kenaz-harness/releases/download/<tag>/manifest.json",
  "is_release": true
}
```

The docs workflow `curl`s `manifest_url`, writes it to (e.g.)
`static/downloads/kenaz-harness/manifest.json`, regenerates the docusaurus
download page from that JSON, commits, and triggers the docs deploy.

The `manifest.json` schema is:

```json
{
  "version": "v0.1.0",
  "tag": "v0.1.0",
  "commit": "abcd1234...",
  "released_at": "2026-04-27T...",
  "release_url": "https://github.com/.../releases/tag/v0.1.0",
  "assets": [
    {
      "platform": "linux-amd64",
      "os": "linux",
      "arch": "amd64",
      "filename": "kenaz-harness-v0.1.0-linux-amd64.tar.gz",
      "url": "https://github.com/.../releases/download/v0.1.0/...",
      "sha256": "...",
      "ext": "tar.gz",
      "size_bytes": 12345678
    },
    ...
  ]
}
```

## Notes + caveats

- macOS binaries are **unsigned**. Notarization is out of scope; users will
  need to right-click → Open on first launch. Code signing is a follow-up
  (requires an Apple Developer ID + secret plumbing).
- Windows binaries are **unsigned** too. Defender SmartScreen will warn on
  first run. Code signing is a follow-up.
- Wails on Linux is built against `webkit2gtk-4.0` (the older API). If the
  Ubuntu runner image moves to webkit2gtk-4.1 only, swap the build tag to
  `webkit2_41`.
- The `build-smoke` job in `pr.yml` doesn't run the matrix — that's
  intentional. It exists to catch compile breakage on PR; the full matrix
  fires only on merge.
