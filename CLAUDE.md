# CLAUDE.md

Operational doctrine for AI agents working in this repository. Read this before doing release work, planning missions, or running parallel agents. Code conventions live in `CONTRIBUTING.md`; this file is about *workflow*.

---

## Release flow

### Branch + merge pattern

- Cut a `release/v<MAJOR>.<MINOR>.<PATCH>` branch off `origin/main` at the start of a release.
- Land mission work into that branch (one merge commit per mission worktree — see "Parallel sub-agent workflow" below). Use `git merge --no-ff` so the per-mission history is preserved as merge commits and is reachable via `git log --merges`.
- When the release is green, open **one PR from `release/v<X.Y.Z>` to `main`**. The PR is squash-merged. Branch ruleset on `main` disallows merge commits (`mergePullRequest` returns "Merge commits are not allowed"); always use `gh pr merge --squash --delete-branch`.

### Versioning is title-driven

`.github/workflows/tag-on-merge.yml` classifies **the squash-commit subject** (which is the PR title verbatim per `squash_merge_commit_title=PR_TITLE`) and computes the next tag from the latest `vX.Y.Z` git tag. **No CHANGELOG file** — release notes are GitHub's auto-generated "What's Changed" body.

Conventional-commit prefix → bump:

| Prefix | Bump | Notes |
|---|---|---|
| `feat(scope)?:` | minor | `0.x.0`; this is the right prefix for a *feature release roll-up* |
| `fix\|perf\|revert\|deps(scope)?:` | patch | `0.x.y` |
| `feat!:` or `BREAKING CHANGE:` in body | major | capped to **minor** while version is `< 1.0.0` |
| `docs\|chore\|ci\|style\|refactor\|test\|build(scope)?:` | none | no tag, no release dispatch |

`release(...)` is **not** a valid type — it will fail `pr-title.yml` ("Unknown release type"). For a release roll-up PR use:

```
feat: v0.8.0 — extensibility foundation (hooks, slash commands, …)
```

The leading `feat:` (no scope) gives the right minor bump; the `vX.Y.Z` in the subject is human framing.

### Workflows that run on each PR / merge

| Workflow | Trigger | Purpose |
|---|---|---|
| `pr-title.yml` | PR open/edit | Conventional Commits gate (single source of truth for the bump computation) |
| `pr.yml` | PR push | Go lint+vet+codegen drift, Go tests `-race -short`, Frontend tests + typecheck |
| `tag-on-merge.yml` | push to `main` | Reads the squash-commit subject, computes next tag, pushes the tag, creates the GitHub Release, dispatches `release.yml` for binary builds |
| `release.yml` | tag push or `workflow_dispatch` | Cross-platform signed builds (macOS .dmg + Windows NSIS + Linux AppImage), publish to env-specific S3 (dev/stage/prod chosen by trigger) |

### Runner pool policy

**Org policy (kameas-ai): Linux CI runs on the `kameas-ci-*` self-hosted ARM64 pool. GitHub-hosted runners (`ubuntu-latest`, `macos-latest`, `windows-latest`) are reserved for cross-platform release builds in `release.yml` where native compilation requires the target OS** (i.e. Apple + Microsoft). PR-time CI must never use GitHub-hosted runners — the org billing cap will block jobs from provisioning, and Linux jobs have a self-hosted alternative.

Current runner usage:

- `pr-title.yml` → `ci-fast` (self-hosted)
- `pr.yml` (all jobs: lint-go, test-go, test-frontend) → `ci-medium` (self-hosted)
- `tag-on-merge.yml` → `ci-fast` (self-hosted)
- `release.yml` → `ci-fast`/`ci-medium` for coordinators; `macos-latest` for darwin; `windows-latest` for windows. The linux-amd64 and linux-arm64 release builds **still use `ubuntu-latest` / `ubuntu-24.04-arm`** as documented drift — no self-hosted Linux image with the Wails toolchain (libgtk-3-dev + libwebkit2gtk-4.1-dev) exists yet. Tracked.

