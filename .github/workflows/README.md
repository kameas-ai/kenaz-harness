# Workflows

Four workflows live in this directory.

## `release-please.yml` — semantic versioning

Runs on every push to `main`. Reads conventional-commit subjects since the
last tag and either:

- **Updates a long-lived "release PR"** with the next version, regenerated
  `CHANGELOG.md`, bumped `wails.json` `info.productVersion`, and bumped
  `.release-please-manifest.json`. The PR body lists every commit grouped
  by section.
- **Cuts a tag + GitHub Release** when that PR is merged, then dispatches
  `release.yml` against the new tag so the cross-platform signed binaries
  attach to the proper `vX.Y.Z` release (not just the rolling `main` channel).

Bump rules (with `bump-minor-pre-major: true` while we are < 1.0.0):

| Conventional prefix | Bump |
|---|---|
| `feat:` | minor |
| `fix:` | patch |
| `feat!:` / `BREAKING CHANGE:` body | minor (capped pre-1.0; flip to major after the 1.0 cut) |
| `docs:` `chore:` `ci:` `style:` `refactor:` `test:` `build:` | none (no release PR opened by these alone) |

Source of truth: `release-please-config.json` + `.release-please-manifest.json`
in the repo root.

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

## `release.yml` — main pipeline + binary publish

Runs on:
- pushes to `main` (rolling pre-release at the `main` tag — overwritten each push)
- tagged GitHub Releases (stable, kept indefinitely)
- `workflow_dispatch` (manual, accepts a `version` input)

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

Add a workflow at `.github/workflows/kaneaz-harness-release.yml` in
`kameas-ai/kenaz-docs` that listens for `repository_dispatch` of type
`kaneaz-harness-release`:

```yaml
on:
  repository_dispatch:
    types: [kaneaz-harness-release]
```

The payload is:

```json
{
  "version": "v0.0.0-main-...",
  "tag": "main",
  "release_url": "...",
  "manifest_url": "https://github.com/kameas-ai/kaneaz-harness/releases/download/<tag>/manifest.json",
  "is_release": true
}
```

The docs workflow `curl`s `manifest_url`, writes it to (e.g.)
`static/downloads/kaneaz-harness/manifest.json`, regenerates the docusaurus
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
      "filename": "kaneaz-harness-v0.1.0-linux-amd64.tar.gz",
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
