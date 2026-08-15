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

### Prefix convention for the patch lane

`fix\|perf\|revert\|deps` all produce patch bumps per the table above, but in practice **default to `fix:`** for everyday patch-lane work. The other patch prefixes are special-purpose:

- `fix:` — bug fixes, type-safety holes, dead-control wiring, broken UX (the everyday case). **Use this unless one of the below clearly applies.**
- `perf:` — performance-only changes with no behaviour change. Rare.
- `revert:` — reverting a specific prior commit. Use the `git revert` default subject.
- `deps:` — dependency bumps only (Go modules, npm). The change must be purely the dep version + any minimal call-site adjustments.

Examples from the v0.10.x patch lane:

| Right | Wrong |
|---|---|
| `fix(frontend): declare Settings_{Get,Set}MCPAutoRestart on WailsBindingsLike` | `chore(frontend): add missing binding declarations` (no-bump; the gap is a real type-safety hole) |
| `fix(workflows): align local schedule type with WorkflowsScheduleEntry` | `refactor(workflows): use canonical type` (no-bump; the cast was failing tsc) |
| `chore(frontend): drop unused imports + vars surfaced by vue-tsc` | `fix(frontend): drop dead code` (`chore:` is correct here — no semantic change, no version bump needed) |

The decision boundary: **if the working tree had observable wrong behaviour or a type-safety hole that hid a runtime risk, prefix `fix:` and let SemVer bump the patch.** If the change is pure cleanup with no observable difference (formatting, unused imports, comment rewording), use `chore:` / `docs:` / `style:` / `refactor:` so the patch lane stays signal-rich.

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

### Environments & promotion (release downloads)

The harness is a desktop app; its "environments" are the env-specific S3 + CloudFront **download** channels that `release.yml` publishes signed builds to. The **trigger picks the env** (see the header of `.github/workflows/release.yml`):

| Trigger | Env | Channel |
|---|---|---|
| **push to `main`** (any commit) | **dev** | rolling "latest" → `s3://kameas-ai-dev-releases-use2` → dev.downloads.kameas.ai |
| **tag `v*-rc*`** (e.g. `v1.2.0-rc1`) | **stage** | release candidate → `s3://kameas-ai-stage-releases-use2` → stage.downloads.kameas.ai |
| **tag `v*`** (no `-rc`) **or** `workflow_dispatch` with `version=` | **prod** | stable → `s3://kameas-ai-prod-releases-use2` → prod.downloads.kameas.ai |

**Develop → promote ladder:**
1. **Develop** locally with `wails dev` + the test commands under *Local dev commands*. Open a PR; `pr.yml` (lint/vet/codegen-drift, `-race -short` Go tests, frontend tests+typecheck) is the gate.
2. **Merge → main** ⇒ a **dev** rolling build is published automatically (every merge, incl. `chore`/`docs`). Smoke it from the dev channel.
3. **Stage (optional RC):** push a `v<X.Y.Z>-rc1` tag manually ⇒ a **stage** release-candidate build. Soak it.
4. **Prod:** a stable `v<X.Y.Z>` tag ⇒ a **prod** build. `tag-on-merge.yml` auto-cuts this stable tag from a `feat:`/`fix:` PR — so a normal feature merge yields **both** a dev rolling build (from the push) **and**, once the tag lands, a prod build. To gate prod behind an RC, push the `-rc` tag (and soak) **before** the stable tag, or land the work as a no-bump prefix (`chore`/`docs`/`refactor`) so only the dev rolling build is produced until you choose to tag.

Note: `tag-on-merge` cutting the stable tag immediately means **a `feat:`/`fix:` merge ships to prod downloads without a stage stop** unless you deliberately RC-tag first. Treat that as the default and plan RC soak explicitly when a release warrants it.

---

## Release ritual: unwired sweep

**Runs first on every release branch, before any feature work.** Owner
directive: find-and-eliminate unwired code is a ritual, not a campaign. Its
fixes are small and scattered, so running it ahead of mission merges avoids
conflicts and means the release builds on a verified-wired base.

*Unwired* = built but not reached: a tool no wiring site imports, a setting
nothing branches on, an output port with no reader, a prop no parent passes,
a gate function with no caller. The failure mode is not a missing feature —
it is a **lie**: a toggle that reports it is on, a schema that promises the
model a capability, a comment asserting an invariant nothing enforces.

### The five passes

1. **3-pass read detection** for output ports, node attrs and struct fields:
   Go reads **+** YAML `condition:` / attr-string references in
   `core/rpc/views/agentgraph/library/*.yaml`, `core/agentgraph/graphs/*.yaml`,
   `core/agentgraph/activities/yaml/*.yaml` (alias-resolved via `kindAliases`
   in `wire_gen.go`) **+** frontend reads. A naive Go grep over-reports ~3x.
2. **Registration-vs-consumption diffs.** Pair every Register/Emit/Publish
   with a real consumer: builtin tools ↔ `builtinEnabledPredicate` cases,
   event kinds ↔ emit sites ↔ readers, broker topics ↔ subscribers, Wails
   bindings ↔ `harnessClient.ts` ↔ an actual `.vue` caller, RPC methods ↔
   served-mode dispatch. Check **both** directions — the existing tripwire
   only walks registered→predicate.
3. **Zero-call-site exported control-flow.** The I10 heuristic
   (`Evaluate*`/`Enforce*`/`Authorize*`/`Check*`/`Verify*`/`Guard*`/`Permit*`/
   `*Gate`), widened one notch to exported constructors and `With*` options
   in code new since the last release —
   `git diff --stat <last-tag>..HEAD` scopes the fresh surface.