### Self-hosted runner caveats

The kameas-ci-medium / ci-fast image **is not Debian-family**:
- **No `apt-get`.** Workflows cannot install Linux system deps at job time. The v0.8.0 smoke build job (Wails build with GTK deps) was dropped in v0.9.0 for this reason — cross-platform builds happen in `release.yml` against tags.
- **No pre-installed `gh` CLI on `ci-fast`.** Workflow steps using `gh` (release notes, release creation) must include an install-on-demand block — see `.github/workflows/release.yml:807` for the canonical pattern. Missing this is why PR #109/#111 + the v0.8.0 `tag-on-merge` GitHub Release step failed silently while the tag itself still got pushed.
- **GOPATH persists across jobs.** `actions/setup-go@v5` with `cache: true` will fail with `tar: Cannot open: File exists` because the action tries to extract its module cache on top of an existing one. **Always set `cache: false` on `setup-go@v5`** for self-hosted jobs — Go modules carry over naturally, no caching needed.
- **CGO autodetection is fragile.** `setup-go@v5` sometimes flips `CGO_ENABLED=0` when it doesn't find a compiler at the version-cache path. The runner ships gcc + glibc-devel but the action misses them. For `-race` tests (which require CGO), set `CGO_ENABLED: "1"` explicitly in the step env. See the `test-go` job for the canonical pattern.

### Branch ruleset gotcha

Repo ruleset `15871858` ("main: PR-only with required CI") enforces required status checks **by exact job name**. Renaming or removing a CI job (e.g. `Smoke build (linux/amd64)` in v0.9.0) leaves the ruleset waiting on a check that will never come — every subsequent PR merge blocks indefinitely.

When renaming or removing a required CI job, update the ruleset in parallel:

```bash
rtk proxy gh api /repos/kameas-ai/kenaz-harness/rulesets/15871858 --jq '.rules' > /tmp/rules.json
# edit /tmp/rules.json — drop / rename required_status_checks entries to match
rtk proxy gh api -X PUT /repos/kameas-ai/kenaz-harness/rulesets/15871858 \
  --input <(echo '{"rules": '$(cat /tmp/rules.json)'}')
```

### Burned tags + roadmap-vs-tag drift

Version numbers consumed by infra/release-pipeline iterations are *real tags* on the remote and cannot be reclaimed for feature work. v0.6.0 and v0.7.0–v0.7.5 were burned by release-pipeline migration commits in May 2026. `docs/roadmap.md` has a "Note on burned version numbers" preamble that records the shift; always check git tags + the roadmap before slotting a planned minor.

`docs/roadmap.md` slot labels (e.g. "v0.8.5 — UX maturity") are **planning artifacts, not version contracts**. The next tag is whatever `tag-on-merge.yml` bumps from the latest tag using the PR's Conventional Commits prefix. Example: v0.8.5-themed UX work landed as **`v0.9.0`** because v0.8.4 + `feat:` = v0.9.0. Accept the drift; don't contort PR titles to hit a planning label. When the user references a roadmap slot, translate to the current tag context before acting.

---

## Mission system (kitty-specs/)

Each feature is a *mission* under `kitty-specs/<slug>-<ULID>/`:

```
kitty-specs/<slug>-<ULID>/
  meta.json     # mission_id, friendly_name, mission_type, target_branch
  spec.md       # what + why; the contract for the work
  plan.md       # how the work decomposes
  tasks.md      # WP01..WP0N — the unit of agent dispatch
  research/     # optional supporting notes
  checklists/   # optional gating checklists
```

Conventions:

- **One WP = one logical commit** with subject `feat(<area>): WP0X — <one-line summary>`.
- WPs are designed to be parallelizable across missions; intra-mission they're usually sequential.
- The spec is the source of truth — never plan from an `tasks.md` alone if the spec contradicts.
- When a mission ships, move its directory to `kitty-specs/_archive/`. Update `docs/roadmap.md` in the same PR.