4. **Dials-to-consumer tracing.** Every Settings field, autonomy knob and
   graph dial must reach an **observable behaviour**. A read that only
   copies the value into another struct is not consumption — follow it to a
   branch. This pass has found four inert toggles across the campaign.
5. **Frontend placeholders.** Hardcoded zero/empty props and literals with
   "placeholder" / "TODO" / "until X wires" / "for now" comments; declared
   props no parent passes; `defineExpose` members only tests reach.

### Rules

- Every find gets **wired, deleted, or dated-justified**. Deleting needs
  positive no-consumer proof *and* deletion of the tests that were its only
  readers.
- A justification names the **blocker** and the **owner** — the change that
  will delete the line. "We'll get to it" is not a reason.
- **Gate-extension rule:** if a find represents a class the existing gates
  cannot see, extend a gate in the same commit, with a planted-violation
  proof in `scripts/ci/gates_can_fail_test.go`.
- Allowlists shrink monotonically. Nothing gets added without a date.

### Disposition: delete vs. finish

"Unwired" is a finding, not a verdict. The sweep's job is to end the
**lie** — a toggle that reports it is on, a tool advertised to the model
that always fails, a docstring promising a route that does not exist. It
is *not* to shrink the tree. A useful half-built feature deleted is a
product decision made by a linter.

**Delete** when the code has no future, and say which:

- A **live substitute** does the same job (`ScopePicker` →
  `AttachmentTreePicker`; the toast trio → `useEventToasts`).
- A **documented product retirement** superseded it (corpora → contexts;
  the update-dot → the Help menu, per `os-menu-bar-01NDFSEX16` §FR-006).
- It is **rival infrastructure** — a second implementation of something
  the app already does one way (`lib/slashcmd.ts`, `lib/useStream.ts`).
- The **whole subsystem has no producer and no product intent**, and
  nobody will claim it. Deleting the consumer half of a *wanted* feature
  is the wrong call — it leaves the gap and destroys the tested work.

**Spec it and finish it** when the feature is real:

- The **backend is live and only the UI is missing** — the cheapest
  possible win, and the strongest signal someone meant it (e.g.
  `RecoveryCodeFlow`: RPCs registered, adapter assigned in production,
  no other recovery surface exists).
- It is the **only surface for a real capability** — deleting it removes
  the capability from the product, not just from the tree.
- It is **trust- or compliance-relevant** (consent, permissions,
  denials, audit).
- Its absence makes something else **lie** — a registered tool, a
  docstring, a settings field with no writer.

Finishing means a mission under `kitty-specs/`, not a drive-by mount.
Mounting a panel whose Go knob is inert just moves the lie from the
backend to the UI: wire the consumer first, in the same PR.

**Escalate** when the call is genuinely product, not technical — two
rival implementations where the weaker one ships, or a feature nobody
can say is wanted. Record the question; do not resolve it by deleting.

Every disposition names its class. "Deleted — no importers" is not a
reason; "deleted — `AttachmentTreePicker` is the live substitute" is.

### Two blind spots the file-level scans cannot see

Both were found the hard way in the 2026-08-14 sweep. Neither is covered by
any gate; both need a human or an agent explicitly looking.

1. **Dead code inside a live file.** Deleting ten orphaned components saved
   826 bytes of JS — Vite had already tree-shaken every unreferenced `.vue`,
   so they cost nothing at runtime. The *entire* saving came from dead code
   living inside `harnessClient.ts`, `types.ts` and `useHarnessAPI.ts` (a
   composable, its handler `Set`, and a `_emitDenialForTest` with no
   callers). An orphan-FILE scan will never surface these. Grep for exported
   symbols with no non-test reader, not just for unreferenced files.

2. **Test fixtures that bypass the layer under test.** Composition fixtures
   built on `session.NewMemoryStore()` skip SQL encode/decode entirely.
   Four separate SQL-path mutations survived the full suite that way, two of
   which would have silently disabled a whole feature for every real user
   while CI stayed green. **Anything asserting persistence, compaction of
   persisted history, or an FTS index must drive real sqlite.** The same
   blind spot hid the WP06 finding that the tool-pair clamp was a no-op on
   every move-bearing session: every fixture filled the pair markers by hand.

### Where the ledger lives

`docs/unwired-ledger.md` — index of the gated findings (which allowlist
holds what) plus the full text of the **ungated** ones, which have no
allowlist to live in. Read it first; it is the previous sweep's handoff.
Per-symbol justifications stay with their gate in
`scripts/ci/allowlists/`.

### Tooling footguns

- Use `rtk proxy grep` / `rtk proxy git` for anything multi-file or
  load-bearing — the plain wrappers silently truncate.
- **Do not pipe `rtk proxy grep` into another `rtk proxy grep`.** Even the
  proxied form truncates on a double pipe — this dropped
  `views/audit/AuditView.vue:16` during the 2026-08-14 frontend
  orphan-deletion sweep and nearly produced a false orphan verdict for
  `EventStreamRow.vue`. Pipe into `/usr/bin/grep` instead.
- `gofmt -l` flags ~337 files from local-toolchain drift (Go 1.26 vs
  go.mod 1.25). Never "fix" a file you did not otherwise touch.
- `go test` needs `-p 4`; mcp stdio tests time out at default parallelism.
- Run only `check-*.sh` scripts from `scripts/ci/`; others are not
  read-only. `check-csp.sh` needs a frontend build and
  `check-release-integrity.sh` needs `REPO` set.
- Known flakes: AutoTitle, views/sites keyring, views/update timing,
  check-oss-first.

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