### Status-of-the-fleet

`kitty-specs/_archive/SESSION_HANDOFF_<date>.md` files are running notes left between work sessions — read recent ones for context if you're picking up mid-stream.

---

## Roadmap (docs/roadmap.md)

- **The roadmap file is `.gitignore`d** (intentional — keeps minor drafting churn out of PR review). Edits land via direct push or are mirrored into a release-roll-up PR description.
- Source of truth for *what's slotted where*. The mission spec is source of truth for *what the work is*.
- Update when (a) a mission ships → move row from upcoming to "Already shipped", (b) a new spec lands → add it to the most-appropriate upcoming minor, (c) priorities flip → edit the version assignment in place.
- Ordered chronologically forward, not by priority. Within a release the missions sit roughly in dependency order.

---

## Parallel sub-agent workflow (for big releases)

When a release ships 4+ missions, dispatch them through worktree-isolated sub-agents in waves.

### Wave structure

1. **Map deps first.** Spawn an Explore agent to read `spec.md` + `plan.md` + `tasks.md` for every in-scope mission and produce a written dependency map. Identify shared-file conflict zones before any code agent touches the tree.
2. **Wave 1 = foundation + truly-parallel WPs.** Dispatch one agent per mission with `isolation: "worktree"` and `subagent_type: "general-purpose"` (or `model: "sonnet"`). Each agent commits per-WP into its own `worktree-agent-*` branch.
3. **Merge order = dependency order.** Foundation mission first (e.g. hooks), then satellites. Merge each branch into `release/v<X.Y.Z>` with `--no-ff`. Expect conflicts in the shared-file zones (see next section); resolve by *combining both sides additively* — these are almost never genuine semantic conflicts.
4. **Wave 2 = work that needs Wave 1 contracts.** Dispatch only after Wave 1 is merged so Wave 2 agents see the real foundation, not a pre-merge stub.
5. **Wave 3 = integration + ship.** Full-repo tests, frontend build, then open the release PR to main.

### Shared-file conflict zones (high-confidence overlaps in this repo)

When merging multiple mission branches, expect simultaneous additions in these files. Resolve by keeping *all* additions:

- `core/policy/cedar/types.go` — `Action*` constants + `EntityType*` constants + `*UID()` helpers
- `core/rpc/api.go` — `HarnessAPI` wiring (constructor body, `Slash()` / `Elicit()` / etc. getters, `slashStore` / `slashDispatch` early construction)
- `core/rpc/builtins_wiring.go` — imports + `registerBuiltinTools` parameter list + tool registrations + `builtinEnabledPredicate` cases
- `core/rpc/bindings.go` + `frontend/wailsjs/go/rpc/Bindings.{js,d.ts}` — Wails-bound method surface
- `frontend/src/lib/harnessClient.ts` + `frontend/src/lib/types.ts` — typed client interfaces, runtime wiring, fake stubs, wire-type definitions
- `core/context/audit/audit.go` — `Kind*` constants + payload structs
- `core/hooks/hooks.go` — `Event*` constants + `AllEvents` slice (the slice is what `isKnownEvent` validates against; new events must be added there too)
- `core/hooks/runner.go` — `BuiltinRegistry` fields + constructor map initialisers

### Sub-agent dispatch hygiene

- **Brief like a colleague who just walked in.** Each sub-agent starts cold — include spec paths, repo orientation, contract pointers, build/test commands, and explicit instruction to commit per-WP without pushing.
- **Tell the agent what to defer.** If a WP needs a contract from a not-yet-merged mission, say so — agents may otherwise stub something inferior or stall.
- **Trust but verify.** Sub-agent reports describe what they *intended* to do. Re-run the build/test commands on the merge target, especially `go test -race -short ./core/...` which catches issues local agents may have missed.

---

## Codegen + race discipline

### Agentgraph manifests

Editing `core/agentgraph/nodes/manifests/<kind>.yaml` requires three downstream commits in the same change:

```bash
go generate ./core/agentgraph/...
# commits both core/agentgraph/attrs_gen.go and core/agentgraph/wire_gen.go
```

Plus updating **two** tests that pin the canonical node-kind set:

- `core/agentgraph/spec_test.go` `TestNodeKindEnumeration.want` (the alphabetised list of `NodeKind*` constants)
- `core/agentgraph/nodes/catalog_test.go` `wantCallable` count and `ListByCategory(<category>)` counts

CI runs `scripts/ci/check-codegen.sh` and fails on drift. Run it locally before pushing.

**Valid manifest attr types** (the codegen rejects others — `integer` is *not* one):

```
any, bool, int, float, string, text, enum, json, object, branch_id,
node_id_ref, model_ref, messages, messages_ref, activity_ref,
compute, control, marker, read, state, write
```

### Race-safe test fakes

CI runs `go test -race -short ./core/...`. Any test fake that receives writes from a goroutine *and* is read by the test body needs a mutex + snapshot helper. Canonical pattern:

```go
type fakeEmitter struct {
    mu      sync.Mutex
    emitted []emitRecord
}
func (f *fakeEmitter) Emit(...) { f.mu.Lock(); defer f.mu.Unlock(); f.emitted = append(...) }
func (f *fakeEmitter) snapshot() []emitRecord {
    f.mu.Lock(); defer f.mu.Unlock()
    out := make([]emitRecord, len(f.emitted)); copy(out, f.emitted); return out
}
```

All test-side reads go through `snapshot()`. Locally `go test -count=1 -short` may pass without `-race`; CI will catch it.

---

## Local dev commands

```bash
# Build
wails build

# Live dev
wails dev

# Backend tests (mirror CI)
go test ./core/... -race -count=1 -short

# Frontend tests
cd frontend && ./node_modules/.bin/vitest run --reporter=basic

# Codegen drift check
bash scripts/ci/check-codegen.sh

# Credential hygiene check
bash scripts/ci/check-no-cred-bytes-in-rpc.sh
```

---

## Worktree gotchas

The harness sub-agent system creates `.claude/worktrees/agent-<id>/` worktrees that share the repo's `.git` database. A few things to watch:

- `git checkout <branch>` in the main repo will fail with "already used by worktree" if any sub-agent worktree has that branch checked out. Use `git worktree list` to see who's holding what.
- Force-moving `release/v<X.Y.Z>` while sub-agents are still running can desync them. Prefer creating commits on top vs. resetting.
- When you need to run tests against a temporary worktree at `/tmp/...`, symlinking `frontend/node_modules` does *not* work (Vite resolves transitive deps from absolute paths). Either `npm install` inside the temp worktree or run tests from the main checkout.

---

## Quick reference: shipping a feature release

1. `git checkout -b release/v<X.Y.Z> origin/main`
2. Dispatch parallel sub-agents per mission with `isolation: "worktree"`.
3. Merge each completed worktree branch with `git merge --no-ff worktree-agent-<id>`. Resolve conflicts in the known zones additively.
4. Run `go test -race -short ./core/...` + `vitest run` + `bash scripts/ci/check-codegen.sh` on the merged release branch.
5. `git push -u origin release/v<X.Y.Z>`.
6. `gh pr create --base main --head release/v<X.Y.Z> --title "feat: v<X.Y.Z> — <theme>"` (NOT `release(…)`).
7. Wait for `pr.yml` + `pr-title.yml` green. Fix any CI issues with a follow-up commit on the same branch.
8. `gh pr merge <num> --squash --delete-branch`.
9. `tag-on-merge.yml` auto-tags + dispatches `release.yml`. Verify the tag is pushed and the GitHub Release is created (the latter is failing today — see "Self-hosted runner gotcha" above).
10. Archive shipped specs to `kitty-specs/_archive/`. Update `docs/roadmap.md`. Open follow-up PRs for any tracked patch-lane items.
