# Dead-code / unwired audit — 2026-08-18

**Base tree.** Every `file:line` in this document resolves against the main
checkout at `/Users/alecfeeman/PycharmProjects/kameas-ai/kenaz-harness` on
branch `release/v0.59.0` at `9d9ebbce`, with `main` merged through
`55029354` ("feat: v0.64.0"). The `.claude/worktrees/` and `.worktrees/` trees
were excluded from every search — they are stale mirrors and produce phantom
findings.

**Scope.** Fifteen module agents swept the tree in parallel; an adversarial
refutation pass re-checked every claim and every quote against the file it
cites. **159 findings survived**; 1 was refuted and discarded. After collapsing
eight duplicate pairs (the same defect found independently by two modules —
the permissions revoke-reload, the narrative layer, the workflows audit
emitter, the hooks MCP invoker, the fleet Cedar engine, the lifecycle hook
seam, `LLM_UpdateProviderCredential`, and `EVENT_FAMILY`), that is **151
distinct defects**, of which **87 are user-visible**. Verifiers also surfaced
**52 findings of their own** that no module agent filed; those are listed
separately in §6 and are marked as **single-pass, unrefuted**.

Line-number corrections issued by the verification pass have been applied
throughout. Where a verifier overturned a finder's *reasoning* while leaving
the defect standing, both are recorded.

---

## 1. What state the tree is actually in

The 2026-08-14 and 2026-08-16 sweeps drained the *file-level* orphan question
and the *frontend module* question. This sweep went after the two classes the
repo's 34 CI gates structurally cannot see, and found that they are where the
damage lives.

**Class A — nil optional dependency.** A struct is constructed in production
with an optional pointer/interface field left unset, silently disabling the
feature. Everything compiles, every test passes (the tests set the field
themselves), and the gate inventory sees nothing because every symbol involved
*is* registered and *is* consumed — just not on the production path. **48
findings.** This is the single largest class in the sweep and it is
concentrated in one file: `core/rpc/api.go`, where ~20 view constructors take a
`Config` literal and omit one field each.

**Class B — per-variant gap.** A feature implemented for one provider,
platform, or transport and silently degrading to nothing on the others.
Registration-vs-consumption gates are blind by construction: every arm is
registered, every arm is consumed, one arm returns zero. **14 findings**,
including the sweep's most severe: an Azure OpenAI user, or any user of a local
Ollama / LM Studio / Jan / GPT4All runtime the harness auto-configured, gets
`capability unsupported by provider: tool_calling` on **every chat turn that
carries a tool catalog**, which is the default. That was proven empirically by
running the real catalog and gate, not inferred.

**Class F — manufactured success.** Code that records or reports success for
work it did not do. **12 findings**, and they are the ones to fix first
regardless of severity label, because they are not inert toggles — they are
fabricated evidence. The worst three: the fleet config poller ACKs
`applied:true` for a signed Cedar policy bundle it discarded; the scheduled-chat
"Run now" button writes a permanent `status="completed"` row into user-visible
run history for a dispatch that never happened; and the graph `approval` node
writes an `EventApprovalResolved{approved:true}` trace row attesting to a human
decision no human made.

The rest — classes C (registered-not-consumed, 41), D (inert dial, 20), and E
(frontend placeholder, 16) — is residue of the familiar shape, with one
recurring twist worth naming: **the docstring is usually the lie, not the
code.** Roughly forty findings here are a comment, a manifest description, a
tooltip, or a mission doc asserting behaviour that no code performs. In several
cases (`docs/missions/auto-update.md`'s six-kind audit table, the
`agent-kernel-graph-01KQ6391` `corpus_overflow_dropped` acceptance criterion)
the *acceptance criteria of a shipped mission* describe behaviour that does not
exist.

### What a user would notice, in one paragraph

Configure an Azure or local-runtime provider and every tool call fails and every
image is refused. Configure a hook on any of 16 of the 18 events the Settings
picker offers and it saves, shows Enabled, and never runs — including a
`pre_tool_use` hook written to block a dangerous call. Revoke a permission and
it keeps being permitted until you restart. Turn on extended thinking outside
direct Anthropic and pay for thinking tokens you never see. Click "Sign in to
Notion" (or Linear, Figma, Dropbox, Slack — 36 shipped connectors) and get a
developer-facing error you cannot act on. Uninstall a remote MCP connector and
it keeps polling the endpoint with your credentials. Open the Audit view and
find no record of any workflow run, session export, OAuth sign-in, MCP install,
Cedar policy edit, slash command, or update — the log is structurally empty.
Rebind a keyboard shortcut and only the cheat sheet changes. Click "Test
embedder" and be told "Connection OK" after a call that never touched the
embedder. Everywhere the app reports on itself, it is guessing.

---

## 2. Findings by class

Severity is P0 (a shipped capability is broken or a security control is
inert for everyone who uses it) → P3 (internal only). Dispositions quote
CLAUDE.md's "Disposition: delete vs. finish" rubric. **★** marks class A;
**◆** marks class B — the two classes no existing gate can see.

---

### Class A — nil optional dependency (48)

The method: for each exported `Config` / `Options` / `Deps` struct with
pointer or interface fields, find every production literal and diff the set
fields against the declared fields. Every finding below is a field that is
declared, documented, read by a live consumer, and never assigned outside
tests.

#### ★A1 — `fleet.SetCedarEngine` has zero callers; org Cedar bundles are discarded and ACKed as applied — **P0**
`core/rpc/views/settings/fleet.go:145` — `func (a *API) SetCedarEngine(engine *cedarpolicy.Engine) {`

Repo-wide search returns only the declaration, the field comment (`fleet.go:34`)
and the assignment (`fleet.go:151`) — no caller, not even a test. Its sibling
seam `SetSkillRefs` (`fleet.go:195`, same struct, same pattern) *is* wired at
`core/rpc/api.go:2107`, and `a.cedarEngine` is in scope in the same constructor.
The reader is `compositeConfigApplier.ApplyBundle`
(`core/rpc/views/settings/fleet.go:725-732`): `engine := a.state.cedarEngine`
then `if engine != nil { … fleet.ApplyCedarDelta … }` — a nil engine skips the
whole `cedar_delta` section and appends **nothing** to `errs`.
`core/fleet/config_pull.go:311-315` then reads `if len(applyErrs) == 0 {
p.lastAppliedID = b.BundleID; …; p.lastError = "" }`, and `ack.go:61` posts
`Applied: len(applyErrs) == 0`. Signature verification is live
(`config_pull.go:288` → `:293`), so a cryptographically verified bundle is
being discarded and reported as enforced. `CedarEditor.vue:145` renders a
"team-managed" badge for policies that can never be installed.

**User sentence.** An org admin pushes a Cedar policy bundle; every enrolled
device silently discards it, reports success back to fleet, and enforces none
of the team rules. Every other bundle section applies normally, so the failure
is invisible.

**Disposition. Wire** — trust-relevant *and* makes something else lie. One
`SetCedarEngine(a.cedarEngine)` call, **plus an error return when the engine is
nil** so a missing engine can never ACK clean again.
*(Found twice: `fleet-cedar-engine-never-wired`, `cedar-bundle-engine-never-set`.)*

---

#### ★A2 — `permissionsview.Config.Engine` is deliberately nil; revoking a grant deletes the file and never reloads the engine — **P1**
`core/rpc/api.go:2217` — `// Engine left nil for now — RevokeGrant skips the reload`

`RevokeGrant` `os.Remove`s the snippet under `<dataDir>/policy/`
(`core/rpc/views/permissions/impl.go:235`) then guards the reload on
`if a.engine != nil` (`impl.go:238-240`) — always false in production. The live
gates read a **cached** PolicySet: `Engine.Evaluate` does
`ps := e.policies.Load()` (`core/policy/cedar/engine.go:412`), an atomic pointer
only replaced by `Engine.Reload`. There is no policy-dir file watcher. The
asymmetry is what makes it airtight: the grant-*creation* path
(`core/policy/cedar/toolgrant.go:243`, `:279`) **does** reload; only revoke does
not. `a.cedarEngine` is non-nil and in scope at `api.go:2214` (used at `:2178`
and `:2237`), and `*cedar.Engine` already satisfies the seam
(`views/permissions/impl.go:23`).

**Five live `.vue` callers**, not three as originally filed:
`PermissionList.vue:100`, `CredentialPermissionsPanel.vue:25`,
`BashPermissionsPanel.vue:29`, `ToolPermissionsPanel.vue:25`,
`FilesystemPermissionsPanel.vue:25`. All re-fetch from disk afterwards, so the
row disappears from the UI while the in-process engine keeps permitting it.
Partial mitigation: `ClearTransientGrants()` still runs, so allow-once state is
cleared — only the persisted grant survives.

**User sentence.** You revoke a bash, filesystem, tool or credential grant. It
vanishes from the list and the call returns success. Every gate keeps
permitting the revoked action until you restart the app.

**Disposition. Wire** — trust-relevant (consent/permissions/denials) and the UI
reports a completed revocation that did not take effect. One line:
`Engine: a.cedarEngine` at the `api.go:2214` literal.
*(Found twice: `rpc-permissions-engine-nil`, `permissions-engine-nil`.)*

---

#### ★A3 — Three hook adapters are never constructed; 16 of 18 hook events cannot fire — **P1** (cluster of 5)

| Finding | Seam | Evidence |
|---|---|---|
| `lifecycle-hook-runner-never-installed` | `Env.LifecycleHooks` | `core/agentgraph/executor.go:272` `LifecycleHooks LifecycleHookRunner` — zero `.LifecycleHooks =` assignments outside `_test.go`. The only implementation, `core/hooks.LifecycleRunnerAdapter` (`lifecycle_runner.go:17`), is never constructed. `pre_tool_use` / `post_tool_use` / `post_tool_use_failure` cannot fire. |
| `permission-hook-seam-never-injected` | `cedar.WithPermissionHookRunner` | `core/hooks/permission_runner.go:18` `var _ cedar.PermissionHookRunner = (*PermissionRunnerAdapter)(nil)` — zero constructions; `WithPermissionHookRunner(` has exactly one non-test hit, its own declaration at `core/policy/cedar/permission_hooks.go:31`. The production registry (`core/rpc/api.go:1296`) passes no such option. `permission_request` / `permission_denied` cannot fire. |
| `session-hook-seam-never-injected` | `session.WithSessionHookRunner` | `core/hooks/session_runner.go:16` `var _ session.SessionHookRunner = (*SessionRunnerAdapter)(nil)` — zero constructions; `WithSessionHookRunner(` has one non-test hit, its declaration at `core/session/manager.go:151`. `session_start` / `setup` / `cwd_changed` cannot fire. |
| `allevents-events-with-no-fire-site` | `AllEvents` | `core/hooks/hooks.go:58` `EventUserPromptSubmit = "user_prompt_submit"`. Six events resolve to exactly two hits each — their declaration and their `AllEvents` entry — with no `Fire`/`FireAsync` call anywhere: `pre_save_session`, `post_assistant_turn_complete`, `user_prompt_submit`, `subagent_start`, `notification`, `worktree_create`. A seventh, `background_task_complete`, is **already in the ledger** (2026-08-14, "The background-task subsystem has no producer"). `file_changed` has a fire site only inside `core/fswatch`, itself on `scripts/ci/allowlists/i7-orphan-packages.txt:68`. |
| `hooks-mcp-invoker-never-implemented` | `hooks.Config.MCP` | `core/hooks/runner.go:231` `MCP           MCPInvoker // optional — nil means kind=mcp hooks are skipped with a warning`. No type in the repo implements the interface; the sole production literal (`core/rpc/api.go:6382`) omits it. The dispatchers do not warn-and-skip — they error at `runner.go:377`, `:403`, `:488`, `:518`. |

The UI advertises all of it: `frontend/src/lib/hooks.ts:60` `ALL_HOOK_EVENTS`
feeds the `<option>` list at `HookEditor.vue:165`, `SYNTHETIC_PAYLOADS` ships a
sample payload for each, `HOOK_KINDS` (`hooks.ts:113`) offers `mcp` with a
required tool-name input at `HookEditor.vue:219-227`, and
`core/rpc/views/hooks/dry_run.go` gives the operator a **working shell-hook
preview** for events that can never fire in production.

**User sentence.** Only `pre_send` and `post_send` actually fire. A hook on any
of the other 16 events saves, shows Enabled, previews correctly in the dry-run
drawer, and never runs — including a `pre_tool_use` hook written to block a
dangerous tool call, and a `permission_request` hook written to deny.

**Disposition. Wire** the three adapters (trust-relevant: a `pre_tool_use`
block and a `permission_request` deny are permission controls that silently do
not apply). **Escalate** the `mcp` kind — the runner's own text calls it a "v1
stub" whose result is discarded even when wired (`runner.go:489-492`), so
wiring an invoker would not make it useful; either build the seam or drop `mcp`
from `HOOK_KINDS` and the `hooks.go` validator. **Wire or drop** the six
producerless events; a picker entry with no fire site is the definitional case
the sweep exists to end.

---

#### ★A4 — The audit log has no emitter at ten production sites — **P1** (cluster)

Every entry below is `audit.Emitter` (or a bridge over it) left unset at a
`core/rpc/api.go` constructor literal, with `a.auditImpl` already constructed
at `api.go:1333`, well above every one of them, and four sibling views already
receiving a bridge (`acpview`'s `Audit: &acpAuditBridge{impl: a.auditImpl}` at
`api.go:2234` is the template).

| Finding | Literal | Dead kinds |
|---|---|---|
| `rpc-tools-audit-nil` | `api.go:3809` `Audit:          nil, // TODO(audit-wired): reuse process-wide event.Emitter` | 7 emits: `mcp.recipe.installed` / `.uninstalled` / `.key_forgotten`, `mcp.oauth.device_signed_in` / `.persist_failed` / `.broker_fallback_failed` / `.signed_in` |
| `rpc-branches-audit-nil` | `api.go:1919` `a.branchesAPI = branchesview.New(branchesview.Config{` | `KindBranchCreated`, `KindBranchAdvisorAccepted`, `KindBranchReintegrated`, `KindBranchAdvisorDismissed` |
| `rpc-workflows-audit-nil` / `workflowsview-audit-nil` | `api.go:2053` `a.workflowsAPI = workflowsview.New(workflowsview.Config{` | `workflow.executed` / `.saved` / `.deleted` / `.step_failed` |
| `slashcmd-audit-emitter-never-injected` | `api.go:1549` `coreslashcmd.NewDispatch(slashStore, nil)` with no `.WithAuditEmitter` | `KindSlashCommandRun` |
| `audit-export-backend-never-wired` | `api.go:1333` `audit.NewAPI(audit.WithSubscriber(…), audit.WithGate(…))` — no `WithBackend` | **Audit export (CSV/JSONL/PDF) has never worked in a shipped build** |
| `update-audit-emitter-nil` | `api.go:2119` `svc, err := coreupdate.NewService(coreupdate.Config{` | all six `update.*` kinds |
| `workflow-runner-audit-no-producer` | `api.go:2002` `wfDeps := corewf.Deps{}` — `Audit` never assigned | `notify.sent`; separately `KindWorkflowNetworkFetch` has **zero emit sites anywhere** |
| *(verifier extra)* | `api.go:2189` `cedarpolicyview.NewAPIWithOptions(cedarEng, cedarDataDir, nil, policyEditorEnabled)` | `KindPolicyFileSaved`, `KindPolicyFileDeleted`, `KindPolicyTemplateInstalled` |
| *(verifier extra)* | `api.go:1812` `sessions.WithExportOpts(a.sessionsAPI, a.cedarGate(), nil, nil)` | `KindSessionExport` |
| `migrations-registry-nil-emit` | `core/storage/sqlite/sqlite.go:103` `registry := migrations.NewRegistry(exec, nil, nil)` | `migration_applied` / `_failed` / `_rolled_back` |

The docstrings are the lie in every case. `core/context/audit/audit.go:84-102`:
"These cover the four lifecycle events the workflows RPC + engine emit."
`audit.go:134`: "KindBranchCreated fires when a branch is created."
`audit.go:205`: "KindSessionExport fires on every successful Sessions_Export
call." `docs/missions/auto-update.md:129-133` publishes a table of six kinds
with their triggers and Acceptance Criterion #1 states "every successful Service
call emits exactly one event of the corresponding kind" — and
`core/update/integration_test.go` asserts it **with a wired emitter**, so CI is
green while production emits nothing.

**Highest-severity site is the Cedar policy one.** Editing, deleting or
installing a Cedar policy — the mechanism that governs every permission
decision — leaves no audit record at all, while the audit panel is the app's own
evidence surface.

**User sentence.** The audit log contains no record of any workflow run,
session export, OAuth sign-in, MCP recipe install, credential forget, Cedar
policy edit, slash command, branch operation, or software update. Clicking
Export in the Audit view returns `audit: Export requires a backend`.

**Disposition. Wire** — trust-relevant (audit), and the absence makes ten
docstrings and one shipped mission's acceptance criteria lie. One field per
literal; `export.go` + `export_csv.go` + `export_jsonl.go` + `export_pdf.go` are
complete and tested behind the one missing `WithBackend`.

---

#### ★A5 — The narrative memory layer is never constructed; "Mark important" returns success and does nothing — **P1**
`core/rpc/api.go:1868` — `a.memoryAPI = memoryview.New(memoryview.Config{`

The only production literal sets `Store`, `Embedder`, `Reader`, `Profiles` and
omits `Journal`, `NarrativeMetrics`, `NarrativeJobs`. There is no setter escape
hatch. Stronger than "unset at this site": `narrative.MetricsStore` and
`narrative.JobQueue` have **no production implementation anywhere** — the only
constructors are `NewMemMetricsStore` (`core/memory/narrative/score.go:72`) and
`NewMemJobQueue` (`jobqueue.go:74`), neither with a non-test caller. The same is
true of `NewPromoter` (`promoter.go:99`), `NewSyntheticBuilder`
(`synthetic.go:56`), `NewCitationDetector` (`citation.go:91`), and
`LoadPrelude` / `FormatPrelude` (`prelude.go:38`, `:94`) — the latter two have
no caller *including tests*.

Consequences at `core/rpc/views/memory/impl.go`: `:651` `if a.narrativeMetrics
== nil {` → bare `return nil` (MarkImportant reports **success**); `:660`
NarrativeFailedCount always 0; `:669` NarrativeFailedList always empty; `:703`
NarrativeMetricsForChunk always zero. `RetryFailedNarrative` (`impl.go:694`) is
the one method that fails loudly — which is what makes the silent-nil siblings
the anomaly, not the house style.

Fully wired to the UI: `MemoryView.vue:1006-1014` renders an "Important" button
titled "Boost this memory's promotion score" → `:360` `toggleMarkImportant` →
`:369` `await client.memory.markImportant(chunk.id, true)` →
`harnessClient.ts:3733` → `Memory_MarkImportant` (`bindings.go:1772`). The catch
block only surfaces thrown errors. `MemoryView.vue:30` documents the same false
behaviour in a comment. The "N narratives unrecoverable" banner
(`MemoryView.vue:834`) can never appear.

**Cross-reference:** `docs/unwired-ledger.md` §"2026-08-18 · Migrations that can
never run" records that `narrative.RegisterMigrations` has no caller, so
migrations 821/822 never run and the tables do not exist on any install. That is
the same subsystem's dormancy from the storage end, and it explains **why** no
SQL-backed MetricsStore exists. Treat this as one question, not two.

**Disposition. Escalate** — ~1,500 lines across 13 files plus four RPCs, a
binding surface and a rendered banner. Deleting removes the capability;
"wiring" means building a store. Do **not** resolve by wiring the in-memory
`MemMetricsStore`, which would lose every promotion on restart. **Regardless of
the ruling**, `MarkImportant` must stop returning success — return an explicit
error the way `RetryFailedNarrative` already does, so the UI's existing catch
surfaces it.
*(Found three times: `rpc-memory-narrative-nil`, `memory-narrative-deps-nil`,
`narrative-layer-never-constructed`; plus `mark-important-silent-noop` and
`narrative-settings-gate-inert` as the same root cause.)*

---

#### ★A6 — Remaining class-A findings

| ID | Site | Consequence | Sev | Disp |
|---|---|---|---|---|
| `cost-reducer-never-wired` | `core/rpc/api.go:4041` — `registry.Options.Cost` unset at all three production sites | `core/llm/registry/audited_stream.go:77` `resp.Cost = llm.Cost{Indeterminate: true}` for every adapter that does not populate Cost itself — i.e. everything except OpenRouter. Cost is $0/unknown for Anthropic, OpenAI, Bedrock, Gemini, Azure and custom. **Already open as task #37.** | P1 | wire |
| `worklist-dir-never-set` | `core/rpc/builtins_wiring.go:531` `opts := corefsbuiltins.Options{` — `WorklistDir` omitted | `kenaz__list_open_worklist` always returns `loop_refusal: "The worklist is empty. Stop and ask the user…"` and the model stops working. The refusal is manufactured: it reports an empty worklist when no directory was ever configured. No producer writes worklist files anywhere. | P1 | escalate |
| `wfdeps-artifacts-nil` | `core/rpc/api.go:2002` — `Deps.Artifacts` **and** `Deps.SessionID` both unset | `read_artifact` / `write_artifact` always fail. The shipped **Doc Generator** builtin burns a full LLM turn then fails on its final `write_artifact` step. Two fields, not one — wiring `Artifacts` alone still fails with `no SessionID configured on engine deps`. | P1 | wire |
| `wfdeps-netauthz-nil` | same literal — `Deps.NetAuthz` unset | The `workflow.network.fetch` Cedar gate is skipped on every `web_fetch`/`web_scrape`. `cedarStrictWorkflowMode` is reachable and cannot deny anything. | P1 | wire |
| `audit-retention-backend-nil` | `core/rpc/api.go:2788` `sweeper := corefleet.NewAuditRetentionSweeper(corefleet.AuditRetentionConfig{` | `SweepOnce` returns `(0, nil)` at `core/fleet/audit_retention.go:135-137`. The hourly ticker runs a permanent no-op; `CompliancePanel.vue:180-208` saves and confirms a retention window that deletes nothing. No production `AuditRetentionBackend` exists. | P1 | wire |
| `catalog-withpubkey-never-called` | `core/rpc/views/catalog/impl.go:45` `func (a *API) WithPubKey(pubKeyBase64 string) *API {` — no caller; the three construction sites stop at `.WithEmitter(flAudit)` | `verifyCatalogSignature` opens `if pubKeyBase64 == "" { return nil }`. Fleet **catalog and skill** installs both skip ed25519 verification — `core/rpc/api.go:2653` hardcodes `PubKeyBase64: "", // fleet-level pub key; empty = skip verify (same as catalog)` into `slashview.SkillDeps`. | P1 | **justify/escalate** — the field's own comment names the blocker ("Empty until the server-side per-device key lookup is implemented") and **there is no pubkey source anywhere in the repo to wire from**. Needs an owner + date, not a one-liner. |
| `planmode-subscribe-missing` | `frontend/src/lib/planmode.ts:118` `if (client && typeof (client as any).subscribeEvent === 'function') {` | No client declares `subscribeEvent`. The guard is always false, `setPendingPlan` has zero production callers, `pendingPlanId` is permanently null, so `PlanApprovalModal` (`SessionsView.vue:2030`) and the awaiting-approval arm of `PlanModeBadge` (`PlanModeBadge.vue:43`, also via `SessionHeader.vue:209`) can never render. | P1 | wire |
| `tool-schemas-never-populated` | `core/agentgraph/executor.go:376` `ToolSchemas map[string][]byte // toolName → JSON Schema bytes` | Its own doc claims "Populated by the LLMProviderAdapter at StartStream time." Nothing populates it, so `validateToolArgs` short-circuits (`tool_validation.go:45`) and only the "parses as object" check survives — no required-property and no type check. FR-006 schema validation is inert in production. | P2 | wire |
| `attachment-registry-never-wired` | `core/agentgraph/executor.go:204` `AttachmentRegistry AttachmentRegistrar` | `read_file`'s `as_attachment: true` silently returns inline content. `attachment_ref` is not even a declared output port on `read_file.yaml`. | P2 | wire |
| `envdeps-history-seams-never-set` | `core/rpc/views/agentgraph/env_deps.go:46` `HistoryWriter coreag.HistoryWriter` | `History` and `HistoryWriter` are never assigned at the one production `EnvDeps` literal (`api.go:6084`). Confined to the Graphs-view Run path — which also has no LLM or Tools seam at all, so it is already degenerate. | P2 | **escalate** — either the Run button executes real graphs (wire all four) or it is topology-debug only (delete both fields). Do not assign two of four. |
| `bash-nil-logger` | `core/rpc/builtins_wiring.go:121` `bashTool := corebash.New(corebash.Options{` | All 18 bash log sites silent, including every Cedar gate decision. `bash.gate.snippet_write_err` is swallowed entirely — an "Allow always" that fails to persist leaves no trace. Every sibling tool defaults a nil Logger to `slog.Default`; bash does not. | P2 | wire |
| `catalog-reciperegistry-nil` | `core/rpc/api.go:1991` `wfcatalogpkg.New(wfcatalogpkg.Config{Store: wfStore, Scheduler: sched})` | `core/workflows/catalog/concrete.go:176` `if c.cfg.RecipeRegistry == nil \|\| !c.cfg.RecipeRegistry.Has(st.Server) {` — every `mcp_call` server is reported as a missing credential, so the catalog preview always flags MCP workflows as unconfigured and installing one always bounces the user to Providers. **Correction:** `*recipes.MergedCatalog` does **not** satisfy `RecipeRegistry` (no `Has` method) — an adapter must be written, and `concrete.go:14`'s "Satisfied by *recipes.MergedCatalog" comment is false. | P2 | wire |
| `installed-mcp-sync-nil-halves` | `core/rpc/api.go:2449` `mcpSyncCat := corefleet.NewMCPSyncCategory(nil, nil, nil, syncPending)` | Reader, Writer **and** SecretKeys all nil. No type in the repo implements either interface. Latent hazard: with `SecretKeys` nil, the documented HARD-RULE redaction of secret env values would not run the moment a Reader is supplied. | P2 | wire |
| `fs-gate-policydir-never-set` | `core/rpc/builtins_wiring.go:502` `gate := corefs.NewGate(corefs.GateOptions{` | `PolicyDir` unset → `writePolicySnippet` returns nil at `core/tools/fs/gate.go:336`, and `Evaluate` discards the return with `_ =` on both prompt-allow branches (`:223`, `:237`). Latent only because the same literal also omits `Prompter` (task #35) — a Prompter-only fix ships the regression. | P3 | wire **in the same PR as task #35** |
| `wfdeps-tools-nil` | `core/rpc/api.go:2002` — `Deps.Tools` unset | The canvas editor offers a "Tool call" node (`workflowAdapter.ts:140`); every such step fails with `no ToolCaller wired`. `stack.wrappedPool` is already adapted onto `ToolDispatcher` two lines away. | P3 | wire |
| `cred-invalidator-never-set` | `core/rpc/views/llm/impl.go:476` `CredInvalidator CredentialInvalidator` | **Downgraded by verification to P3, internal-only.** The field's own doc (`impl.go:472-475`) documents the nil case as benign ("the keychain write is canonical"). The lie is the *interface* doc at `:294-300` and the step list at `:1470`, both of which claim an adapter over `secrets.Resolver.Invalidate` that cannot exist. | P3 | fix the two docs |
| `bootstrap-consent-checker-nil` / `contextbootstrap-consent-inert` | `core/rpc/contextbootstrap_wiring.go:590` `eng := contextbootstrap.New(contextbootstrap.Config{` | `Consent` omitted → `New` substitutes `&alwaysConsentChecker{}` (`seams.go:123` `return true, nil`). Dormant second layer only: the caller-supplied `ConsentedSources` list is the effective gate. | P3 | **escalate, not justify** — the blocker (fleet WP04 consent store) is named at `seams.go:91`/`:117` but there is **no owner and no date**, which per CLAUDE.md is not a reason. |

---

### Class B — per-variant gap (14)

The method: for each feature with a provider/platform/mode switch, enumerate
the arms and find the arms that no-op, return zero, or fall through.

#### ◆B1 — No capability-catalog entry for `azure-openai` or `custom-openai`; the gate refuses tool-calling and every image on both — **P0**
`core/llm/capabilities/gate.go:26` — `desc := g.cat.Describe(prof.Kind, prof.Model)`

`capabilities.Load` keys the spec map on each YAML's `provider:` value
(`loader.go:181`). `core/llm/capabilities/data/` contains exactly six files —
anthropic, bedrock, gemini, ollama, openai, openrouter. `azure.Kind =
"azure-openai"` (`azure/adapter.go:26`) and `custom.Kind = "custom-openai"`
(`custom/adapter.go:32`) match none of them, so `Describe` takes the `if !ok`
branch (`loader.go:198-204`) returning streaming + usage_reporting only, and
`AttachmentLimits` (`loader.go:512`) returns `ImageInput=false`.
`Registry.Stream` calls `gate.Check` (`registry.go:384`) and
`gate.CheckAttachments` (`:394`) using `prof.Kind`, written verbatim from the
frontend picker (`AddProviderForm.vue:76,78`). **Local runtimes — Ollama, LM
Studio, Jan, GPT4All — are auto-configured as `Kind: "custom-openai"`**
(`core/rpc/views/llm/impl_local_runtime.go:88`). The chat path always attaches
the discovered catalog (`chat/llm_provider_adapter.go:535`).

**Proven empirically**, not inferred: a scratch module outside the repo
importing the real catalog and gate returned `capability unsupported …:
tool_calling` and `attachment MIME type "image/png" not supported by
azure-openai` for `azure-openai/gpt-4o` and `custom-openai/llama3.1`, and `nil`
for `openai/gpt-4o` and `anthropic/claude-sonnet-4-5`.

**User sentence.** An Azure OpenAI provider — or any local runtime the harness
auto-configured for you — fails **every** chat turn that carries a tool catalog
(the default) and refuses **every** image attachment. Both adapters contain
complete tool-calling and `image_url` encoders that are never reached.

**Disposition. Wire** — deleting the gap removes Azure and every local runtime
from the product. Add `azure-openai.yaml` (mirroring openai) and a
`custom-openai` baseline, or make the gate resolve a kind alias the way
`azure/adapter.go:116` already does.

---

#### ◆B2 — Bedrock silently drops every `tool_result` and `tool_use` block on both paths — **P1**
`core/llm/bedrock/bedrock.go:713` — `// Tool results aren't yet wired through the harness — skip`

`toBedrockMessages` has `case llm.RoleTool:` → `continue` (`:715`), and
`toBedrockContentBlocks` (`:735`) switches only on `""`/`text`/`image`/
`document` — `tool_use` and `tool_result` fall through and vanish. The
bearer/REST path repeats it (`bearer.go:379`, `:393`). Yet the adapter **parses
tool calls off the wire** (`toolUseAccum`, `bedrock.go:1004`; `llm.ToolUse{…}`,
`:1065`) and `bedrock.yaml` declares `tool_calling: true`, so the gate lets
tools through. The comment's asserted invariant is false: `KernelMessagesToWire`
(`chat/llm_provider_adapter.go:479`, `:489`) emits both block types on the
production chat path.

**User sentence.** On Bedrock the model emits a tool call, the harness executes
the tool, and the result is discarded before the next request. The model sees a
dangling `tool_use` with no answer and either re-calls the tool forever or
hallucinates a result. Multi-turn tool use is broken for every Bedrock user.

**Disposition. Wire.**

---

#### ◆B3 — Azure and custom-openai maintain private encoders that never emit `tool_calls` or `tool_call_id` — **P1**
`core/llm/custom/adapter.go:301` — `"content": flattenContent(m.Content),`

`flattenContent` (`custom/adapter.go:331-342`) concatenates only `p.Text`; Azure
does the same via `buildContent` (`azure/adapter.go:426`, declared `:538`).
Neither package imports `core/llm/openaiwire` — whose `body.go:110-140` is
exactly the code that emits `tool_call_id` (`:115`) and the assistant
`tool_calls` array (`:138`), under a comment naming the failure it prevents:
"Omitting tool_call_id makes OpenAI-compatible providers reject the turn."
Both adapters nonetheless run full tool-call accumulators off the stream.

**Currently masked by B1** — the gate refuses tool-bearing requests before the
encoder runs. **Fixing B1 alone exposes this one**, so they must land together.

**Disposition. Wire** — route azure and custom through `openaiwire.BuildBody`
rather than maintaining a third and fourth partial copy.

---

#### ◆B4 — "Sign in to X" renders for 36 shipped OAuth recipes whose only sign-in path hard-rejects them — **P1**
`frontend/src/views/tools/RecipeKeyPromptModal.vue:904` — `v-if="oauthAuth"`

`oauthAuth` (`:204`) is `props.recipe.auth?.kind === 'mcp_oauth'` — it does not
look at `client_id` or `primary_auth`. Its button (`:923`) calls
`client.tools.recipes.signIn` → `Tools_SignInRecipe` (`bindings.go:1988`) →
`core/rpc/views/tools/oauth.go:149`, which short-circuits at `oauth.go:163`
`if recipe.Auth.ClientID == ""` with `tools: recipe %q has no OAuth client_id
configured`. Parsing `core/mcp/recipes/registry.json` counts **36** `mcp_oauth`
recipes with an empty client_id: slack, notion, linear, sentry, cloudflare,
asana, zapier, make, pipedream, supabase, gitlab, intercom, mercury, ramp, deel,
grafana, datadog, new-relic, circleci, netlify, google-docs, google-sheets,
clickup, monday, shortcut, mixpanel, amplitude, klaviyo, dbt-cloud, airtable,
canva, figma, miro, dropbox, paypal, square. The DCR implementation exists
(`core/mcp/oauth/register.go`, `resolve.go`) including a drop-in
`SignInWithDCR` at `resolve.go:160` — with **zero production callers**.

**Scope correction:** `primary_auth` breaks down as `browser_oauth_dcr`=30,
`oauth`=4, `browser_oauth_pkce`=2. Swapping in `SignInWithDCR` fixes 30; the
other 6 need a second arm or a `primary_auth` guard on the section.

**Disposition. Wire** — the only sign-in entry the UI has, and the recipes' own
`primary_auth: browser_oauth_dcr` declares a capability the backend fully
implements but never reaches.

---

#### ◆B5 — `RecipeAuth.ClientID` is never `${VAR}`-substituted; 14 recipes send a literal placeholder as their OAuth client_id — **P1**
`core/mcp/recipes/recipes.go:266` — `ClientID string \`json:"client_id,omitempty" yaml:"client_id,omitempty"\``

Every `Substitute*` call site covers URL, PostURL, headers, Command and
ArgsTemplate — none touches `Auth`. Both consumers read it raw
(`views/tools/oauth.go:169`, `device_auth.go:90`) and the only guard is `== ""`,
which a `${…}` literal passes. **14 recipes** (not 11) carry a placeholder
client_id across 13 distinct env keys: atlassian, ringcentral, webex, front,
discord, zoom, vercel, bitbucket, smartsheet, wrike, tableau, **hubspot, box,
salesforce**. `registry_test.go:659`/`:672` pin **both** the placeholder **and**
a required EnvKey of the same name "so install modal can surface it" — i.e. the
design is user-supplies-env-key → substitute, and only the substitution half is
missing.

**User sentence.** You paste your OAuth client id into the recipe's env key as
the modal instructs, click Sign in, and the browser opens the provider's
authorize page with `client_id=${KAMEAS_..._OAUTH_CLIENT_ID}` — an
unrecoverable provider error with no hint what went wrong.

**Disposition. Wire.**

---

#### ◆B6 — Uninstalling an http/sse MCP connector tears nothing down — **P1**
`core/mcp/dispatch/pool.go:305` — `// http and sse pools do not yet expose a per-server CloseOne method.`

Only `stdio/pool.go:317` has `CloseOne`. The dispatch default arm deletes the
ownership map entry and returns nil. Consequences: (a) the http pool's
`serverEntry` stays in `p.servers` and `entry.probe.Stop()` is only reached on
the replace path and pool-wide Close, so the HealthProbe keeps issuing
`tools/list` **with the recipe's Authorization bearer, forever**; (b)
`dispatch.Pool.Tools` still includes the pool, and that is the discoverer source
at `core/rpc/api.go:4213`, so the model still sees the tools; (c) `Call` returns
`ErrServerNotFound`, which `UninstallRecipe`
(`core/rpc/views/tools/impl.go:639`) swallows, returning nil.

**Scope:** 60 of 115 registry recipes are transport `http` — the majority of the
remote catalog. This is an unmet FR of a shipped mission:
`kitty-specs/_archive/remote-mcp-transport-wiring-01NMCPX02/spec.md:53` (FR-001)
required per-server CloseOne dispatch for http/sse.

**Disposition. Wire** — reports success for work it did not do, and a
credentialed probe survives the user's removal.

---

#### ◆B7 — The cloud-sync WAL-corruption guard matches POSIX separators only — **P1**
`core/storage/db/mount.go:133` — `"/OneDrive/",`

`CheckMount` overlays `matchCloudSyncRoot` whenever the OS classification is
`MountKindLocal` — which is what `mount_windows.go` returns for any non-remote
drive, i.e. every local disk holding a Dropbox/OneDrive folder. Every
`CloudSyncDenyList` entry uses forward slashes (`/Dropbox/` `:101`,
`/Google Drive/` `:117`, `/OneDrive/` `:133`, `/Insync/` `:147`). On Windows
`filepath.Abs` returns backslash paths, so none can fire. **The tell:**
`mount_windows.go`'s own `init()` adds an iCloud matcher using **backslash**
literals — the author fixed separators for the one matcher they added and left
the four cross-platform ones POSIX-only. The only test feeds
`matchCloudSyncRoot` hand-written POSIX strings, so it passes identically on
Windows and covers nothing.

**Disposition. Wire** — data-integrity guard; OneDrive is the default documents
location on many Windows installs, so this is the platform where it matters
most.

---

#### ◆B8 — Remaining class-B findings

| ID | Arms | Consequence | Sev | Disp |
|---|---|---|---|---|
| `reasoning-output-anthropic-only` | 4 providers advertise `reasoning: true` | `llm.StreamReasoning` has exactly one non-test emitter: `anthropic/anthropic.go:1061`. Bedrock sends `reasoning_config` and never parses `ReasoningContent` back; Gemini sends `thinkingConfig` without `includeThoughts`; Azure/openaiwire send `reasoning_effort` and parse nothing. **Worse than filed:** `openaiwire/body.go:76-77` sets `reasoning_effort` only from `k.Reasoning.OpenAIEffort` (RequestKnobs), never from `req.Reasoning` — so for OpenAI/OpenRouter/custom the ReasoningControl budget is dropped on the **request** side too. `llm.Response.Reasoning` (`llm.go:691`) has zero writers and zero readers. | P2 | wire; delete `Response.Reasoning` |
| `lockdown-reason-dropped` | boot-seed vs live-event | `core/rpc/views/settings/fleet.go:799` `return LockdownStatusView{Active: active}, nil` — `Reason` never set, because `core/fleet/lockdown.go:18` stores state as a bare `atomic.Bool` with nowhere to keep a reason. `LockdownBanner.vue:37-45` seeds `reason` on mount precisely for the boot-into-locked case. | P2 | wire |
| `wirecheck-excludes-three-shipping-adapters` | 4 of 7 adapters in scope | `core/llm/wirecheck/registry_completeness_test.go:21` `var inScopeAdapters = []string{"anthropic", "openai", "openrouter", "bedrock"}` — azure, gemini and custom-openai are structurally exempt from every field-coverage assertion. **This is the gate blind spot that let B1 and B3 ship.** | P2 | wire + planted-violation proof |
| `workflow-input-kind-variant-gap` | 6 declared input kinds | `WorkflowsView.vue:659` branches only on `'multiline'`; the `v-else` is a plain text box. `schema.go:88` **requires** options for `enum`, and nothing reads `inp.options`. No file/artifact/project picker exists. | P3 | escalate |
| `llm-testproviderkey-azure-only` | 1 of 7 kinds | `core/llm/views/llm/impl.go:1638` `// Other provider kinds are stubs — parallel agents will fill them in.` No `.vue` calls it. **Correction:** `TestAndRotateKey` is *not* the substitute (`api.go:261-263` explicitly contrasts them); the live substitute for the pre-submit probe is `AddProviderForm.vue:344` `client.llm.listModels(form.kind, form.apiKey)`. | P3 | delete (re-grounded) |

---

### Class C — registered but not consumed (41)

Both directions checked: builtin tools ↔ `builtinEnabledPredicate`, event kinds
↔ emit sites ↔ readers, broker topics ↔ subscribers, Wails bindings ↔
`harnessClient.ts` ↔ an actual `.vue` caller, RPC methods ↔ served-mode dispatch.

#### C1 — Scheduled chat runs never fire, and "Run now" writes a fabricated completed row — **P0** (2 findings)
`core/rpc/views/scheduledchat/impl.go:36` — `// no-op summary (noop dispatcher). Cron-triggered dispatch is wired`
`core/scheduler/chat_dispatcher.go:89` — `Status:        "completed",`

`DispatchChatRun` has exactly one call site (`impl.go:217`) and **no non-noop
implementation of `scheduler.ChatRunDispatcher` exists anywhere in the tree**.
`Job.EffectiveKind()` and `scheduler.JobKindChatRun` have zero non-test readers,
so nothing dispatches on job kind and no cron engine could route a `chat_run`
job. `RenderPromptTemplate` and `ParseOutputSink` are test-only, so the
documented `{{date}}`/`{{time}}` interpolation and the banner/file:/none sinks
never execute. `core/rpc/api.go:2264` constructs
`scheduledchatview.Config{Store, Cedar}` with no `Dispatcher` — so the noop is
not a fallback, it is the only path. `impl.go:200-202` substitutes it, `:224`
assigns a history row id, `:229` persists via `Store.AppendHistory`, `:235`
returns a success summary to the UI. The panel is mounted
(`SettingsView.vue:1135`) with Pause/Resume rows and empty-state copy reading
"run a prompt on a cron schedule".

**User sentence.** You create a scheduled chat with a cron expression, model and
output sink. The row persists and shows Pause/Resume. It never fires, ever, with
no error. Clicking "Run now" returns a successful run summary and writes a
permanent `completed` row into run history for work that never happened.

**Disposition.** **Escalate** the cron half (unattended prompt execution is the
same product question as task #33; the mounted panel must not stay as-is under
either outcome). **Wire immediately, unconditionally:** `RunNow` must return a
typed "not wired" error, and `NoopChatRunDispatcher.Status` must not be
`"completed"` while it is reachable from production. That is fabricated
evidence in a persisted table.

---

#### C2 — `@secret:` references are never resolved while `kenaz__list_secrets` advertises them to the model — **P1**
`core/credstore/refs/context.go:29` — `func WithResolver(ctx context.Context, r *Resolver) context.Context {`

Four `_test.go` callers and one **comment** at `core/tools/bash/bash.go:381`
("via context by the chat runner (via refs.WithResolver)") describing a producer
that does not exist. `refs.ResolverFromContext(ctx)` therefore returns nil at
every production call site — `bash.go:384`, `webfetch.go:213`,
`stdio/server.go:615` — all of which guard with `if resolver != nil` and pass
the string through **unchanged**. The producer half is live (`/secret` →
`core/rpc/api.go:3026` → `ExposureIndex.Add`) and the tool half is live
(`builtins_wiring.go:251`). `refs.WithTurnSanitizer` has zero non-test callers
too, so output redaction is unreachable for the same reason.

**User sentence.** You expose a secret with `/secret`. `kenaz__list_secrets`
returns `@secret:<locator>` and its schema tells the model "use the returned
`@secret:` tokens in tool arguments (e.g. web_fetch headers, bash env vars)".
The model does exactly that, and the **literal string** `@secret:mytoken` is
sent — as an HTTP header to a third-party host, as an MCP tool argument, or into
your shell. The request fails to authenticate and the reference name is
disclosed to the remote end.

**Disposition. Wire** — the sole `@secret:` substitution path, and a registered
tool's schema promises the model a capability that always fails. One call:
wrap the tool-dispatch ctx with `refs.WithResolver` + `refs.WithTurnSanitizer`.
`cedar.EvaluateSecretReferenceResolve` (`hooks.go:783`) and
`policies/secret_reference.cedar` belong in the same wire — `resolver.go:129` is
their sole caller.

---

#### C3 — `filesystem-full-recommended.cedar` cannot match anything — **P1**
`core/policy/cedar/policies/filesystem-full-recommended.cedar:25` — `action in [Action::"file_read", Action::"file_write"],`

Production-reachable: `core/mcp/recipes/shipped.json:89` sets
`"recommended_policy_template": "filesystem-full-recommended.cedar"` for the
`filesystem-full` recipe, and `CedarPolicy_InstallTemplate` copies it into
`<DataDir>/policy/`. The install button is at
`RecipeKeyPromptModal.vue:367`/`:935`. **Three independent reasons every rule is
unmatchable:** (1) *action* — the only production callers of
`CheckFileRead`/`CheckFileWrite` are the agentgraph state executor
(`env_deps_policy.go:34`,`:39`); the filesystem tooling evaluates
`ActionReadFilesystem`/`ActionWriteFilesystem` instead
(`core/tools/fs/gate.go:167-169`). (2) *resource* — the rules say
`resource is FilesystemOp`, but those helpers build `Filesystem::"<path>"`
(`types.go:497`). (3) *context* — every `when` reads `context.canonical_path`,
which `populateFamilyContext` only ensures for the `read_filesystem` /
`write_filesystem` arms (`engine.go:626-628`), and those helpers pass nil
contextAttrs.

**User sentence.** You install the unrestricted `filesystem-full` MCP recipe,
accept the recommended hardening policy that states "it CANNOT touch your
secrets, credentials, or .git internals through this policy", and **every**
forbid in it — `~/.ssh`, `~/.aws`, `~/.gnupg`, `.netrc`, `.env*`, macOS
Keychains, `/etc` writes, `.git` writes, the harness `data.db` — is inert.

**Disposition. Wire** — retarget onto `read_filesystem`/`write_filesystem` +
`resource is FilesystemOp`, with a regression test that installs the template
and asserts a `~/.ssh` read is denied through `core/tools/fs`'s gate. Do not
delete: this is the only filesystem hardening surface the product ships.

---

#### C4 — `Action::"use_tool"` has no evaluator, so the embedded default tool policy and every install-time grant snippet are inert — **P2**
`core/policy/cedar/policies/default_tool_policy.cedar:38` — `    action == Action::"use_tool",`

The file is embedded into **every** engine (`core/policy/cedar/engine.go:57`,
`:257`). Repo-wide, `ActionUseTool`/`use_tool` produces **no Evaluate call** —
only attr filling (`engine.go:631`), family membership (`:739`), policy *text*
generation (`toolgrant.go:236`), a comment, and the policy files themselves.
Two production writers emit `use_tool` policy text nothing will ever read:
`core/rpc/views/tools/impl.go:452` writes a pre-seed grant snippet per MCP
recipe tool at install time, and `toolgrant.go:236` writes the confirm-each
"always allow" body (harmless — a file-existence check is the real lookup). The
shipped installable `sites-recommended.cedar` is built entirely on `use_tool`
and is a no-op when installed.

**User sentence.** You open the Cedar policy editor and write
`forbid(principal, action == Action::"use_tool", resource ==
Tool::"filesystem__write_file");`. The editor accepts it, ListPolicies reports
it loaded, and the tool still runs. There is no per-call tool authorization in
the harness at all.

**Partial ledger overlap:** `scripts/ci/allowlists/i10-unwired-gates.txt:220-231`
already records that `cedar.CheckTool` (which uses `ActionToolExec`) has no call
sites and rules DELETE. The **new** half is that the shipped policies and the
install-time snippet writer target `use_tool`, an action with no evaluator
either — so the ledger's "tool dispatch is NOT ungated" framing is incomplete.

**Disposition. Wire** — evaluate at the tool-dispatch boundary, or delete
`default_tool_policy.cedar` from the embedded bundle, delete
`sites-recommended.cedar`, and stop writing pre-seed snippets. Half-and-half is
the current state and it is the lie. Ride
`tool-family-actions-no-evaluator` (P3 — `tool.list_secrets`,
`tool.skill.invoke`, `tool.passive`, `tool.read.glob/grep`, `tool.tasks.*`,
`tool.subagent.dispatch`, all evaluator-less, three package headers asserting
"Cedar-gated") on the same commit.

---

#### C5 — `kenaz__exit_plan_mode` returns `awaiting_user_approval: true` and nothing pauses — **P1**
`core/tools/planmode/exit.go:206` — `AwaitingUserApproval: true,`

The result's doc (`exit.go:115`) says "The toolloop pauses the session on
receipt of this" and the returned Message says "The session is paused until the
user approves, edits, or discards the plan." `awaiting_user_approval` has six
hits repo-wide: the struct field, two comments, the literal, and two frontend
comments — **zero consumers**. Nothing dispatches on the exit tool's result.
`plan_mode_changed` is emitted only from
`core/rpc/views/planmode/approve.go:195`, i.e. *after* the user acts. Combined
with `planmode-subscribe-missing` (A6), `PlanApprovalModal` never mounts.

Additionally (verifier extra, single-pass): **entering** plan mode notifies
nothing either — there is no emitter seam on `EnterOptions`
(`core/tools/planmode/enter.go:71`) or `ExitOptions`, and the frontend's only
other source of plan-mode state is a one-shot mount poll
(`SessionHeader.vue:107`). So the badge stays hidden for the rest of the session
while write tools are silently forbidden.

**Disposition. Wire** — the tool, artifact capture, posture manager,
Approve/Discard/Edit RPCs, `PlanApprovalModal.vue` and `PlanModeBadge.vue` are
all built and tested; the missing link is a producer carrying `plan_id` from the
exit tool result to the frontend. Fix `planmode-approve-optional-chain`
(`PlanApprovalModal.vue:56` `await window.go?.rpc?.Bindings?.Planmode_Approve({`
— optional chaining makes the whole expression `undefined`, `await undefined`
resolves, and `emit('approved')` fires anyway) **in the same PR**, per the rule
that you wire the consumer before mounting the surface.

---

#### C6 — Remaining class-C findings

| ID | Symbol | Consequence | Sev | Disp |
|---|---|---|---|---|
| `sentry-lastfive-no-writer` | `core/sentry/cache.go:73` `func AppendToCache(dataDir string, entry CacheEntry) {` | Zero callers, including tests; `newCacheID()` never called, so no `CacheEntry` is ever constructed. The read half is fully live to `CrashReportingPanel.vue:221`. Settings → Crash Reporting permanently shows "Recent crash events (last 0)" and every generated local crash report contains `"last_five": []`. | P1 | wire |
| `sessions-deletewithoptions-no-caller` | `core/rpc/views/sessions/api.go:273` | Zero files under `frontend/src`. `Delete` funnels to `DeleteWithOptions(…, DeleteOptions{})` and `DeleteArtifactsCascade` is `!o.PreserveArtifacts` — the zero value deletes. `PreserveArtifacts` and `PromoteArtifactsToProject` have no writer in production. **Deleting a session always destroys its artifacts, with no warning and no choice.** | P1 | wire |
| `compaction-graph-seams-unwired` | `core/rpc/views/compaction/impl.go:36` `SetGraphLister` / `:41` `SetGraphResolver` | Only test callers. `ListCustomStrategies` is permanently `[]`. `CompactionStrategyPanel.vue:91` nonetheless offers `custom_subgraph` and lets the user **save** it; compaction then fails with `ErrCustomSubgraphMissing`. The panel's empty-state copy (`:667`) blames an agent-graph library mission that **already shipped** (`views/agentgraph/api.go:172`,`:175`). | P2 (arguably P1) | wire |
| `contexts-markapplied-no-caller` | `core/contexts/library.go:499` | Read half fully wired to `ContextsView.vue:191` and rendered at `:701-704`. The "Recent" pane is empty forever. Docstring confesses: "later WPs flip the call sites" — they never landed. | P2 | wire |
| `contexts-search-export-no-caller` | `core/rpc/views/contexts/impl.go:352` | Both delegate to a real syncer; both have bindings; neither appears anywhere in `frontend/src`. No way to search or export the shared context graph from the app. | P2 | wire (cheapest win) |
| `audit-chain-skip-no-caller` | `core/fleet/audit_archive.go:216` `SkipToID` | Its own doc calls it "the operator recovery action after a chain-break", and the loop comments "operator may have called SkipToID". Zero callers; `ComplianceAPI` binds only Status/ArchiveNow/SetRetention. After one hash-chain break, fleet audit archival stops **permanently**; the panel banner instructs the user to resolve it and offers no control that can. | P2 | wire |
| `prune-scheduler-never-started` | `core/memory/prune/scheduler.go:76` | `NewScheduler`/`Start`/`RunOnce` and all four options have zero production callers. The only prune path is the user pressing "prune now" in the Memory view. Memory grows without bound for everyone who never finds that button. | P2 | **escalate** — prune deletes user memories; whether it should run automatically without a consent surface is a product call. |
| `last-usage-json-write-only` | `core/session/store.go:1061` `GetLastUsage` | Written after every turn (`api.go:4975`), read by nothing. The frontend's `lastUsage` is fed only by the broker and reset on session switch. Reopening a session shows the context-window indicator and the "N tok · $M" footer at **zero** until the next turn — the persistence exists specifically to prevent that. | P2 | wire |
| `unit-resolveloadable-no-consumer` | `core/rpc/views/fleet/impl.go:154` | `core/units/resolution.go:199-201` states "returns the units that should be injected at conversation start… (WP18 wires this into the unit/context resolution path)". No conversation-start path calls it. Its four Phase-3 siblings **do** have frontend references. | P2 | wire |
| `site-env-vars-orphan` | `core/fleet/sites.go:316` `SiteEnvSet` / `SiteEnvList` | Zero callers. `core/sites/manifest.go:102-104` declares `Env map[string]EnvVarSpec` and points users at "PUT /sites/{id}/env", while `:168` **rejects inline `value` fields**. There is no affordance anywhere to supply those values; dynamic sites deploy unconfigured. `sites.go:315` even says "This is the ONLY way secrets reach a site." | P2 | wire (mission-sized) |
| `documentchip-pagecount-no-producer` | `DocumentChip.vue:18` `pageCount?: number;` | The backend really parses PDFs and persists `page_count` (`core/attachments/media.go:286`,`:302-306`), but `llm.MediaSource` (`llm.go:268-280`) has no page-count field, so the wire shape cannot carry it and `pageLabel` always computes `''`. | P2 | wire or delete the extraction |
| `event-kind-registry-builtins-no-emitter` | `core/event/kind/registry.go:35` `// These fire regardless of which resource family triggered the gate.` | **16 built-in kinds proven** to have zero emit sites in either constant or string form (permission.granted/prompted/revoked/bash/filesystem/credential/tool; mcp.recipe.added/removed/tested; llm.knob.unsupported/dropped; event-log.chain.rebased / database.opened / redaction.salt-rotated / .supersede). Root cause: `core.Subsystems` is **never constructed in production**, so `c.Events` is nil on every boot. *Correction: the registry is not uniformly dead —* `kind.KindHarnessSelfToolCalled` **is** emitted at `core/mcp/builtin/harness/audit.go:57`. | P2 | escalate |
| `context-audit-kinds-never-emitted` | `core/context/audit/audit.go:24` | All ten `context.*` kinds have zero Go emitters and zero raw-string emitters anywhere; only two test readers. `KindContextBootstrapRun` **is** emitted, so this is not a whole-file artefact. | P2 | escalate with the pack-parser question |
| `elicitation-hooks-never-run` | `core/hooks/runner.go:314` `RunElicitation` | Two declarations, zero callers; `RegisterElicitation`/`RegisterElicitationResult` also have zero call sites, so even the builtin maps are empty. `core/elicitation` contains no reference to `hooks.Runner`. Both events are in `AllEvents` (so hand-edited `hooks.json` passes validation) but deliberately absent from `ALL_HOOK_EVENTS`. | P2 | wire or remove from `AllEvents` |
| `conv-message-refs-write-only` | `core/conversation/manager.go:228` `ListMessageRefs` | Write side live (`env_deps_branch.go:84`); zero readers outside the package. **Wider than filed:** `Store.UpdateChildMsgID` (`store.go:40`, `:177`, `:437`) also has zero production callers and no Manager passthrough — rows are appended with `child_msg_id` never populated **and** never read. `migrations_branches.go:24-27` documents a copy-on-write read-through that no code performs. | P2 | escalate (both halves) |
| `eval-replay-half-unwired` | `core/eval/replay.go:112` | `eval.NewRecorder` **is** constructed at boot (`api.go:2300`, logging `eval.recorder.wired`), but `NewReplayer`/`Diff`/`RunMatrix`/`GateModelProfilePromotion`/`ReadCapture`/`FingerprintRequest` have zero call sites, and `Sessions_StartCapture`/`StopCapture` have no frontend caller and no alternate entry point. Captures can never be started from the app. | P2 | escalate (dev tool vs. product) |
| `rpc-mcp-health-subscribe-dead` | `core/rpc/bindings.go:545` | `mcp:health-changed` has **neither publisher nor subscriber**: `PublishHealthChange` (`views/mcp/impl.go:254`) has zero production callers and `MCP_SubscribeHealthChanges` has zero frontend callers. `KindMCPHealthChanged` likewise has zero emits. I14 cannot see it — the topic is two inline string literals, not a `*Topic` const. Served mode has one-shot health (`core/serve/connectors_rpc.go:70`); desktop has none at all. | P2 | escalate |
| `grammar-mode-unreachable` | `core/llm/capabilities/data/ollama.yaml:17` `grammar: true` | No adapter returns `"ollama"` from `Kind()`; local Ollama installs persist as `custom-openai`. Proven empirically: grammar=false for all seven registered kinds. `llm.go:34` claims "local runtimes (llama.cpp via Ollama) return true". | P2 | escalate |
| `responseformat-jsonmode-no-producer` | `core/llm/llm.go:431` | `ResponseFormat` outside `core/llm` has exactly one hit and it is a comment. `JSONMode` likewise. The whole structured-output surface — four `ApplyResponseFormat` impls, `core/llm/structured`, the registry's validate-and-repair-once loop — is reachable only from tests. `audit.go:1070 FormatMode string` has no writer either. | P2 | escalate |
| `catalog-unpublish-orphan` | `core/fleet/catalog.go:213` | `DELETE /api/v1/catalog/{id}` is implemented and unreachable. A user who publishes something sensitive to the team catalog has no in-app withdrawal path. | P3 | escalate (permissions question) |
| `provider-capabilities-table-orphan` | `core/session/migrations_provider_capabilities.go:19` | Migration 0329 creates `provider_capabilities`; nothing reads or writes it. **A second orphan table exists** (verifier): `agent_graph_node_provenance` (`migrations_manifest_provenance.go:29`, migration 0326) — three prose comments, no INSERT, no SELECT; its consumer `CheckManifestDrift` has only test callers. | P3 | escalate — an *applied* migration cannot simply be deleted |
| `mcp-prompts-never-fetched` | `core/mcp/builtin/sites/prompts.go:19` | The server registers a `deploy_a_site` prompt and `toolserver` serves `prompts/list`+`prompts/get`; the only client caller is the one-shot Test-connection dialog, which takes `len(res.Prompts)`. No pool exposes `ListPrompts`. | P3 | escalate |
| `acp-receive-no-evaluator` | `core/policy/cedar/types.go:270` | `default_acp_policy.cedar` ships four `acp_receive` rules to every user with no evaluator and no inbound path (`envelope.AcceptTask` has zero callers; `core/acp/doc.go:18` documents a `server/` package that does not exist on disk). Its twin `ActionACPSend` **is** wired. `core/rpc/api.go:2236-2237` claims both gates "actually enforce policy". | P3 | escalate |
| `synckind-org-scope-declared-only` | `core/fleet/synckind.go:130` `HasScope` | Zero non-test callers; `CategoryConfig()` hardcodes `ScopeUser` at `:183`. Four kinds declare `Scopes: {ScopeUser, ScopeOrg}`, advertising an org layer that does not exist. | P3 | justify — blocker is the unstarted `kitty-specs/fleet-org-config-inheritance-01NORGX01/`; **needs a named owner and a date** |
| `unit-promote-merge-request-no-caller` | `core/rpc/views/fleet/impl.go:63` | Zero frontend references while its four Phase-3 siblings all have them. The docstring frames it as the safe alternative to writing the higher layer directly — i.e. the direct-write path is the one that ships. | P3 | escalate (governance) |
| `rpc-orphan-binding-inventory` | `core/rpc/bindings.go:2713` | **Nine** orphan bindings (not seven), from a 459-method surface diffed against a 16,460-token index of all 518 `frontend/src` files: `CedarPolicy_ListPlanModeActions`, `Diag_LogPath`, `Sessions_StartCapture`, `Sessions_StopCapture`, `Sessions_DeleteWithOptions`, `Contexts_ContextSearch`, `Contexts_ContextExport`, `Unit_PromoteAsMergeRequest`, `Unit_ResolveLoadable`. Two carry docstrings naming a UI that does not exist. | P3 | escalate per-binding; do not batch-delete |
| `llm-updateprovidercredential-*` | `core/rpc/bindings.go:381` | Zero frontend references; docstring at `:379-380` claims "The frontend ONLY calls this when the user has typed a new key value". **Correction:** the live substitute is **not** `TestAndRotateKey` (whose only `.vue` caller is `AuthFailureToast.vue:104`, a mid-session rotation toast) — it is `ProvidersView.vue:122` `client.llm.updateProvider(input)` / `:124` `addProvider(input)`. Re-ground the delete on those. | P2 | delete; **the docstring fix ships regardless** |

---

### Class D — inert dial (20)

A read that only copies the value into another struct is not consumption. Every
finding below was followed to a branch.

| ID | Dial | Where it dies | Sev | Disp |
|---|---|---|---|---|
| `knobs-0330-columns-no-reader-no-writer` + `session-tune-panel-inert` + `effort-metadata-wire-shape-mismatch` | reasoning effort / thinking budget | Three independent breaks in one feature. (1) `core/session/migrations_knobs.go:37` `ALTER TABLE sessions ADD COLUMN knobs_default TEXT;` — the column has **no reader and no writer**; `Sessions_SetKnobsDefault`, named in `SessionTunePanel.vue:12`, does not exist as a binding. (2) `SessionTunePanel.vue:110` `saved.value = true;` fires unconditionally; the value reaches only `activeReasoningConfig` in `SessionsView.vue`, which is never serialised or sent. (3) `/effort` emits snake_case JSON (`llm.go:208` `json:"openai_effort"`) while the only reader looks for `knob['openAIEffort']` (`SessionsView.vue:755`) — the keys are absent, so `newConfig` stays `{}`. `llm.Request.Knobs` has **zero production writers**. | P1 | wire — three separate lies ("Saved", "takes effect on the next message", `/effort` success) for a setting that reaches nothing |
| `skill-model-invokable-not-enforced` | `model_invokable` | `core/tools/skill/skill.go:4` "has model_invokable=true set in its frontmatter." `Dispatch.Run` (`core/slashcmd/dispatch.go:97-200`) never reads `cmd.ModelInvokable`; `LoadUserOne` has no such predicate. The only behavioural consumer is the system-prompt catalog builder. `core/rpc/builtins_wiring.go:729` asserts the opposite in a comment. **A user-facing permission checkbox that enforces nothing at execution time.** | P1 | wire — one guard in `Dispatch.Run` + planted-violation proof |
| `shortcut-overrides-inert` | `Settings.keyboardShortcuts` | `frontend/src/shell/Shell.vue:169` `shortcutOverrides.value = s.keyboardShortcuts ?? {};` — its **only** use is `:overrides` on `CheatSheetModal`. All 17 registry ids have zero runtime consumers; the real handlers hardcode (`Shell.vue:146`, `:152`, `useCommandPalette.ts:121`, and the native menu's literal `keys.CmdOrCtrl("n"/"f"/"k"/",")`). `lib/shortcuts/registry.ts:5-6` asserts "Components and composables READ from this file; they never hard-code binding strings." | P1 | wire — rebinding changes only the cheat sheet, which then *reports a state the app is not in* |
| `corpus-read-filters-and-cap-ignored` | 4 of 7 `corpus_read` attrs | `core/agentgraph/exec_state.go:106` reads only `CorpusIds`, `Query`, `TopK`. `source_path_prefix`, `mime_types`, `score_threshold`, `max_bytes` have generated `Validate()` bounds and no reader. The shipped `retrieve` activity claims a 16 KiB cap "with the kernel's `corpus_overflow_dropped` event" — that event kind has **no constant, no emitter and no reader anywhere**, and `agent-kernel-graph-01KQ6391/tasks.md:150` records WP11-T6's acceptance criteria as "50 KiB retrieval truncates to 16 KiB; `corpus_overflow_dropped` event emitted". **Also class F.** | **P1** (raised from P2) | wire |
| `bundle-model-prefs-inert` | `Bundle.model_prefs` | `core/fleet/bundle.go:63`. The applier copies into `fleetState.fleetModelPrefs`; the only reader of that is the accessor `FleetModelPrefs()`, which has **zero callers**. Chain dead-ends one hop later. Un-applied section appends no error → `ack.go:61` reports `applied:true`. | P2 | wire |
| `engine-cache-nil-rerun-inert` | `rerun_policy` | `core/workflows/runtime.go:157` `if e.Cache != nil && wf.RerunPolicy != "" && !opts.SkipCache {` — `Cache` assigned only in `_test.go`. `schema.go:42-50` **rejects** an invalid value, telling the author the field is real, while all six accepted values behave identically. `ErrRerunPolicyAsk` can never be returned; `skipCache` is a no-op. | P2 | escalate (changes billing/freshness semantics) |
| `recipe-timeout-dials-inert` | `init_timeout_ms` / `ping_period_ms` | `core/mcp/recipes/recipes.go:87`. Declared on all 117 catalog recipes, copied by nothing; `mcp.ServerSpec` has no field to copy them into. github/sqlite/slack ask for 15 s and get the 5 s process-wide default. **Worse than filed** (verifier): on http and sse there is **no init deadline at all** — `ConnSpec.InitTimeout` and `FirstByteTimeout` are assigned from pool opts and then read by nothing, so for the 60 http recipes the declared value is discarded into a field that is itself inert. | P2 | wire or delete both fields + fix `health.go:7` |
| `capability-cache-refresher-orphan` | `HARNESS_LLM_CAPABILITY_CACHE` | `core/llm/capabilities/cache.go:21`. `DefaultCache`, `NewMemoryCache`, `NullCache`, `NewRefresher`, `MaybeRefresh`, `Start` all have zero non-test callers. `RefreshFunc`'s doc says implementations "call the adapter's ProviderCapabilities method" — **no adapter has one, and `llm.CapabilitiesProvider` (`capabilities.go:318`) has zero implementors** while two comments assert it is preferred at dispatch. The archived spec promised `sqlite \| memory \| off`; the **sqlite arm has a shipped schema with no code** (migration 0329). | P2 | escalate |
| `stdio-sampling-hardcoded-off` | `SamplingEnabled` | `core/mcp/transport/stdio/pool.go:160` `SamplingEnabled: false,` on every spawn. `SetSamplingEnabled` has zero non-test callers; `EnabledRecipe.SamplingEnabled` has one writer and **no reader**; `Recipe.SamplingPolicy.Allowed` has no reader at all — while `core/rpc/api.go:4104` constructs `stdio.LLMSamplingHandler` in production. | P2 | escalate — `sampling.go:20` names the blocker with no owner |
| `sentry-identified-tier-inert` | crash-reporting tier | `main.go:552` `tier := coresentry.ResolveTier(s.CrashReportingTier, false /* fleet login state TBD */)`. **Worse than filed** (verifier): `TierIdentified` has **no consumer anywhere** — `client.go:59` branches only on `TierOff` and there is no `SetUser` call in `core/sentry`. So threading real login state would return `TierIdentified` and still attach no user tag. The tier is inert at both ends. | P2 | wire (both ends) |
| `update-manifest-url-404` | Layer-3 update fallback | `useUpdateStore.ts:37` `export const MANIFEST_URL = 'https://docs.kameas.ai/downloads/manifest.json';` vs the canonical `https://downloads.kameas.ai/kenaz-harness/manifest.json` (`core/update/manifest.go:44`, `release.yml:1276`). *Correction:* docs.kameas.ai **is** a real mirror; the defect is the missing `kenaz-harness/` path segment plus the mirror lagging the CDN. The header states this exists to guard "where the backend update service silently breaks again" — the exact scenario in which it cannot fire. | P2 | wire |
| `telemetry-otlp-no-producer` | `core.Options.Telemetry` | `core/core.go:124`. Zero writers repo-wide including tests; all four production `core.Options{}` literals omit it, so `OTLPEndpoint` is always `""` and the three OTLP exporters plus the dual-export composite arm are dead. The live outbound path is `core/fleet/otlp_pipeline.go`, which has redaction; `core/telemetry`'s does not. | P2 | escalate (rival infra) |
| `narrative-settings-gate-inert` | `MemoryNarrativeEnabled` | `core/memory/narrative/feature_flag.go:35`. The ledger records this as **drained** (2026-08-13, 01PMGX01 WP17). It is not: the chain is `Enabled` → {promoter, citation detector, `LongTermEnabled` → `LoadPrelude`} and **all three are unreached**. `api.go:1437-1460` carries a 24-line comment asserting "a runtime toggle takes effect on the next turn without a restart". | P2 | justify (blocked on the narrative escalation) — **but correct the comment and re-date the ledger entry this release** |
| `parallel-dispatch-attr-inert` | `parallel_dispatch` | `core/agentgraph/exec_dispatch.go:419` `if len(calls) > 1 && a.MaxConcurrent != 1 {`. No non-test reader of `a.ParallelDispatch` exists, yet four shipped/library graphs set `parallel_dispatch: true`. The manifest says "set false for serial dispatch" (`tool_dispatch.yaml:46`). *Correction: there is no bespoke editor control — the manifest description is the load-bearing lie.* | P2 | delete (live substitute `max_concurrent: 1`) or wire |
| `required-capability-inert` | `Recipe.RequiredCapability` | `core/mcp/recipes/recipes.go:137`. Zero readers; `core/fleet/sites_reconciler.go:31` hardcodes the recipe id and `:69-72` the capability. A second capability-gated recipe would declare the field and get no gate at all, silently. | P3 | wire |
| `workflow-secrets-unenforced` | `Workflow.Secrets` | `core/workflows/types.go:314` — the comment promises a run-start assertion against the live ExposureIndex "so the model never reaches a step that would fail mid-run". Only shape validation exists (`schema.go:62-63`); `Engine.Run` never reads `wf.Secrets`. | P3 | wire or delete the promise |
| `workflow-slash-command-unread` | `slash_command` | `core/workflows/types.go:303`. Zero readers; the `/wf <id>` gateway is the shipped path. | P3 | delete (or reject the key so authors get an error instead of silence) |
| `storage-config-wal-fk-inert` | `Config.WAL` / `.ForeignKeys` / `.EventBufferSize` / `.SecretsBackend` | `core/storage/storage.go:90`. `Open` builds a literal DSN (`sqlite.go:79-80`) so `Config{WAL:&false}` still opens WAL and `ForeignKeys:&false` still enforces FKs. `Config.applyDefaults()` and `validate()` have **zero callers including tests** — dead code inside a live file. Not cosmetic: the ledger's 0327/0332 entries turn on `foreign_keys(1)` always being on. | P3 | delete (substitute: the DSN literal) — **include `SecretsBackend`, omitted by the finder** |

---

### Class E — frontend placeholder (16)

Orphan-FILE scans miss most of this; the cost is dead exported symbols and
hardcoded literals **inside live files**.

| ID | Site | Consequence | Sev | Disp |
|---|---|---|---|---|
| `recipe-source-badge-hardcoded` | `KenazToolsPanel.vue:609` `return 'shipped';` | `sourceBadge(_listing)` ignores its argument. Its justifying comment claims "that is the only catalog the backend exposes today" — false: `mcpLiveCatalog` merges `recipes.Shipped()`, `recipes.Registry()` and the user store (`api.go:3666` says so explicitly). **Understated by the finder:** `shipped.json` has 2 recipes and `registry.json` has 115, so the badge already lies on ~113 rows on a fresh install, before any user import exists. The one control whose job is provenance for a server spawned with the user's credentials always gives the same answer. | **P1** (raised) | wire or remove the badge |
| `sync-panel-installed-mcp-id` | `SyncPanel.vue:65` `id: 'installed_mcp_servers',` | Canonical wire value is `installed_mcp` (`core/fleet/sync.go:34`). `statusFor` never matches, so the row reads Enabled=false / "Last synced: Never" forever, and toggling surfaces a raw `fleet/sync: unknown category`. `SyncPanel.spec.ts:56` hand-builds a status row with the wrong id, so the suite is green — **CLAUDE.md blind spot #2 exactly**. | P2 | wire (one token) — land with `installed-mcp-sync-nil-halves` or the row becomes inert instead of broken |
| `recipe-status-counts-hardcoded-zero` | `core/mcp/transport/stdio/status.go:52` `PromptCount:     0,` | Both counts hardcoded in the only producer; no overlay writes them; no live path ever issues `resources/list` or `prompts/list`. `KenazToolsPanel.vue:1329-1330` renders them under "Tools / Resources / Prompts". Every server shows "N / 0 / 0", indistinguishable from a real measurement. | P2 | wire or delete the row |
| `credstatus-tautology` | `CatalogPreviewDrawer.vue:69` `return missing.includes(cred) ? 'missing' : 'configured';` | `missing` is `requiresCredentials`, and the only caller iterates that same array — the false branch is unreachable by construction. The chip is always red "missing". | P2 | wire (with `catalog-reciperegistry-nil`) |
| `template-http-save-invalid` | `SimpleTemplateEditor.vue:179` | The `write_artifact` starter emits `path:` and `content:` and no `title:`. `path` is not a key on `workflows.Step`; `schema.go:257-258` rejects the document outright. Picking the third of three starter templates and clicking Save **always** fails. | P2 | wire (after `wfdeps-artifacts-nil`, or it saves and then cannot run) |
| `template-wrong-interpolation` | `SimpleTemplateEditor.vue:165` `` `Execute the plan from {{ steps.plan.output }}` `` | The engine expands only `${…}` (`core/workflows/refs.go:9-18`); every shipped builtin uses that form. The "plan then execute" template produces two independent turns — the execute step is prompted with the literal characters and never sees the plan. The workflow **succeeds**, so nothing signals the break. Same at `:180`. | P2 | wire |
| `workflow-run-deeplink-inert` | `WorkflowRunsSection.vue:67` | Pushes `/workflows?run=<id>`; `WorkflowsView.vue` (785 lines) has **zero** matches for `route` or `query`. Its `workflow-run:focus` emit has no listener either. Clicking a run row navigates away and tears down the inline expansion without selecting anything. | P2 | wire |
| `branch-breadcrumb-ancestor-dead` | `BranchBreadcrumb.vue:32` `ancestorCount?: number;` | The sole mount passes three props; `turnNumber` and `ancestorCount` are never passed, so both `v-if` guards are always false and `label` always falls through to "Branch of". The docstring claims it displays "Branch from turn N of <parent>". | P2 | wire |
| `audit-drawer-synthesised-traceid` | `AuditEventDrawer.vue:65` `// Synthesise a trace_id from the trailing hex for demo purposes.` | Pads the payload-hash prefix to 32 chars and presents it as an OTel trace id. The block that renders it is unreachable (`otelActive` is never passed). **Disposition corrected:** the finder said delete-all on "no producer + no intent" — but the repo ships a live OTel subsystem and a completed OTel mission (`audit.go:284`, `KindFleetTelemetrySent`). Correct split: **delete the fabricator at `:65-71` now** (wrong under every branch), **escalate** `otelActive`/`traceBaseUrl`/`TraceLink.vue`. The live landmine is that "finish the OTel feature" would link every audit entry to a trace id that exists in no backend. | P2 | delete + escalate |
| `compaction-overhead-row-writerless` | `SessionsView.vue:948` | `compactionOverheadUSD`/`Tokens` have **no assignment anywhere**, so the header row is permanently hidden. Self-documented TODO naming compaction-strategy-ui WP08+. **Disposition corrected:** labelled "justify" with `OWNER: unassigned`, which does not satisfy the rule. **RULED 2026-08-19 by X-7** (`docs/escalation-register-2026-08-19.md` Part 0) — owner alec, **wire it**; merged with `CK-08` (`:1405`), its producer half. | P3 | ~~assign an owner or downgrade to escalate~~ → wire (X-7) |
| `event-family-orphan-const` / `event-family-orphan` | `frontend/src/lib/hooks.ts:94` `export const EVENT_FAMILY` | Two lines repo-wide: its docstring and its declaration. Its siblings from the same file **are** consumed. The docstring promises "a display category for the UI grouping"; the picker renders a flat 17-item `<option>` list. **Disposition corrected:** a flat picker is not a live substitute — nothing does the job it was written for. | P3 | delete-or-wire (owner's call); the docstring must not keep asserting a consumer |

Plus five verifier-found placeholders in §6 (`SlashArgFill.prefilled`,
`SlashCommandEditor.readOnly`, `HealthPill.label/compact`,
`MessageList.scrollToBottom` expose, `ConfirmToolModal.reconcile` expose).

---

### Class F — manufactured success (12)

The most severe class: not an inert toggle, fabricated evidence.

| ID | Site | What is fabricated | Sev |
|---|---|---|---|
| `noop-chatrun-completed` | `core/scheduler/chat_dispatcher.go:89` `Status:        "completed",` | A permanent `completed` row in user-visible scheduled-chat run history, with `EndedAt` set, for a dispatch that never happened. | **P0** |
| `fleet-cedar-engine-never-wired` | `core/fleet/config_pull.go:311-315` → `ack.go:61` | `applied:true` posted to the fleet control plane for a signed Cedar bundle that was discarded. | **P0** |
| `approval-node-fabricates-approval` | `core/agentgraph/exec_control.go:1534` `res.Outputs["approved"] = inputs["in"]` | An `EventApprovalResolved{approved:true, auto:false}` row in the run trace attesting a human approved. The executor never blocks, never evaluates `PolicyLabel` against Cedar (despite `approval.yaml:21`), and `auto_approve_window_seconds: 0` — documented at `:25` as "0 disables" — still auto-approves **and records `auto: false`**. The `rejected` output port is one of only two declared-but-never-written ports in the whole manifest set. The code self-declares the lie twice (`:1499-1502`, `:1531-1533`). | P1 |
| `embedder-test-fabricates-ok` | `SettingsView.vue:567` `embedderTestStatus.value = 'ok';` | "Connection OK" after one call — `client.settings.getEmbedderConfig()`, whose entire Go body is `return a.store.LoadEmbedderConfig()`, a settings-file read. Its justifying comment ("We don't have a dedicated TestEmbedder RPC yet") is **false on this tree**: `Memory_TestEmbedder` exists, is on the same client object, and is already used by `MemoryHealthPanel.vue:54`. Converts a diagnosable failure into an undiagnosable one. | P1 |
| `embedder-health-always-ok` | `core/memory/capture_rate.go:85` `RecordEmbedCall` | Zero production callers (also `RecordEmbedError`), so `lastEmbedDuration` stays 0 and `embedErrors` stays empty — the `"slow"` and `"error"` branches are unreachable. The LegendBar pill, mounted unconditionally, reports embedder health "ok" while the embedder is timing out. Positive false evidence. | P1 |
| `slashcmd-tool-dispatcher-nil` | `core/slashcmd/dispatch.go:189` `Text: fmt.Sprintf("would dispatch: %s %s", …)` | `core/rpc/api.go:1549` `NewDispatch(slashStore, nil)` — the second parameter **is** `tools ToolDispatcher`, with no setter. A Tool-kind slash command (created in `SlashCommandEditor.vue:259-263`, kind label `tool: 'Tool dispatch'` at `:75`) returns the info bubble "would dispatch: kenaz__bash ls -la" with `dry_run: true` metadata **no frontend code reads**. The same nil Dispatch is handed to `registerBuiltinTools` (`api.go:4161`), so the model-invoked `__skill` path dry-runs too. | P1 |
| `effort-metadata-wire-shape-mismatch` | `core/slashcmd/cmd_effort.go:70` | `/effort high` prints "reasoning effort set to \"high\" — takes effect on the next message" and provably cannot land (see class D). | P1 |
| `rpc-memory-narrative-nil` / `mark-important-silent-noop` | `core/rpc/views/memory/impl.go:651` | "Important" button returns success and increments nothing. | P1/P2 |
| `remote-status-synthesised-running` | `core/mcp/dispatch/pool.go:337` and **`:385`** | Both `RecipeStatus` and `AllRecipeStatuses` hand-build `State: "running"` for any id with an ownership entry, never re-checked and never cleared on later failure, with `LastError`/`ToolCount`/`ProtocolVersion` all zero. The health panel consumes `:385`. A remote connector that has started 401ing shows healthy for as long as the app stays up. **Fix is bigger than surfacing the probe:** `OnFailure` at `http/pool.go:160-162` logs and returns — there is no last outcome to surface, so probe-side state is needed too. | P2 |
| `skill-tool-kind-claims-dispatch` | `core/slashcmd/cmd_skill.go:49` | `/standup: dispatching kenaz__bash` printed into the transcript with **no metadata at all** and no frontend tool-routing branch. Unlike the dry-run path this text does not even hedge with "would". | P2 |
| `upgrade-snapshot-missing-for-latest-tag` | `core/storage/sqlite/upgrade_path_test.go:68` | `v0.64.0` is the latest tag; `testdata/upgrade/` holds only v0.63.0–v0.63.2. The test is table-driven over `os.ReadDir` and skips dirs without `dump.sql`, and `check-upgrade-snapshots-locked.sh` uses `LATEST_TAG` only to *exempt* ahead-of-release dirs — it never asserts one exists. Both pass at exit 0 with the newest release entirely uncovered. **First live instance of the class the ledger records as ungated.** | P2 |
| `planmode-approve-optional-chain` | `PlanApprovalModal.vue:56` | Latent today (the modal cannot mount). Becomes a recorded approval for work never done the moment plan-mode is wired — which is why it must land in the same PR. | P2 |

---

## 3. Everything user-visible, in one table

87 distinct findings a user could notice. Ordered by severity, then by how many
users hit it.

| Finding | What the user sees | Sev | Class |
|---|---|---|---|
| `catalog-no-azure-custom-entry` | Azure / Ollama / LM Studio / Jan / GPT4All: every tool call and every image fails | P0 | ◆B |
| `fleet-cedar-engine-never-wired` | Org Cedar policy pushed, ACKed as applied, enforced by nobody | P0 | ★A/F |
| `scheduled-chat-no-cron-producer` | Scheduled chats never fire, ever, with no error | P0 | C |
| `noop-chatrun-completed` | "Run now" writes a fabricated `completed` row into run history | P0 | F |
| `bedrock-tool-results-dropped` | Bedrock: tool results discarded; model loops or hallucinates | P1 | ◆B |
| `azure-custom-tool-roundtrip-dropped` | Azure/custom: second tool turn 400s (masked until B1 is fixed) | P1 | ◆B |
| `oauth-signin-btn-36-recipes` | "Sign in to Notion/Linear/Figma/Dropbox/Slack" — 36 connectors, always errors | P1 | ◆B |
| `oauth-clientid-not-substituted` | 14 connectors open the authorize page with a literal `${…}` client_id | P1 | ◆B |
| `remote-closeone-noop` | Uninstalled remote connector keeps polling with your bearer token | P1 | ◆B |
| `cloudsync-denylist-posix-only-on-windows` | Windows OneDrive/Dropbox users get no WAL-corruption refusal | P1 | ◆B |
| `A3` hook cluster (5 findings) | 16 of 18 hook events save, show Enabled, never run | P1 | ★A/C |
| `A4` audit cluster (10 findings) | Audit log has no workflow / export / OAuth / policy / slash / update record; Export button errors | P1 | ★A |
| `permissions-engine-nil` | Revoked permission stays enforced until restart | P1 | ★A |
| `secret-ref-resolver-never-installed` | `@secret:` literal sent to third-party hosts and into your shell | P1 | C |
| `fsfull-recommended-policy-unmatchable` | Accepted filesystem hardening policy protects nothing | P1 | C |
| `skill-model-invokable-not-enforced` | `model_invokable: false` slash commands are still model-runnable | P1 | D |
| `exit-plan-mode-approval-unreachable` + `planmode-subscribe-missing` | Plan mode: no modal, no badge, session appears stalled | P1 | C/★A |
| `knobs-0330` + `session-tune-panel-inert` + `effort-metadata-wire-shape-mismatch` | "Saved" / "takes effect next message" for a reasoning setting that reaches nothing | P1 | D/F |
| `reasoning-output-anthropic-only` | Extended thinking on o-series/Gemini/Bedrock: pay for thinking, see nothing | P2 | ◆B |
| `shortcut-overrides-inert` | Rebinding a shortcut changes only the cheat sheet | P1 | D |
| `cost-reducer-never-wired` + `last-usage-json-write-only` + `autotitle-overhead-never-read` | Cost is $0/unknown outside OpenRouter; footer reads zero on reopen | P1 | ★A/C |
| `embedder-test-fabricates-ok` | "Test embedder" says "Connection OK" without testing | P1 | F |
| `embedder-health-always-ok` | Memory pill reports "ok" while embedding is failing | P1 | F |
| `sentry-lastfive-no-writer` + `sentry-set-harness-version-lie` | "Recent crash events (last 0)" forever; every crash report says version "dev" | P1/P2 | C/orphan |
| `slashcmd-tool-dispatcher-nil` + `skill-tool-kind-claims-dispatch` | Tool-kind slash commands print "would dispatch"/"dispatching" and do nothing | P1/P2 | F |
| `worklist-dir-never-set` | `kenaz__list_open_worklist` tells the model to stop working, every time | P1 | ★A |
| `wfdeps-artifacts-nil` | Doc Generator burns an LLM turn then fails on save | P1 | ★A |
| `wfdeps-netauthz-nil` | Strict workflow Cedar mode denies nothing | P1 | ★A |
| `audit-retention-backend-nil` | Retention window saves, confirms, deletes nothing | P1 | ★A |
| `catalog-withpubkey-never-called` | Fleet catalog **and skill** installs skip signature verification | P1 | ★A |
| `sessions-deletewithoptions-no-caller` | Deleting a session always destroys its artifacts, silently | P1 | C |
| `corpus-read-filters-and-cap-ignored` | `score_threshold`/`mime_types` ignored; the advertised 16 KiB cap does not exist | P1 | D/F |
| `approval-node-fabricates-approval` | Graph `approval` node never pauses and records a human approval | P1 | F |
| `recipe-source-badge-hardcoded` | Every connector badged "SHIPPED", including registry and pasted ones | P1 | E |
| `use-tool-action-no-evaluator` | A `forbid … use_tool` policy you author is silently ignored | P2 | C |
| `remote-status-synthesised-running` | Broken remote connector shows "running", 0 tools, no error | P2 | F |
| `recipe-status-counts-hardcoded-zero` | Every server shows "N / 0 / 0" resources/prompts | P2 | E |
| `recipe-timeout-dials-inert` | Slow servers fail a handshake their own declared timeout would have covered | P2 | D |
| `compaction-graph-seams-unwired` | "Custom subgraph" is selectable, savable, and fails at run time | P2 | C |
| `audit-chain-skip-no-caller` | After one chain break, archival stops forever with no recovery control | P2 | C |
| `bundle-model-prefs-inert` | Org default model / provider allowlist accepted, ACKed, ignored | P2 | D |
| `sync-panel-installed-mcp-id` | One of five sync rows errors on click and reads "Never" forever | P2 | E |
| `lockdown-reason-dropped` | Lockdown banner on boot omits the admin's reason | P2 | ◆B |
| `site-env-vars-orphan` | Dynamic sites deploy unconfigured; no way to supply declared env vars | P2 | C |
| `contexts-markapplied-no-caller` | Contexts "Recent" pane empty forever | P2 | C |
| `contexts-search-export-no-caller` | No way to search or export the shared context graph | P2 | C |
| `prune-scheduler-never-started` | Memory grows unbounded unless you find the manual prune button | P2 | C |
| `unit-resolveloadable-no-consumer` | Resolved units never assembled into the conversation-start set | P2 | C |
| `catalog-reciperegistry-nil` + `credstatus-tautology` | Every MCP workflow flagged missing-credentials; chips always red | P2 | ★A/E |
| `template-http-save-invalid` + `template-wrong-interpolation` | Two of three workflow starter templates cannot save or cannot chain | P2 | E |
| `engine-cache-nil-rerun-inert` | `rerun_policy: skip` / `prompt` re-runs and re-bills every time | P2 | D |
| `workflow-run-deeplink-inert` | Clicking a run row navigates away and selects nothing | P2 | E |
| `branch-breadcrumb-ancestor-dead` | Branch bar never shows "from turn N" or the ancestor chain | P2 | E |
| `documentchip-pagecount-no-producer` | PDFs never show "12 pages" despite being parsed on ingest | P2 | C |
| `update-manifest-url-404` | The update safety-net cannot fire in the scenario it exists for | P2 | D |
| `sentry-identified-tier-inert` | "Identified" crash reporting stays selected and runs anonymous | P2 | D |
| `attachment-registry-never-wired` + `tool-schemas-never-populated` | `as_attachment: true` silently inlines; bad tool args reach the tool | P2 | ★A |
| `parallel-dispatch-attr-inert` | `parallel_dispatch: false` still dispatches in parallel | P2 | D |
| `bash-nil-logger` | A failed "Allow always" leaves no trace anywhere | P2 | ★A |
| `dual-cmdf-search-surfaces` | ⌘F: either a dead search modal or two overlays stacked | P2 | other |
| `mark-important-silent-noop` | "Important" button succeeds and changes nothing | P2 | F |
| `catalog-unpublish-orphan` | No in-app way to withdraw something published to the team catalog | P3 | C |
| `scroll-position-no-caller` | Switching back to a session loses your scroll position | P3 | orphan |
| `workflow-input-kind-variant-gap` | Enum workflow inputs render as free text; no file/artifact pickers | P3 | ◆B |
| `wfdeps-tools-nil` | A "Tool call" canvas node always fails at run time | P3 | ★A |
| `workflow-secrets-unenforced` | `secrets:` gives no fail-fast; the run dies mid-way instead | P3 | D |
| `workflow-slash-command-unread` | `slash_command:` on a workflow is silently ignored | P3 | D |
| `required-capability-inert` | A second capability-gated recipe would get no gate, silently | P3 | D |
| `cedarpolicy-planmodeactions-phantom-panel` | Policy authors get no plan-mode reference panel (two docstrings claim one) | P3 | orphan |

---

## 4. Coverage gaps — what this sweep did **not** look at

Built from the fifteen module agents' own coverage notes. Read this before
treating any area below as clean.

### Not swept at all

- **`core/mcp/oauth/**` per-symbol.** Package importers and the ClientID data
  path were traced; `SlackSignIn`, `SlackSignInWithDiscovery`,
  `ResolveSlackClientID`, the whole `DCRStore`, `RegisterClient` and `Refresh`
  were **not** reader-audited. Given 30 recipes declare
  `primary_auth: browser_oauth_dcr` and neither
  `PrimaryAuthBrowserOAuthDCR` nor `...PKCE` has a Go branch anywhere, a second
  per-variant gap is *suspected* here. **Highest-value follow-up in the sweep.**
- **`core/bundle/{cache,manifest,lockfile,kinds,channels}`.** Only the orphan
  sub-packages were confirmed against the I7 allowlist; the live sub-packages
  were not audited for inert dials or dead symbols inside live files. Largest
  uninspected block in `misc-backend`.
- **`core/trust/**` internals** (~2,000 lines: anchor, revocation, rotation,
  sign, verify, envelope, policy, bundleadapter, `internal/algo`). Established
  as an orphan cluster and stopped. Needs its own pass if
  `core/bundle/integrity` is ever wired.
- **`core/fleet` OTLP lane** (`otlp_pipeline.go` 674 lines, `otlp_ack.go`,
  `otlp_transport.go`, `telemetry_redactor.go`, `telemetry_optins.go`,
  `log_event_kind.go`). Setters confirmed called from `core/rpc`; nothing else.
  This is exactly where a per-variant gap would hide.
- **`core/fleet` context/unit/session/project/skill sync** —
  `context_graph_sync.go` (1114 lines), `context_sync.go`, `unit_sync.go`,
  `unit_mapper.go`, `session_sync.go`, `project_sync.go`, `skills_sync.go`.
- **`core/agentgraph/compaction/**` beyond one Config diff.** `CompactionConfig`
  / `SiteConfig`'s ~15 cascading knobs were **not** traced to consumers, nor
  were `strategies.go`, `session_rolling.go`, `session_snap.go`,
  `pipeline.go`, or the `compaction/wiring/` adapters. The ledger records four
  historical inert-toggle finds in this area — **highest-value unswept surface
  in `agentgraph`**.
- **`core/rpc/views/agentgraph/chat/**` (13 files, ~5k lines)** — stream_bridge,
  llm_provider_adapter, overflow_recovery, partial_flush, session_compaction,
  autotitle, merge_suggestion. Only the compaction seams and the env_context
  surface were read.
- **`core/storage/internal/lockfile`** — all three build-tagged variants unread,
  and it is a textbook per-variant shape (Windows vs unix advisory locking).
- **`core/workflows/web/`** (`fetcher.go` 384 lines, `scraper_css.go`,
  `scraper_llm.go`) — only the authz call sites were read. The css-vs-llm
  scraper switch (`runners.go:764`/`:809`) is an unopened class-B candidate.
- **`core/workflows/storage.go`** (402 lines) and `core/units/store_sql.go`
  (584 lines) + `core/units/migrations.go` — no SQL-path audit, and **no
  upgrade-path reasoning was applied to the units block** (blind spot #3).
- **`views/artifacts/preview/`** (10 renderers + registry) — the per-MIME
  renderer switch is the class-B shape and its arms were not enumerated.
- **`views/agentgraph/` editor** — `GraphEditor.vue` (~470 lines),
  `NodeAttributeEditor.vue`, `NodePalette.vue`. The manifest-attr ↔
  editor-widget matrix (an attr type with no editor arm) is unexamined.
- **`frontend/src/lib/canvas/*`** (~69 KB) beyond the exported-symbol scan.
- **`core/serve` dispatch drift**, in full. `check-serve-dispatch-drift.sh` runs
  informational-only and lists a very large gap set;
  `docs/served-mode-boundary.md` declares six views as an intentional boundary,
  and **nobody separated genuine drift from that documented boundary.** This is
  the single biggest hole in `rpc-views`' coverage.

### Swept shallowly — importer counts or seam checks only

`core/corpus` internals (chunker, walker, vectorstore, store — two orphan
methods found and dropped as too thin); `core/contextbootstrap`
extraction/interview/quarantine/confidence; `core/event/{redact,chain,log,
secretref}` internals; `core/slashcmd` template/validate/loader internals and
whether each `Deps` gateway is non-nil at the `api.go:2078` call site (**a nil
there would make that command return "not wired" on every invocation — an
unclosed class-A candidate**); `core/tools/bash/{parser,pattern,exec,
background,store,dangerous}.go`; `core/tools/websearch` backends;
`core/llm/{fallback,localruntime,retry,events,credref,personal,envprovider,
httpx,tokenizer,model_profile*}`; `core/mcp/transport/{http,sse}` connection
internals (reconnect, ringbuf, progress, router); `core/mcp/recipes/{allowlist,
merged,keychain,substitution,import,user,primary_auth}.go`; `core/policy` and
`core/credstore` proper (both allowlisted orphans); `core/artifacts`
detectors and stores; `core/sessions/export/redact.go`;
`core/storage/migrations/{doctor,blocks,hash,ledger,bootstrap}.go` internals.

### Whole classes under-covered

1. **The `harnessClient.ts` → `.vue` direction.** Bindings were diffed against
   `frontend/src`, and the reverse was clean — but the ~250
   `client.<domain>.<method>` wrappers were **not** diffed against actual `.vue`
   callers. Method names are generic (`list`, `get`, `save`), so this needs a
   per-interface caller trace. **This is where "backend live, UI missing"
   findings would concentrate**; the richest seams look like `Corpus_*`,
   `Handoff_*`, `ACP_*`, `Unit_*` and `Memory_Narrative*`.
2. **Class D on `settings.Settings`.** ~100 fields were **not** traced to
   branches in `rpc-views`; the module deferred to
   `check-knob-coverage.sh`, which the ledger already records as covering one
   struct out of several. Class D is essentially uncovered for that module.
3. **`core/rpc/api.go`'s non-literal wiring.** 325 KB. Composite literals and
   constructor arguments were diffed; wiring done through **setter calls,
   `With*` options, or post-construction interface assignment** was not
   systematically enumerated — the `SetCedarEngine` / `SetSkillRefs` shape.
   A dependency injected by a setter that is never called was caught only when
   a finder happened to look.
4. **`defineEmits` → parent-listener diffs** were run for `components/**` but
   **not** for `views/**`; **declared-prop diffs** were run for `views/**` but
   not for the 13 `shell/*.vue`.
5. **Served vs. desktop divergence** beyond route-table and per-finding greps.
   No served deployment was exercised.
6. **Upgrade-path reasoning (blind spot #3)** was applied only in
   `session-storage`. No other module asked whether its findings behave
   differently on a database a previous release produced.
7. **Test-fixture bypass (blind spot #2)** was checked opportunistically; only
   `sync-panel-installed-mcp-id` was caught that way. `tools` and
   `events-hooks` explicitly did not assess it.

### Ruled out — recorded so nobody re-walks them

`kenaz__monitor` / `core/tools/monitor` (ledger); bash
`BackgroundSpawn`/`BackgroundEnd`/`SessionIDFromCtx` (ledger); todo
`Store.Read`/`Drop` (ledger); `core/mcp/fixture` (I7 allowlist);
harness-self MCP unattachment (ledger + owner ruling); the six frontend
view components with no importer (all ledgered); `SessionCloseDialog.vue`,
`BackgroundTaskChip.vue`, `CrashReportingOnboardingModal.vue` (ledger);
`Settings_SetShortcut(s)` (dated in-code by consent-surfaces-truth-01PMTR01
WP04); `stubPolicy` (ledger); `EffectiveLocalRuntimeRAMBytes` /
`LocalRuntimeRAMOverrideGB` (ledger); `initFeatureFlags` and
`stopReconnectPoller` (live via intra-file callers); `toolloop.IterCounter`
and `RegisterPassiveTool` (self-declared test seams); every `createFake*` /
`_reset*` / `__reset*` export (test seams — must not be reported as dead code);
6 registry recipes with `primary_auth: device_code` (handled by a static
branch); `Tools_SignInRecipe` (reachable as `client.tools.recipes.signIn`);
`recipes.UserStore.StartWatch` deletion and the `CUSTOM_RECIPE_AUTHORING_ENABLED`
closure (ledger); `MCPAutoRestartDisabled` (ledger).

### Method limits

Nothing was built, nothing was run, and no file was modified. Two exceptions,
both recorded by the finders: the `llm` module ran the real catalog and gate
from a **scratch module outside the repo** to prove the azure/custom capability
gap and the grammar-kind gap; the `events-hooks` module ran a throwaway probe
inside `core/slashcmd` (deleted immediately, no file left behind) to print the
`/effort` metadata's real JSON wire bytes — that evidence has since been
**replaced with the struct-tag citation**, because a reviewer cannot re-run a
deleted test.

---

## 5. Gates owed

Per the gate-extension rule: a find representing a class the existing gates
cannot see must extend a gate in the same commit, with a planted-violation proof
in `scripts/ci/gates_can_fail_test.go`.

| Class no gate can see | Findings it would have caught |
|---|---|
| **An exported `Config`/`Options`/`Deps` field of pointer/interface type with no non-nil assignment at any production literal** (class A) | 48 findings. This is the single highest-value gate in the sweep. A first cut over `core/rpc/api.go`'s ~55 view-Config literals alone would catch 20. |
| **A provider/platform/transport switch with an arm that returns zero, no-ops, or falls through** (class B) | 14 findings. Partly constructible today: widening `wirecheck`'s `inScopeAdapters` to the registered set would catch B1 and B3. |
| A capability-catalog `provider:` key with no registered adapter `Kind()`, and vice versa | B1, `grammar-mode-unreachable` |
| A broker topic expressed as **inline string literals** (I14 walks `*Topic` consts only) | `rpc-mcp-health-subscribe-dead` |
| An audit `Kind` constant with zero emit sites | 22 findings across `context.*`, `event-log`, `workflow.*`, `update.*`, `mcp.*` |
| A `testdata/upgrade/<LATEST_TAG>/` directory that does not exist | `upgrade-snapshot-missing-for-latest-tag` — `check-upgrade-snapshots-locked.sh` computes `LATEST_TAG` already and uses it only to exempt |
| A manifest-declared node attr with no non-generated Go reader | `parallel_dispatch`, the four `corpus_read` filters, `Inline`, `provenance` |
| A `hooks.AllEvents` entry with no `Fire`/`FireAsync` site | `allevents-events-with-no-fire-site` (6 events) |
| A Wails binding declared on `WailsBindingsLike` with no client adapter | `Planmode_{Approve,Discard,Edit}` |
| An exported frontend symbol whose only readers are `__tests__` | 8 orphan-symbol findings in `fe-lib` / `fe-components` |
| `settings.Settings` knob coverage | **still open** — already ledgered at `docs/unwired-ledger.md:693-710` |
| I7 fixpoint over live packages only (orphan **cluster roots**) | `trust-orphan-cluster-root` — the ledger's recorded closure of 42 is understated; add `core/trust`, `core/trust/internal/algo`, `core/policy`, `core/secrets/cache`, `core/secrets/preflight` |

---

## 6. Verifier-found extras — **single-pass, unrefuted**

These were found by the adversarial verification agents, not by the module
sweep, and have therefore **not themselves been through a refutation pass**.
Treat every one as needing a second read before action. Grouped by weight.

### Would have ranked P0/P1 had a finder filed them

1. **The entire LLM connector audit trail is discarded.**
   `core/llm/events/eventlog_sink.go:64` `NewEventLogSink` — zero callers
   anywhere including tests. `registry.Options.Emitter` is unset at both
   production sites, so `registry.New` falls through to
   `events.New(&events.MemorySink{})` (`registry.go:79`). All ten Emitter
   methods **are** called on the live path — RequestSubmitted,
   PreflightResolved/Failed, CapabilityRejected, RetryAttempted, StreamChunk,
   ResponseFinal, Cancelled, Error, PolicyDenied — writing into an in-memory
   slice nothing reads. `emitter.go:68-69` says "Plugging a real event-log
   binding is a one-line change at the Registry construction site". It was
   never written. Ten declared audit kinds and the `llm/connector` EmitterID
   appear nowhere else. **Wire** — this is the capability-denial and
   policy-denial audit record.
2. **The context_sync tier gate is inert for session, project and handoff
   sync.** `core/rpc/api.go:2687-2689` constructs all three syncers with a nil
   `caps`: `sessionSyncer := corefleet.NewSessionSyncer(flCl,
   contextSyncAudit, nil)`. `core/fleet/session_sync.go:71` `if ss.caps != nil
   && !ss.caps.Has(CapContextSync) {` is therefore always false and
   `ErrSessionSyncCapabilityRequired` is unreachable, while `EnableSync`'s doc
   (`:65-66`) states it returns exactly that. **The wiring knows:**
   `api.go:2670-2680` builds a working `capsFn` closure and discards it at
   `:2684` with `_ = capsFn // caps are surfaced to the fleet layer via the
   backends below` — and `core/rpc/context_sync_wiring.go:24-48` forwards
   everything through with no capability check of any kind. Every tier can
   enable fleet session sync.
3. **`cedar.WithPostureMode` has zero non-test callers — plan mode denies
   nothing.** `core/policy/cedar/posture.go:105`. `core/autonomy/presets.go:3-4`
   states "While active, every write-class Cedar action is denied." Nothing
   denies them. The only enforcement that runs is the knob preset
   (`presets.go:18-26`), which empties `KnobAutoApproveFamilies` and sets
   AskAlways — it downgrades to confirm, on the confirm path this repo has
   already recorded as weak. `posture.go:38-39` names two consumers of
   `PlanModeDeniedActions` and **both are unreached** (the second is the
   `ListPlanModeActions` RPC with no frontend caller). Trust-relevant: plan mode
   is a safety posture the model itself enters and the user is told is
   read-only.
4. **Three update dials are hardcoded at the poller.** `core/rpc/api.go:847`
   `if err := a.updateSvc.BackgroundPoll(pollCtx, 6*time.Hour, "stable");` —
   interval and channel both literal, and the poller is **not even gated on
   `AutoCheckUpdates`** (`api.go:838`'s only condition is `a.updateSvc != nil`).
   `UpdatesPanel.vue` ships and persists all three controls;
   `LoadAutoCheckUpdates` / `LoadUpdateChannel` / `LoadUpdateCheckInterval` have
   **zero readers** outside `core/rpc/views/settings`. Turn auto-check off and
   you are still polled every 6 h; pick Prerelease and get stable; pick 1 h and
   wait 6. Compounded by `core/update/service.go:150`
   `return s.checkChannel(ctx, "stable")` and
   `core/rpc/views/update/impl.go:116` `Channel:        "stable",`, which make
   `prereleaseManifestURL` and the whole 404-fallback branch unreachable —
   `docs/missions/auto-update.md:165-167` Acceptance Criterion 6 ("Channel
   fallback works") describes behaviour no production path can reach.
5. **`Retriever.WithSessionID` has zero production callers**, so
   `GlobalRetrievalHistory().Push` never fires
   (`core/memory/retriever.go:48`, guard at `:128`), and its consumer
   `LastRetrieval` (`core/rpc/views/memory/impl.go:737`) always returns an empty
   report — fully wired through `Memory_LastRetrieval` to
   `MemoryView.vue:461`. The retrieval-report panel is empty for every session
   on every install. `retriever.go:46` asserts "The production path calls this
   when wiring the kernel per-session."
6. **Two `await import('vue-router')` sites call `useRouter()` after an await**,
   so `inject` returns undefined and the call throws.
   `SettingsView.vue:503` (both memory-embedder banner CTAs, `:1439` and
   `:1447`) and `:601` (the "Reconfigure with assistant" handler) — the second
   catches the TypeError and tells the user onboarding restart **failed**,
   *after* the backend already created the session at `:595`. The correct
   pattern is one directory over at `SyncPanel.vue:28`.
7. **The `attachment` node kind always errors.** `core/agentgraph/exec_state.go:160`
   `if env.Attachments == nil {` — `Env.Attachments` has no production writer,
   so `seams.go:959-960` installs `nilAttachments{}` returning
   `ErrNotImplemented`. It escapes the I3 gate **only because a kernel testdata
   fixture exercises it** (`testdata/seam_fanout/attachment.yaml:3`) — blind
   spot #2 shielding a kind that always errors for real users.
8. **`Manager.SaveScrollPosition`'s sibling defect:**
   `core/session/manager.go:151` `WithSessionHookRunner` has zero callers
   *including tests*, so `Manager.FireSetup` and `Manager.FireCwdChanged` are
   also uncalled — the Go-side confirmation of A3's session arm.
9. **`sessions.WithExportOpts(…, nil, nil)` and
   `cedarpolicyview.NewAPIWithOptions(…, nil, …)`** — folded into A4 above.
10. **`cedarpolicyview.NewAPIWithDataDir(nil, c.DataDir())`
    (`core/rpc/api.go:2836`)** hands a nil-engine snippet writer to
    `MigrateBashAllowlist`. `reloadBestEffort` returns immediately on a nil
    engine, and ordering makes it observable: on the one boot where the
    migration fires, every migrated bash-allowlist permit is on disk but absent
    from the live PolicySet **until the user restarts**. The migration exists
    specifically so upgrading users do not lose their allowlist; today they lose
    it for exactly one session.

### Second instances of a filed finding

- `core/rpc/api.go:2653` — fleet **skill** installs skip signature verification
  identically to catalog installs (folded into A6's `catalog-withpubkey` row).
- `core/mcp/transport/{http,sse}/connection.go` — `InitTimeout` and
  `FirstByteTimeout` assigned and never read on **both** remote transports;
  `sse` additionally has `PingPeriod`/`PingTimeout` assigned with **no
  `health.go` at all**, so a dead SSE stream is never detected (http has one).
- `core/mcp/pool.go:32` `RequestTimeoutMs` — zero writers, zero readers, and its
  doc references `DefaultRequestTimeout`, **a constant that does not exist
  anywhere in the tree**.
- `core/mcp/recipes/recipes.go:130` `RecommendedPolicyTemplate` — set by both
  shipped recipes, both template files exist, and the "copy recommended policy"
  affordance its docstring promises **does not exist**. The `filesystem-full`
  recipe grants broad filesystem access and its authored containment policy is
  never offered. (Compounds C3: the policy is both unofferable *and*
  unmatchable.)
- `core/session/migrations_manifest_provenance.go:29` —
  `agent_graph_node_provenance`, a **second** orphan table, refuting the
  finder's "only one" sub-claim.
- `core/workflows/runners_notify.go:71` — there is **no MCP recipe with id
  `"push"`**, so one of the four accepted notify surfaces can never resolve;
  and `:124` calls a tool named `send_notification` that occurs **exactly once
  in the entire tree**, that line. The `os` arm is the only verified live
  dispatch. (slack/email arms: **UNCERTAIN** — the repo cannot confirm remote
  tool names.)
- `core/workflows/types.go:642` `Deps.SessionID` — the **second** unset field
  blocking `write_artifact`; wiring `Artifacts` alone still fails.
- `core/rpc/api.go:1610` sets `AutoCaptureToolOutputs` into the CaptureConfig,
  but `core/rpc/views/artifacts/sink.go:116-118` gates the tool-output path on
  `AutoCaptureCodeBlocks` — **the wrong flag**. `DetectToolOutput` has zero
  non-test callers and the dial has no writer from either end.
- `core/rpc/views/settings/api.go:1639` `EffectiveLocalRuntimeRAMBytes` — zero
  callers; its doc names a WP06 filter and a WP07 panel, neither of which
  exists. Its bindings are declared on `WailsBindingsLike` with no client
  wrapper. (Its package `core/system/resources` is already I7-allowlisted —
  resolving the allowlist line without wiring the consumer just moves the
  orphan.)
- `core/hooks/hooks.go:158` `AsyncTimeoutMs` — the doc says it "overrides the
  default async timeout"; `FireAsync` computes from the hook **config**
  (`fire.go:233`) and never consults the returned output.
- `frontend/src/lib/types.ts:1929`/`:1931` and siblings — the Go dry-run mapper
  marshals `watchPaths`, `asyncTimeoutMs`, `permissionDecision`,
  `permissionDecisionReason`, `updatedInput`, `updatedMcpOutput`;
  `HookDryRunDrawer.vue` renders exactly one of them (`additionalContext`).
- `core/menu/menu.go:107` — **⌘F is registered twice** (Windows/Linux Edit
  "Find" and View "Search"), both to `h.onFind`, on top of `Shell.vue`'s window
  listener. The `dual-cmdf-search-surfaces` escalation must therefore also ask
  *which menu owns ⌘F*, not only which surface ships.

### Dead surface and rival infra

`core/agentgraph/attrs_gen.go:246` `Inline` (manifest promises inline-vs-ref
behaviour, executor writes the block unconditionally);
`_archetype.state.yaml:7` `provenance` — inherited onto 10+ state attr structs
with **no reader anywhere**, while `exec_state.go:459` says "Provenance lands in
the EventLog regardless of the output port"; `Env.Recommender`
(`executor.go:316`) — no production writer and its own comment names the RPC
path as "the more useful recommendation site", i.e. rival infra;
`core/fleet/sync_mcp.go:194` `CategoryConfigForMCP` (substitute:
`installedMCPKind` + `SyncKind.CategoryConfig()`);
`core/fleet/context_graph_sync.go:92` `CapForClassification` — zero callers
*and ignores its own parameter*, returning `CapSharedTeamGraph` unconditionally
while its doc describes a per-classification policy;
`core/fleet/signing_key.go:44` `FleetSigningKey` (singular) — dead, and
`errors.go:27` + `bundle.go:11` document behaviour through it;
`core/session/store.go:160` `AppendContinuation` — zero non-test callers while
`views/sessions/impl.go:159` claims production threads through it and
`api.go:5205-5212` admits the opposite;
`core/storage/migrations/runner.go:153` `Rollback` — zero callers and no
operator surface, while `migrations/doc.go:39` states "Rollback(ctx, toVersion)
is an explicit operator action";
`frontend/src/lib/recipeCategories.ts:41` `CANONICAL_RECIPE_CATEGORIES`
(substitute: `Object.keys(RECIPE_CATEGORY_META)`, which `isCanonicalCategory`
already treats as authoritative);
`SessionsView.vue:1228` — the long-session nudge calls `dismiss()` on a
write-once ref inside a `<KeepAlive>` with no `:key`, so the banner is
permanently disabled after the first session switch, and the trailing comment
describes a reset "on re-instantiation" that KeepAlive makes impossible;
`SessionsView.vue:1219` — the nudge's token arm is fed `lastUsage.promptTokens`
(per-turn) against a parameter documented as "cumulative prompt tokens";
`SlashArgFill.prefilled` never passed while `SessionsView.vue:721` captures the
raw slash line into `fill.raw`, **which has no reader** — so args the user
already typed are silently discarded;
`SlashCommandEditor.readOnly` never passed (15+ template bindings);
`HealthPill.label`/`compact` never passed;
`MessageList.scrollToBottom` and `ConfirmToolModal.reconcile` exposed to tests
only; `AttachmentTreePicker.attachmentKind` never passed, so every
library-picked attachment is a system attachment (**hedged** — a system-only
picker may be intended).

### Negative results worth keeping

`artifactsview.Config`, `elicitview.Config`, `chat.Config`, `coreupdate.Config`,
`connectors.SupervisorConfig` and `dispatch.Options` were all diffed and are
**clean**. The builtin tool registration ↔ predicate pairing is covered in
**both** directions by `core/rpc/builtins_wiring_test.go:157-182`, so class C is
not open there. `core/autonomy`'s seven `ResolvedKnobs` fields and `PostureMode`
all reach real branches. `SERVED_STREAM_TOPICS` (13) matches
`core/serve/wsstream.go`'s `passthroughTopics` (13) exactly. No orphan **file**
exists in `lib/`, `composables/`, `shell/` or `components/`. `core/workspace`
is clean. Fleet config bundles **are** signature-verified
(`config_pull.go:288` → `:293`) — which is what makes A1's false ACK a genuine
integrity failure rather than a nothing-was-trusted-anyway shrug.

---

## 7. Cross-reference with `docs/unwired-ledger.md`

### Already recorded — reported here only as context, not as new

`background_task_complete` and the whole background-task subsystem (2026-08-14);
`narrative.RegisterMigrations` / migrations 821/822 (2026-08-18);
`core/tools/monitor` (I11); the harness-self MCP unattachment (2026-08-14 +
2026-08-18 amendment); `core/context/merge` and `core/context/verify` orphan
cluster (2026-08-14); the memory hook-journal read path; `stubPolicy`; the
denial UX gap; `Settings_SetShortcut(s)`; `LocalRuntimeRAMOverrideGB`;
`core/mcp/fixture`, `core/context{,/bundlekind,/snapshot}`,
`core/system/resources`, `core/fswatch` (all on
`scripts/ci/allowlists/i7-orphan-packages.txt`); `cedar.CheckTool`,
`cedar.CheckModel`, `cedar.CheckRecipeAdd`, `cedar.CheckLLMFallback` (I10).

### Ledger entries this sweep proves **incomplete or stale**

1. **`narrative.SetSettingsGate` is recorded as DRAINED (2026-08-13, 01PMGX01
   WP17). It is not.** The gate is wired, but all three of `Enabled()`'s
   consumers are unreachable, so flipping the dial still changes no observable
   behaviour. Re-date the entry and correct `core/rpc/api.go:1437-1460`'s
   24-line comment, which asserts a runtime toggle that takes effect next turn.
2. **The I7 orphan closure of 42 is understated.** `core/trust` and
   `core/trust/internal/algo` are cluster **roots** the fixpoint gap cannot see,
   as are `core/policy`, `core/secrets/cache` and `core/secrets/preflight`. The
   ledger names six invisible orphans; there are at least eleven.
3. **The `cedar.CheckTool` I10 entry's framing "tool dispatch is NOT ungated" is
   incomplete.** The shipped policies and the install-time snippet writer target
   `use_tool`, an action with **no evaluator either** — so there is no per-call
   tool authorization at all, not merely a missing `ActionToolExec` helper.
4. **The ungated-upgrade-snapshot class now has a live instance.** The ledger
   records the class; `v0.64.0` shipped without a snapshot, so the
   `upgrade-path` job is green and covering nothing newer than v0.63.2.
   **Release-ritual action, one command:** `bash
   scripts/ci/upgrade-snapshot.sh v0.64.0` and commit the directory.

### New — no ledger entry exists

Everything else in §2 and §6. Grepping the ledger for each finding's identifying
token returns zero for: `SetCedarEngine`, `NarrativeMetrics`, `WithPubKey`,
`azure-openai`, `custom-openai`, `CloudSyncDenyList`, `client_id`,
`CloseOne`, `use_tool`, `filesystem-full`, `@secret`, `list_secrets`,
`ExposureIndex`, `secret_reference`, `model_invokable`, `WorklistDir`,
`LifecycleHooks`, `PermissionRunnerAdapter`, `SessionRunnerAdapter`,
`AuditRetentionSweeper`, `knobs_default`, `Sessions_SetKnobsDefault`,
`keyboardShortcuts`, `AppendToCache`, `SetHarnessVersion`, `MANIFEST_URL`,
`subscribeEvent`, `setPendingPlan`, `awaiting_user_approval`, `sourceBadge`,
`installed_mcp_servers`, `RecipeRegistry`, `NoopChatRunDispatcher`,
`corpus_overflow_dropped`, `parallel_dispatch`, `ToolSchemas`,
`AttachmentRegistry`, `NewEventLogSink`, `WithPostureMode`, `WithSessionID`,
`MarkApplied`, `RecordEmbedCall`, `AppendContinuation`, `provider_capabilities`,
`agent_graph_node_provenance`.

---

# Closing sweep (2026-08-19) — the remaining coverage gaps

**What this section is.** Two sweeps preceded it: a 16-cluster sweep over
`core/*/` + `frontend/src/{views,components,lib,shell,composables}`, and a
6-agent sweep over `cmd/`, `scripts/ci/` and the frontend root files. Together
they produced **183 confirmed findings**. Each of those agents honestly reported
what it could not reach; those eight gaps were this sweep's entire scope. After
this run the owner intends to **stop looking and start fixing**, so the
*Coverage: final state* subsection below is the last word on what remains
unexamined in the tree.

**Base tree.** Same as the rest of this document: main checkout,
`release/v0.59.0` at `9d9ebbce`. `.claude/worktrees/` and `.worktrees/` excluded
from every search.

**Result.** **120 confirmed module findings** (118 distinct — `AN-01` ≡ `CK-01`,
and `SD-15` ≡ `C2V-21`), plus **24 verifier-found findings** that no module
agent filed, marked **single-pass, unrefuted**. **144 raw / 141 distinct new
findings; 66 user-visible.** Running total across all three sweeps: **327 raw /
324 distinct.**

One **P0** appeared, in a cluster no prior sweep had entered: `CHAT-01`, the
periodic partial flush. Four P1s in the chat/compaction runtime and four more in
the settings and served-mode surfaces.

---

## C.1 The headline: the chat turn loop writes lies into the transcript

The single most consequential find of this sweep is not a dead toggle. It is
`core/rpc/views/agentgraph/chat/partial_flush.go:66`:

```go
_, err := persister.PersistPartial(flushCtx, sessionID, text,
```

`driveRun` starts the flush goroutine **unconditionally** on every production
turn (`chat_runner.go:1222`, interval 10s), `PartialPersister` is always non-nil
in a shipped build (`api.go:4895`), and the production implementation
(`api.go:4898`) calls `mgr.AppendMessage` — an **INSERT** that mints a fresh id
(`session/manager.go:474-486`), then stamps `streaming_failed_at` /
`recoverable=true` onto that brand-new row. `partial_flush.go` passes the *whole
turn accumulation*, not a delta, so each tick writes a longer superset of the
last. Nothing filters these rows out on read: `model_history.go:184-186` takes
the `case "":` arm and hands every one of them back to the model.

The file's own comment at `:12-16` asserts "write amplification acceptable for a
single UPDATE to session_messages". **There is no UPDATE anywhere on this
path.** A 60-second agent turn leaves six duplicate, ever-growing copies of the
same partial answer in the user's transcript, each flagged as a streaming
failure with a Resume affordance nobody triggered, and the model re-reads all
six on the next turn and pays for them. The session grows super-linearly and
never self-repairs.

Blind spot #2 confirmed live: `partial_flush_test.go:14` drives a
`fakePartialPersister` and never the real `AppendMessage`-backed closure, so the
entire suite passes with this present.

A verifier found a second, independent defect one line below it —
`partial_flush.go:68` stamps `true, /* recoverable: no tool_use has executed at
this point */` while `:58` writes `text, _ := bridge.PartialState()`, throwing
away the very `hasTool` bit that claim depends on. The terminal path in the same
package does the opposite (`chat_runner.go:1449 partialRecoverable = !hasTool`),
and `views/sessions/impl.go:195-198` documents `ErrResumeNotRecoverable` as
existing precisely to forbid resuming past an executed tool. So the checkpoint
offers a resume the harness elsewhere refuses.

---

## C.2 Findings by class

### Class A — nil optional dependency (18)

| ID | File:line | Defect | Sev | Disp |
|---|---|---|---|---|
| `AN-01`/`CK-01` | `core/rpc/api.go:6233` | `RegisterStrategy(compaction.NewSummaryStrategy(nil))` — the *only* production registration of the "Summary (LLM)" strategy ships a nil LLM, so every summary is `heuristicSummary`'s 80-char-per-message pipe join (`strategies.go:408-424`). The panel labels it `'Summary (LLM)'` (`CompactionStrategyPanel.vue:317`) and renders a "Summary model" input read only inside the `s.LLM != nil` branch. **Both re-verifiers refuted the prescribed wire**: `compactionwiring.NewLLMCaller` has no `Generate` method and does not satisfy `agentgraph.LLMProvider`; the only production implementation is `chat.LLMProviderAdapter`, constructed per-run. A new adapter must be written. | P1 | wire |
| `AN-02` | `core/rpc/api.go:3451` | `attachments.Manager` built with `WithMediaStore` and no `WithLibrary`, so `attachments.go:434` returns `no library reader wired` for every `library:` attachment. `AttachmentRow.vue:112-120` does not recognise that string, so the user sees a raw internal error instead of the soft Detach affordance. **Correction:** `contexts.Library.Get` takes ONE argument (`library.go:248`); `LibraryReader` requires two — an adapter is required, not a one-line injection. | P1 | wire |
| `AN-03` | `core/rpc/api.go:1333` | `audit.NewAPI` passes 2 of 6 options; `WithSweepableBackend` and `WithEmitter` have zero non-test callers, so `BulkPurge` errors with an internal option name (`impl.go:342`) and neither `KindAuditBulkPurgeExecuted` nor `KindAuditBulkPurgeBlockedByPolicy` emits. **File as an amendment widening `audit-export-backend-never-wired` (:220)** — same constructor, and `WithSweepableBackend` also sets `backend`. | P1 | wire |
| `AN-06` | `core/rpc/api.go:4111` | `stdio.DefaultRoots(dataDir, nil)` — the projectRoot selector is permanently nil, so `roots/list` advertises the harness DataDir (sessions DB, policy bundle, credential locators) and never the agent workspace. The justifying comment ("no current-project concept in v1") is stale: `core.go:496 WorkspaceDir()` exists and the bash sandbox already uses it. | P2 | wire |
| `AN-08` | `core/rpc/api.go:4841` | `NewHookManager` is followed by `SetJournalWriter` and never `SetRedactor`. **No type in the repo implements `agentgraph.Redactor`** except a test fake. `hooks.go:286` documents greedy memory capture ("we write near-everything"), so every tool payload and assistant turn is persisted verbatim. | P2 | escalate |
| `AN-10` | `core/rpc/api.go:2230` | `acppeers.NewRegistry(nil, NoopEmitter{})` — nil secrets backend (every `AuthRef` peer fails credential resolution) and a no-op emitter the constructor doc reserves for tests. **Second site:** `views/acp/acp.go:701` has the identical defect. | P3 | escalate |
| `AN-12` | `core/rpc/api.go:1662` | `corpus.Manager` embedder frozen at boot; `SetEmbedder` — documented as "the chassis calls this once the LLM stack reports a working embedder" — has zero non-test callers, so ingest and search stay `ErrEmbedderUnavailable` for the process lifetime after the user configures a provider. **Verifier refuted the "corpus is retired" escalation**: `api.go:6098` wires `corpusMgr` as the `corpus_read` node's backend and `exec_state.go:106` calls it on the live kernel path. Reclassified **user-visible, P2, wire**. | P2 | wire |
| `AN-13` | `core/rpc/builtins_wiring.go:502` | `corefs.NewGate` sets `Engine` only; `AllowDangerousPersist` has no writer while the sibling bash dial got `PermissionCacheDangerousOps: dangerousOpsCacheLookup(store)` in the 2026-08-14 sweep. One Settings toggle, two answers. **Correction:** the bash field is a `func() bool`, the fs field a plain `bool` — the fs field must become a closure or it is frozen at boot. Fold into `fs-gate-policydir-never-set` (:319). | P3 | wire |
| `CHAT-10` | `.../chat/autotitle.go:63` | `AutoTitleDeps` literal (`api.go:5003`) sets 4 of 5 fields and omits `Audit`, whose own doc says "Production binds this to the manager's own Audit sink" and whose struct header says all fields are required. The two **failure** payloads (`list_messages_failed`, `generate_failed`) have no other emitter anywhere. | P3 | wire |
| `SD-09` (serve) | `core/serve/authbroker/authbroker.go:220` | `WithLedgerEmit` has zero production callers; both served entry points call `NewSession(ctx, cfg, log)` bare (`main.go:454`, `cmd/harness-served/main.go:176`), so the documented `session.signed_out` ledger event never fires — while the emitter it asks for is constructed three lines earlier. **Correction:** `connectors.LedgerEmitter` has no `func(event string)` member; an exported session-lifecycle emit must be added first. Verdict UNCERTAIN. | P2 | wire |
| *+ verifier extras* | | `SetGraphLister`/`SetGraphResolver` never called (below); `config.Store` has zero implementations (below). | | |

### Class B — per-variant gap (11)

| ID | File:line | Defect | Sev | Disp |
|---|---|---|---|---|
| `MO-01` | `core/mcp/oauth/slack_signin.go:116` | The **entire Slack OAuth lane** — static endpoints, fixed-port loopback, `KAMEAS_SLACK_OAUTH_CLIENT_ID` override — has zero production callers. Nine exported Slack symbols dead; they drag `SlackLoopbackPort`, `InteractiveConfig.FixedPort` and `ErrFixedPortInUse` with them. The `slack` recipe has `client_id: ""`, so `SignInRecipe` (`views/tools/oauth.go:163`) rejects it before dispatch and `ResolveSlackClientID`'s env fallback is never consulted. **`registry.json:252` ships the inert env var as a user-visible `warning` string** rendered by the install modal. | P1 | wire |
| `MO-02` | `core/mcp/oauth/register.go:119` | DCR registers `"http://127.0.0.1"` while the grant always sends `http://127.0.0.1:<port>/callback`. RFC 8252 §7.3 relaxes only the **port**; the path must match exactly. `ExtraRedirectURIs` — the only escape — has no non-test setter. **Blocking sub-item of ◆B4**: wiring `SignInWithDCR` without this ships a flow every RFC-conformant provider rejects. The "populate from the port" half is not implementable (registration precedes port selection). | P2 | wire |
| `MO-03` | `core/mcp/oauth/register.go:128` | `TokenEndpointAuthMethod: "none"` hardcoded, so no conforming server issues a secret — and if one did, `SignInWithDCR` reads only `result.ClientID` and neither `InteractiveConfig` nor `ExchangeCode` has a client-secret field. The whole `SecretSaver`/`SecretLoader`/`credstoreKey`/`HasSecret`/`ErrDCRExpired` half of `DCRStore` guards a value that cannot be produced. | P2 | escalate |
| `AN-07` | `core/rpc/branches_wiring.go:61` | `var knownModelProviders = []string{"anthropic", "openai"}` — a gemini/bedrock/azure/openrouter/custom parent can only ever be answered with an Anthropic or OpenAI model plus a cross-provider warning. **Correction:** hydrating from `ListProviders` fixes only bedrock+openrouter; gemini.yaml and ollama.yaml carry no `tiers:` block and azure/custom have no catalog file at all. | P2 | wire |
| `CK-03` | `.../compaction/pipeline.go:271` | The **post_tool site can never fire**: of the three `CompactionInput{}` construction sites, `exec_compute.go:503-511` is the only one that omits `ContextWindow`, so `pipeline.go:273` always skips with `"context window unknown"`. The Settings checkbox that claims to enable it is inert **by construction, not by config**. | P1 | escalate |
| `SD-03` (serve) | `docs/served-mode-boundary.md:19` | The doc names two boundary mechanisms; **ten routed served surfaces have neither** — `/bundles`, `/providers`, `/audit`, `/projects/:id`, `/permissions`, `/agentgraph`, `/agentgraph/edit/:id`, `/agentgraph/run/:runId`, `/agentgraph/run/:runId/graph`, `/policy`. `/agentgraph` is in the LeftRail with no `!served` guard. `entrypoint.routes.test.ts:44` grandfathers all ten behind a two-entry allowlist. | P1 | escalate |
| `SD-04` (serve) | `core/serve/server.go:537` | The dispatch docstring sets the bar — "Half a flow is worse than an honest refusal" — and the chat surface, the sole reason served mode exists, violates it: paperclip → `Attachments_Add`, `/` → `Slash_Execute`, autonomy chip → `Sessions_ResolveAutonomy`, title suggestion, all 10 `Branches_*`, `Config_GetFlags`. None gated on `isServedMode()`. **Correction:** `client.slash.list` has no caller anywhere — drop it from the port list. | P1 | escalate |
| `SD-11` (serve) | `main.go:350` | `runServeMode`'s docstring promises SIGTERM/SIGINT shutdown; **main.go does not import `os/signal` at all**. The sibling `cmd/harness-served/main.go` does. `defer cancel()` never runs, `srv.Shutdown` never runs, in-flight streams drop. | P2 | wire |
| `SD-17` (serve) | `.../chat/SessionHeader.vue:114` | `Sessions_ResolveAutonomy` is in the gap, so the plan-mode badge seed rejects and the comment's escape hatch ("live events correct it") does not hold. **Overlap:** `:664-667` of this document already records that no producer emits a plan-mode event at all, so the badge is hidden on desktop too — land these together or the port lands and the badge still never shows. | P2 | wire |
| `MO-13`* | `views/tools/device_auth.go:68` | Docstring asserts a two-part precondition (`PrimaryAuth == "device_code"` **and** `Auth.Kind == "mcp_oauth"`); the body enforces only the second, then hardcodes `oauth.GitHubDeviceConfig`. Latent: 7 device_code recipes exist but only github has an auth block. The hole opens the moment a Microsoft recipe gains one — its client_id would be POSTed to GitHub. | P3 | wire |

`*` verifier-found.

### Class C — registered, not consumed (43)

The largest bucket, and it splits cleanly in two.

**C-i — backend live, UI missing (wire).** The strongest signal someone meant
it, and the cheapest class of fix:

- `C2V-01` `harnessClient.ts:3362` `Handoff_Accept` — the inbox badge renders `{{ inboxItems.length }} shared` with **no click handler, no row list, no accept button**. The entire receiving half of session handoff is unreachable. **Prerequisite the finder missed:** `unwired-ledger.md:323` already records that `Handoff_Share` sends a nil payload, so wiring Accept alone opens an *empty* session.
- `C2V-09` `triggerManualCompaction` — there is no "compact now" anywhere in the app; the panel already calls five sibling methods.
- `C2V-10` `resummarizeChunk` (docstring cites FR-004), `C2V-11` `narrativeMetricsForChunk` — both in a mounted MemoryView that already calls nineteen siblings.
- `C2V-12` `contexts.rename`/`delete` — every Context Library file is permanent and permanently named. `ContextTree.vue:8` defers it in prose with no date and no owner.
- `C2V-13` `contexts.promote`, `C2V-14` `contextBootstrap.resume`, `C2V-07` `SessionSync_DeleteRemote`/`ProjectSync_DeleteRemote` (sync off leaves everything already uploaded, with no in-app purge).
- `C2V-03` per-class fleet telemetry opt-ins — a **consent** surface gating a live export fence (`otlp_pipeline.go:604`) with no UI. **Correction:** the host surface is `FleetTelemetryPanel.vue`, not the onboarding modal; it renders three radio tiers and no per-class control.
- `C2V-04` `getFSRequestAccessEnabled` and *(verifier)* `getMaxAgentTurns` — complete RPC surfaces, typed clients, fake stubs, and no `.vue` caller. Both gate real branches.
- `SD-06` (serve) `Connectors_List`/`Connectors_Status` — 117 lines of projection joining supervisor boot outcomes with live MCP health, fully served, **zero frontend consumers**; the only connector UI is boundary-panelled by design.
- `CK-08` `core/rpc/api.go:3932` `compactionLLM`/`compactionAudit` are stored and never read, so `Overhead()` and `Recent()` have no caller and the 256-entry audit ring accumulates for nobody. This is the **producer half** of `compaction-overhead-row-writerless` (:758) — neither half had been traced to the other. **Correction:** the fields live on `llmStack`, not `HarnessAPI`.

**C-ii — a named live substitute exists (delete).** `C2V-21`/`SD-15`
`getBashAllowlistMigrated` (substitute: `getPermissionsMigrationToastShown`,
which `MigrationToast.vue:23` actually calls); `C2V-23` `recordProgress` (the Go
impl already calls it internally at five sites); `C2V-24` `catalog.installed`
(the list rows carry the flag); `C2V-25` `sites.status`; `C2V-26` `verifyEntry`
(**but `filter` → escalate**: `Audit_ListEntries` and `Audit_Filter` take
*different types* and the saved-query feature is built on the richer one);
`C2V-27` `hooks.get`; `C2V-28` `branches.status`/`listWithBranchTree`; `C2V-29`
`writeSnippet`/`revokeSnippet` (**scope the delete to the client methods and
bindings — `WritePolicySnippet` is what `bash/migrate.go:126` calls**);
`C2V-31` `sessions.getAutonomy`; `C2V-32` `requestAdditionalAllowedDir` (the
binding's own comment concedes no consumer exists); `C2V-33`
`getMonthlyCostNotifyUSD`; `C2V-35` `Tasks_AbortBySession`/`ListBySession`;
`AN-11` `a2aAPI`/`workflowAPI`/`trustAPI`/`contextAPI` permanent `&stub{}`s.

> **⚠ `AN-11` is a re-report.** The calibration auditor found these same five
> stub fields inventoried at `docs/dead-code-audit-2026-08-16.md:463-469`, which
> ruled **"escalate, then delete"** and warned in terms that *"a2a (agent cards)
> and trust (secret references) are plausibly wanted, and deleting the consumer
> half of a wanted feature is the wrong call."* `AN-11` prescribes a flat delete
> and overturns that recorded ruling without arguing against it. **The 08-16
> ruling stands.** See §C.5.

**Also in this class:** `CK-02` — three strategies (`semantic_cluster`,
`custom_subgraph`, `narrative_first`) are **never registered**, while the
Settings panel offers the first two as savable options; `pipeline.go:239` then
returns `ErrUnknownStrategy` *before any strategy runs*, and
`exec_compute.go:156` turns that into a hard turn failure. Corrections that
change the work: the `custom_subgraph` half is **impossible, not deferred** —
nothing in the tree implements `compaction.KernelRunner` (`RunGraph` has one
call site and two test fakes); `narrative_first` is not in the panel's list, so
it is an orphan constructor, not a user-selectable brick; and a **fourth**
strategy, `session_rewrite`, is bound only onto a per-run `Bind()` clone
(`session_compaction.go:304`) while the RPC surface holds the base pipeline — so
**every `Compaction_TriggerManual` call without an explicit override returns
`unknown strategy: "session_rewrite"`**.

`CHAT-06` — the generated-image capture pipeline (settings dial, byte cap,
feature flag naming DALL·E 3 / gpt-image-1 / Titan Image, artifact sink, chat
hook) has **no producer**: zero adapters ever construct a
`StreamGeneratedImage` event.

`SD-05`/`SD-07`/`SD-08` (serve) — `Auth_State` is dispatched "for the served
frontend" that never calls it (zero hits in the whole frontend tree);
`Session.NotifyOn401` and `ConnectorTokens.Invalidate` both have zero callers,
so a revoked token keeps being presented upstream for up to ~55 minutes with no
fast-renewal path.

`SD-12` (serve) — the drift gate itself: `check-serve-dispatch-drift.sh:79` uses
`comm -23`, one direction only, and has **no allowlist**, so
`SERVE_DRIFT_GATE=0` can only be flipped by triaging all 417 entries at once.
It runs in CI and is permanently informational. **Gate-extension rule applies.**
*(Nuance: `methods_test.go`'s `TestServedMethodsMatchDispatchSwitch` does pin
servedMethods ↔ the dispatch switch; what is unguarded is bindings ↔ dispatch
reverse, and servedMethods ↔ the 30 client overlays.)*

### Class D — inert dial (24)

- `AN-04` `core/rpc/api.go:1296` — the process-singleton `cedar.NewRegistry` passes `WithDispatcher` only. `WithPosture` and `SetPosture` have **zero non-test callers**, so `prompt.go:838`'s auto-allow fast path and `:847`'s always-prompt path are both frozen at `PostureDefault`. The field's own doc says it is driven by the session's resolved autonomy tier. **Autonomy level has no effect on prompting at all.** Distinct from the pinned `confirm_each` gap and from `cedar.WithPostureMode`.
- `CHAT-05` — **no production path can produce a `confirm_each` verdict.** The only permission source is a static resolver reading `<DataDir>/mcp_servers.json`, and **nothing in the repo writes that file** — no RPC, no Vue surface, no installer seed. So the six-rung confirm ladder, `ConfirmBus.Pending`, session/persistent grants, both audit kinds, `ConfirmToolModal.vue`, and the *only* consumer of `AutoApproveFamilies` and `DestructiveActionPosture` are all unreachable. Two of the seven autonomy knobs change nothing on any install. **This directly contradicts the negative result at :1221 — that entry is retracted.** The knob-coverage guard passes because `knobcoverage.Register` proves a registration string exists, not that its consumer is reachable.
- `CHAT-07` — a `compact` node's `strategy` attr never reaches the compactor: `coreag.CompactionInput` has **no strategy field**, `Pipeline.Compact` leaves `Override` unset, and `Run` falls to `siteCfg.Strategy`. The attr is read only into two event payloads, so the emitted `CompactionApplied` event **reports the strategy that did not run**. `MaxTokens`, `CustomSubgraphId`, `SystemPrompt`, `Temperature` and `ToolAllowlist` reach nothing but a log field.
- `CHAT-08` — `LLMRequest.Provider` and `.Model` are dropped by the only production `LLMProvider`; `llm_provider_adapter.go:522-523` substitutes the session's own values. **The verifier widened this substantially:** four more executors build their own requests and are equally dropped — reflect (`:920`), review (`:1009`), planner (`:1317`) and **escalate (`:1510` `Model: a.TargetModel`)**. `chat_default.yaml:267` ships an `escalation_ladder` node and commit `602e3bff` was written specifically to ground these calls. **A ladder whose rungs all land on the session's own model reports an escalation that did not happen.**
- `CHAT-03` — the overflow redrive runs on `context.Background()` (`chat_runner.go:1916`), so `StopStream`'s `sub.cancel()` never reaches it and `<-sub.done` blocks until it finishes. **The Stop button does nothing during a full agent redrive**, and the RPC hangs.
- `SD-01` (settings) — `SetAuditSettings` is documented "persists the audit retention policy" and writes only a struct field; nothing calls `SaveAll`. Separately `event/log.RetentionSweep` — the one function implementing keep_forever / delete_after / archive_after — has **zero non-test callers**, while `AuditSettingsPanel.vue:67-68` tells the user events are "permanently deleted during the nightly sweep". **Disposition split (verifier):** wire the persisted field (small, real); **escalate the sweep to ★A4 (:206)** — there is no production `SweepableBackend`, no production `log.Store` and no production emitter, and `api.go:4082-4085` says so.
- `SD-02` (settings) — `FirstRunOnboardingCompleted` never suppresses the dialog: `IsFirstRun`'s entire body is `return len(providers) == 0, nil`. A user who explicitly dismisses onboarding sees it again on every cold start, forever. One-line gate.
- `SD-03`/`SD-04`/`SD-05` (settings) — the sidebar branch-depth dial is hardcoded `ref(5)` under a comment saying "read from settings if available", and the Settings help text describes a `"+N more depths"` affordance that does not exist (the prop only clamps indentation pixels); `AutoCollapseBranchesInSidebar` reaches no collapse logic; `DeleteBranchesWithParent` has no reader and its cascade implementation `DeleteChildrenOf` has no caller.
- `SD-08`/`SD-09`/`SD-10` (settings) — `Settings.Accent` is inert **and is nonetheless fleet-synced across the user's devices**; `WindowSize` is seeded to 1280×800 in three places, displayed back in Settings → About, and never read from or written to the real window; `BranchAdvisorDefaultModel` has neither reader nor writer while its doc asserts a chained `CompactionModel` fallback.
- `SD-10` (serve) — `Sessions_Stream` **ignores its params**: `handleWS` never reads `req.Params`, `streamSessions` takes no session id and subscribes to every topic globally, while the client sends `params: { id }` and re-filters in JS. Every WS client of a served instance receives `llm:stream-chunk` — raw model output — for **every session in that harness**.
- `CK-04`/`CK-05`/`CK-06` — `SiteConfig.ToolResultMaxBytes` has a full RPC round trip and **not one branching read** (it is not in `CompactOpts`, so it cannot reach a strategy) while two doc-comments assert it is the post_tool threshold; `PresetForTier` writes a `PreCallThreshold` onto `SiteManual`, which `pipeline.go:266` explicitly exempts, and the panel displays it with layer attribution; `Pipeline.now`/`WithClock`/`CompactOpts.Now` are assigned, cloned and **never called** — `Event` has no timestamp field for a clock to stamp.
- `SD-15` (serve) — `StreamTruncatedPayload.Reason` is "stable, machine-readable copy for the UI to key on" with one hardcoded value and no consumer branch.
- `MO-05`/`MO-08`/`MO-12` — `ResolveClientIDConfig.Now` is computed and never invoked (the expiry check it claims to override lives on `DCRStore`'s own hardwired clock); `AuthorizationHeader` hardcodes `"Bearer "` under a docstring saying "TokenType defaults to Bearer when unset" — `TokenType` is written on every mint path and read nowhere, so a DPoP/MAC provider would 401 with no diagnostic; `CodeChallengeMethodsSupported`, `GrantTypesSupported` **and `ScopesSupported`** are decoded from every live RFC 8414 document into a dead end while PKCE method and grant type are fixed.
- *(verifier)* `core/agentgraph/validator.go:34-36` — three compaction graph dials (`pre_call_threshold`, `tool_result_max_bytes`, `compaction_recursion_max`) are validated and consumed by nothing; the dials subsystem they fed was deleted on 2026-08-14 as rival infra. `:31`'s `compaction_aggressiveness` enum (`gentle`/`default`/`aggressive`) has drifted off the real five-tier dial (`off`/`conservative`/`balanced`/`aggressive`/`maximal`) — **a graph authored with the product's own default would be rejected.**
- *(verifier)* `core/config/config.go:29` `IsHooksV2Enabled` — `config.Store` has **zero implementations repo-wide**, `c.Config` is nil in every production build, so `FlagHooksV2Enabled` can never be false and no config Document is ever loaded. This subsumes `BT-09` and is larger than any of `BT-03`–`BT-12`.

### Class E — frontend placeholder / dead code inside live files (7)

- `CHAT-09` `frontend/src/lib/useSession.ts:604` — `// tool / reasoning / usage frames not yet rendered.` Reasoning deltas are translated by both bridges, populated onto the wire, fanned onto `llm:stream-chunk`, and **dropped at the switch default one line short of the screen**. With extended thinking on — the dial wired for this release — the user pays reasoning tokens and sees nothing, live or on reload; `flattenContent` also skips non-text blocks, so it never reaches persistence either. **Narrow to reasoning:** usage reaches the frontend on `TopicSessionUsageUpdated` and the context-window indicator reads it.
- `C2V-34` `frontend/src/lib/types.ts:3397` `isAutonomyLayerEmpty` — a repo-wide grep returns exactly one line, the definition. Its sibling `emptyAutonomyLayer()` *is* consumed. Blind-spot #1 shape, in one of the three files the previous frontend sweep's entire byte saving came from.
- `C2V-15` `nodes.reloadOverrides`/`listUserOverrides`/`doctor` — the docstring names "the NodesView debug panel (WP08)"; `find frontend/src -name 'NodesView*'` returns nothing. A user who drops a custom node YAML has no way to reload it or see whether it parsed.
- `C2V-22` `getHandoffHint` — "The account step reads this to pre-fill the email field." There is no account step.
- `SD-13` (serve) — `served-mode-boundary.md:123` and `featureFlags.ts:143` both cite a **33-entry** allowlist that has **34** entries, and the doc's argument (the fence needs no per-surface knowledge) is silent on the fact that the fence closes *fleet gates only* — every one of `SD-01`…`SD-04` is a non-fleet RPC it cannot see.
- *(verifier)* `AuditView.vue:179-180` — loading a saved query keeps only element `[0]` of `kinds` and `actor_ids`, and the save path re-narrows to one element. A user who saves a two-kind query, reloads it and re-saves has **silently lost a filter term** while the UI reports the query as restored.
- *(verifier)* `ChatInput.vue:355` — `getMultimodalInput` is unported in served mode, so the catch keeps `ref(true)`: an operator who turned multimodal input **off** still gets the paperclip, and every attach then dies at the unported `Attachments_Add`. `SD-04` asserted the paperclip is ungated; it is gated, on a dial structurally stuck open.

### Class F — manufactured success (10)

Ranked first regardless of severity label, per the convention this document set.

1. **`CHAT-01`** — §C.1 above. **P0.**
2. **`CHAT-02`** `chat_runner.go:1410` — the interrupt path writes a synthetic `"cancelled: interrupted by user"` `is_error` tool_result for **every tool the turn ever called**, because `SeenToolCalls` only ever appends and nothing removes an entry when a call returns. Press Stop after three successful tools and the transcript shows three red error chips over completed work — and each `tool_use_id` now carries **two** tool_result rows, which is the duplicate shape OpenAI-compatible providers 400 on. The exact API-validity failure FR-001 exists to prevent. `TestPersistInterrupt_APIValid` hand-builds `DanglingToolCalls` and never exercises the producer.
3. **`BT-01`** `BundlesView.vue:182` — `{{ b.signature ? 'Signed' : 'Unsigned' }}` painted in `text-signal-ok`. `Signature` is copied verbatim from the manifest at install; `manifest.Validate` checks only that a kind string is one of two literals and a locator is non-blank. **`core/bundle/integrity` has zero importers repo-wide.** *(Calibration note: the only install kind is a local path the user types, so the lie is real but the exploit path is not remote — P2 is the fairer rank.)*
4. *(verifier)* `views/bundle/impl.go:288` — `ContentHash: ad.ContentHash` — Install copies each artifact's **declared** hash out of `kenaz.yaml` into the lockfile having read exactly one file, the manifest itself. `ErrIntegrityMismatch` is produced at one site, `cache.CAS.Put`, which has zero production callers. **Two false integrity signals on the same table row.**
5. **`BT-02`** `core/trust/bundleadapter.go:96` — `Signature: nil, // engine reads sig math from algo registry`. It does not; `runVerify` passes them in. `ValidateEnvelopeShape` rejects on `len(env.Signature) == 0` before any anchor or key is consulted, so **`EngineVerifier` — the only concrete `trust.Verifier` — can never return `OK=true` for any input.** Wiring it to `VerifyManifestSignatures` as-is converts "every signed bundle silently trusted" into "every signed bundle refused". `bundleadapter_test.go:49-92` **pins** this rather than catching it.
6. **`BT-03`** `core/trust/config.go:22` — `runPreflight` ignores all three parameters and returns an unconditional all-clear. `trust.go:61-63` says "Preflight is invoked by core/config/ at startup (FR-014)"; `core/config` does not import `core/trust`. Five `PreflightCode*` constants have zero producers; `SeverityError` is documented to block startup and can never be produced.
7. **`SD-01`** (serve) `PermissionDialsPanel.vue:103` — `permissionMode.value = 'normal';` in the catch. `/permissions` is routed in served mode, has no boundary panel, and is reachable from an ungated ⌘K entry, so the catch fires on **every** served load. The write path is honest (explicit error banner); only the read lies. *(Downgraded P1→P2: `unwired-ledger.md:756-761` already records `PermissionMode` as inert, so there is no enforced posture being concealed — and `:26` already defaults to `'normal'`, so removing the catch alone fixes nothing.)*
8. **`SD-02`** (serve) `AuditView.vue:80` — `seeded.value = [];` in a bare `catch {}`. All 11 `Audit_*` bindings are in the gap, `/audit` is routed with no boundary panel and an ungated palette entry. **A served user opening the audit log to check what the agent did sees a clean, empty, non-erroring audit trail.** The most severe form of the class: fabricated evidence about a compliance record.
9. **`AN-09`** `core/rpc/harness_wiring.go:209` — all five keys of `settingsKeyToJSON` map to `_`-prefixed sentinels that `json.Unmarshal` silently discards, and the writer returns nil. A 5-of-5 miss against `harness.SettingsAllowlist`. **Latent today** (harness-self is attached to nothing) — but the attach was ruled on 2026-08-18 and its checklist does not mention this, so the moment it lands, an agent calling `harness_write_set_setting` gets a success response for a write that did nothing.
10. *(verifier)* `partial_flush.go:68` — the falsified `recoverable` flag. §C.1.

### Class "other" — comments asserting invariants nothing enforces (17)

`CHAT-11` (a KNOWN GAP block instructing the reader that `AutonomyKnobs` has no
production call site — it was wired at `api.go:5125` and the comment was not
updated, and it *masks* `CHAT-05`, which is the real reason the prompt-skip set
never suppresses anything); `CHAT-12` `AnswerInjector` (a declared seam type
with **zero references anywhere**, under a doc claiming the chassis wires it to
the manager's askRouter); `CHAT-13` `StreamSink.Close` (the interface doc
promises a chassis-fanned stream-finish payload; the kernel never calls it, and
because `Close` hardcodes `Reason: "completed"` and shares the `closed` flag,
any future kernel honouring the contract would stamp "completed" over a failed
turn); `CHAT-14`/`CK-14` `ContextSlice.SystemPrompt` ("strategies must include
it untouched" — four of five drop it, and nothing re-attaches it); `MO-06` (the
Slack no-challenge fallback sits **after** an unconditional return on the same
error, so the graceful-degradation branch can never execute; plus a second dead
`len(scopes) == 0` branch); `MO-07` (`DiscoverAuthServer`'s GitHub special-case
branch body is **character-identical to its initialiser**); `MO-11` (`
DefaultDCRStorePath` — the canonical on-disk location is named, documented and
tested and no code path can produce the file); `SD-14` (settings) (two struct
comments claim `Save` rejects negative values; neither field is reachable from
any of `SaveAll`'s four validators — **and the second field is already ledgered
as fully inert, so validating it is cosmetic work on a dead field**); `SD-13`
(settings) `SchemaVersion` (the one config store in the tree with a version
field and no versioning code, which matters given blind spot #3); `CK-13`
(narrative_first's three comments describe a pipeline cascade, a site-exclusion
rule and a `HARNESS_MEMORY_NARRATIVE_COMPACT_SHADOW` shadow mode — the pipeline
doc says the opposite in words, no SiteConfig excludes a strategy, and
`NarrativeCompactShadowMode()` has zero callers); `CK-15` (`WithCustomTable`/
`SetTable` documented as "a legitimate escape hatch" for a model the YAML
catalog does not know, with **no configuration path, RPC or settings field that
reaches either** — so an operator gets `(0,false)` from `MaxContextTokens`, the
engine skips its pre-flight cap check, and the problem surfaces as a provider
`context_length_exceeded` mid-compaction); `BT-07` (`harness bundle schema` —
no such CLI); `BT-10` (five `core/bundle` sentinels with no producer, under a
docstring citing `harness bundle why` / `harness bundle status`); `BT-12`
(`ResolveConflicts` renders a `kenaz lock --resolve-conflicts` CLI transcript
for a binary that does not exist; **correction: `UniversalMarker` is a
deliberate documented seam and should be dropped from this finding**);
`SD-16` (serve) (`WithStreamQueueCap`'s second documented purpose — "cap
per-client memory in a very constrained workbench" — has no path from any
configuration surface); *(verifier)* `views/bundle/impl.go:2` ("Read-only by
construction" in a file whose `Install` and `Remove` both mutate
`<dataDir>/kenaz.lock`); *(verifier)* `wiring/llm.go:207` (`cost.KindCompaction`
is tagged onto a **debug log line** under a doc claiming "downstream surfaces
can recognize compaction overhead" — no aggregator, RPC or view reads it, which
compounds `CK-08`).

### Orphan symbols and structural dead ends (14)

`CK-09` `SweepScheduler.SeedLastRun` — never called, so `lastRun` is zero on
every boot and the documented "once per day" sweep is really **every app
launch**, plus `Stop()` is never invoked; `CK-10` `ErrCompactionDuringToolPair`
(documented as a `SessionEngine.Compact` return value, returned by nothing, and
— contrary to its own comment — read by **no test either**); `CK-11`
`SortedSites` (doc names users that do not exist; both resolvers iterate
`AllSites()`); `MO-04` `DCRStore.Delete` (the 401-invalidation recovery its
docstring names is not implemented anywhere — and cannot be until `postToken`
grows an `ErrInvalidClient` sentinel, which the finding does not say);
`MO-09` `Tokens.Expired` (test-only; `StoredCredential.expired` is the live
line-for-line twin); `MO-10` `RegisteredClient.SecretExpired` (`DCRStore.Load`
reimplements the same rule inline — **but on a different receiver type, so
"live substitute" is loose; the delete rests on the no-caller proof alone, and
must wait for `MO-03`'s escalation**); *(verifier)* `LoadedClient` — a
**one-hit-in-the-entire-repository phantom**: `dcr_store.go:164` documents a
return type that does not exist; *(verifier)* `resolve.go:49 FromDCR` (written
twice, read only in tests, doc says "so callers can track the source for
debugging" and nothing logs or emits it); `SD-14` (serve) `frameFor`'s
`forward` flag (all three returns are `true`, so `if !forward { continue }` is
unreachable — **and the disposition must pick one class: delete, not "or leave
it as reserved"**); `BT-05` (`GC`, `EvictPolicy` and the entire `ManifestCAS`
are declared on the unexported `*fsCAS`, which is never returned concretely and
never type-asserted, so they are **structurally uncallable from outside the
package, in production and tests alike**; and of the five interface methods only
`Has` has a production caller — nothing has ever written a byte to the CAS the
package doc calls "the long-lived store of every artifact byte"); `BT-08`
`AllowEmptyArtifacts` (three hits total, all inside `validate.go`; the
"scaffolding tools and tests" the comment justifies it with do not exist);
`BT-11` (five `BackendKind` constants, one `SigningBackend`, split by build tag
so production compiles a deliberately-empty `Register` — **`TrustEngine.Sign`
can never succeed in a shipped binary**; *calibration: this is deliberate,
documented and fail-closed and nothing lies, so rank it informational — the
defensible core is that `BackendYubiKey` and `BackendPKCS11` have zero
references anywhere including tests*); *(verifier)*
`compaction/yaml_resolver.go:53 NewYAMLResolver` (test-only; production uses
`NewYAMLResolverWithDefaults`, and `SafeDefaults()`'s path never runs in the
shipped app); *(verifier)* `views/compaction/impl.go:36`
`SetGraphLister`/`SetGraphResolver` — **never called in production**, so
`ListCustomStrategies` returns `[]` and `ErrCustomGraphUnavailable` is
guaranteed, while the Settings panel offers `custom_subgraph` as a first-class
choice and `harnessClient.ts:3850` is a live binding. The source they need,
`a.graphMgr`, is constructed seven lines earlier.

### The one that will bite whoever fixes the compaction panel

*(verifier)* `core/rpc/api.go:6024` — `liveDialResolver.syncTier` calls
`r.Resolver.Set(compaction.LayerGlobal, "", compaction.PresetForTier(t))`, and
its own doc at `:6001` says **"The first call always writes"**. `YAMLResolver.Set`
*replaces* the layer and flushes to `<DataDir>/config/compaction.yaml`. The
Settings panel writes the global layer through the same resolver. So on every
launch the user's persisted per-site strategy, threshold, keep-N and enabled
flags are read back from disk and then **silently overwritten wholesale by the
tier preset** the first time anything resolves — which the panel itself
triggers via `GetEffective`. The precedence note at `:5975-5980` acknowledges
last-writer-wins between two controls over one layer; it does not acknowledge
that one of them fires unconditionally at boot.

---

## C.3 Coverage: final state

The eight gaps the prior sweeps declared, and where each stands now.

| # | Gap | State | What remains |
|---|---|---|---|
| 1 | **`core/rpc/api.go` non-literal wiring** (`api.go`, `builtins_wiring.go`, `onboarding_wiring.go`, `branches_wiring.go`) | **Named shapes exhausted; the file is not** | All 56 `With*` call sites, 32 `.Set*` call sites and 92 `a.<field> =` assignments were resolved **in both directions** against 170 exported option constructors, 119 setter definitions and all 99 `API` struct fields — plus a fourth shape nobody asked for (nil/no-op args to non-literal constructors), which produced 3 of 12 findings. **Unexamined:** (a) the ~4,000-line *body* of `New()` for control-flow/ordering hazards — the already-known nil `cedarPolicyAPI` (task #32 F3) is that class and was found only by accident; (b) the ~90 free functions outside `New()`, several taking 15–22 positional params, none walked internally; (c) served-mode divergence — `shell.New(nil)` under `//go:build serve` leaves a nil opener and the file's comment calls it "a no-op" when `OpenInOSBrowser` errors; unresolved, needs a served build. **A verifier proved the setter-definition scoring itself had holes** and produced a 13-name zero-caller setter list, 7 of which (`SetAnswer`, `SetClock`, `SetIdleTimeout`, `SetJournalIDGen`, `SetNextID`, `SetTable`, `SetTerminalTokens`) got no 3-pass and are **not reported** — that corner is explicitly unexamined. |
| 2 | **Chat runtime** (`core/rpc/views/agentgraph/chat/**`) | **File-level exhausted** | All 13 non-test files (5,906 lines) read end to end; every exported symbol, all 38 `Config`/`Deps` fields, all 22 `LLMRequest` fields, both 8-member `StreamEventKind` enums (emitter→reader, both directions), all 7 `ResolvedKnobs` fields and all 10 `CompactAttrs` fields individually traced. **Unexamined and high-value:** the **20 test files (~10,400 lines) were not audited for blind spot #2**. Two of the three opened were exactly that pattern (both hand-built the producer's output), so the base rate is high; `chat_runner_integration_test.go` (1,300L), `moves_test.go` (1,334L) and `wire_integration_test.go` (1,108L) are the next pass. **Blind spot #3 was not applied at all** — `CHAT-01` writes persisting rows and nobody checked how a session already carrying them behaves under compaction, export, search or move-fidelity composition. |
| 3 | **Settings dials** (`core/rpc/views/settings/**` + the settings arm of `harnessClient.ts`) | **Class D exhausted for the `Settings` struct** | All 79 struct fields now carry a written verdict (15 filed, 15 already in the ledger, 4 in this doc, ~46 traced to a real branch). 41 accessors and ~226 symbols machine-enumerated and reader-counted with a camelCase-JSON-tag second pass — which rescued **ten** near-misses a Go-only grep would have mis-filed. **Unexamined:** (a) `fleet.go` (869 lines) — the fleet config-**apply** path was never traced field-by-field against the bundle schema; `SD-08` proves at least one pushed key is inert, so more are expected, and that needs an enumeration driven from the bundle schema, not the Settings struct; (b) **upgrade-path behaviour of `settings.json`** — every trace was against the current struct shape and no test in the tree starts from a prior release's file; (c) the 73 `SettingsStore` and 31 `SettingsAPI` interface methods were only **sampled** where a finding led there — and `SD-06`/`SD-07` both live in exactly that layer, so it is the most likely remaining seam. |
| 4 | **MCP OAuth** (`core/mcp/oauth/**`) | **EXHAUSTED** | Closed-world proof: the package has exactly **two** production importers, so the consumer set is enumerable rather than sampled. All 74 exported symbols + 81 struct fields classified; all 1,672 non-test lines read whole (which is what surfaced the two dead branches no reader-count method can see). **Residue, both bounded:** (a) runtime correctness of the two live flows was never exercised — `device_auth.go:119-124`'s standing "TODO (live verify)" that GitHub's `ghu_` token is accepted as a Bearer needs a human in the app; (b) no upgrade-path check on the persisted keychain `StoredCredential` blob — the struct looks additive-only, but there is no oauth fixture under `testdata/upgrade/`, so it is **unexamined, not cleared**. |
| 5 | **Compaction knobs** (`core/agentgraph/compaction/**` incl. `wiring/`) | **Knob surface exhausted; the persisted-history half is not** | All 17 non-test files read whole, all 222 top-level declarations enumerated and reader-checked, all 38 fields of the four knob-carrying structs traced past the copy into a behaviour-changing branch. The strategy-registration set is **closed** (2 `RegisterStrategy` + 1 `Bind`) and the 3 `CompactionInput{}` producers enumerated, not sampled. **Unexamined:** (a) `session_snap.go` bodies — a *correctness* defect inside the three snap helpers would have escaped, and blind spot #2 applies with force (the WP06 tool-pair clamp no-op hid exactly there; the only SQL-path coverage is `wiring/pairing_sql_test.go`); (b) `wiring/store.go`'s `ApplyCompaction` SQL body — it flips `archived_at`/`compacted_into_id` on existing rows, **the exact shape that silently emptied `artifact_versions` on upgrade before**, and no blind-spot-#3 reasoning was applied; (c) `compactionpolicy`'s five-tier numeric table was taken as ground truth and never diffed against `GetTierExplain`'s user-facing copy. |
| 6 | **Served mode** (`core/serve/**` + `main-served.ts`) | **Structurally exhausted; per-entry triage is not** | The 417-entry dispatch gap fully enumerated and grouped by all 62 prefixes; all 46 exported declarations reader-counted outside the package; all 20 served routes triaged; and the **both-directions class-C diff nobody had ever run** (servedMethods ↔ the 30 client overlays) executed, producing `SD-05` and `SD-06`. **Unexamined:** (a) per-**method** triage of the ~400 remaining gap entries — they were closed as a *class* ("its only callers live in a boundary-panelled or unrouted view"), which is sound per-view but not proven per-method against shared components and composables; the allowlist `SD-12` asks for is the artifact that closes this properly; (b) the 3,112 lines of `core/serve/*_test.go` were not audited for blind spot #2 — `newChatHarness` in particular was seen and never opened; (c) **nothing was run** — "the served client rejects" rests on reading `createUnsupportedServedClient`, and one specific hole survives: `wrapValue` only replaces functions and recurses into plain objects, so a **non-function, non-object property on the fake client would pass through to the served client verbatim as fake data**, and the fake's non-function fields were never enumerated. That is a bounded, checkable class-F follow-up. |
| 7 | **Client → Vue** (`harnessClient.ts` → `.vue`) | **Forward direction EXHAUSTED** | All 392 client methods brace-depth-parsed and diffed against a caller index over all 281 non-test `.ts`/`.vue` files, with whitespace-normalised chain matching; every zero-hit re-checked two further ways; and the escape hatches **closed by proof** — `= client.<x>`, destructuring, and `client[` all return zero tree-wide. Two false positives from the naive pass were retracted rather than filed. **Unexamined:** (a) the **served-mode variant of this same surface** — `createServedHarnessClient`'s overlay was never enumerated, so which of the 392 are reachable in a served build is unknown; this is a genuine class-B surface no sweep has walked; (b) `WailsBindingsLike`'s 408 declarations in the **binding→client** direction — the 08-18 audit's diff was "name present somewhere in `frontend/src`", which the mirror itself satisfies for every binding, so a declaration never referenced in the `createHarnessClient` body passes both checks; a mechanical 408-vs-403 diff would close it in one pass; (c) ~330 type-only exports in `types.ts`, deliberately not filed (unreferenced-outside-file ≠ dead for a structural type). One prior prediction corrected: `Unit_*` was **wrong** — all four are consumed by `UnitConflictsPanel.vue`. |
| 8 | **Bundle + trust** (`core/bundle/**`, `core/trust/**`) | **EXHAUSTED** | All 52 non-test files read whole; all 343 exported symbols and struct fields cross-package reader-checked **by qualified name** (bare-identifier grep over-reported ~10× and was discarded); both live entry points traced end to end to a pixel or a proven dead end. Confirmed: `kinds`, `channels`, `channels/localpath`, `events`, `integrity`, `core/trust`, `trust/backends`, `trust/backends/software`, `trust/internal/algo` and `trust/internal/fingerprint` have **zero** cross-package production readers. **Unexamined:** (a) `schema/kenaz.yaml.schema.json` was confirmed readerless but **never diffed against the hand-rolled Go validator** — two independent definitions of one contract with no conformance test, invisible to this pass *and* to CI; (b) `kinds/testkind/noop.go` was read for reader-detection only. The YAML leg of 3-pass detection does not apply here (no bundle symbol is a node kind, activity or attr) and the frontend leg was run directly instead. |

**Two structural gaps that survive all three sweeps and belong to no cluster:**

1. **Upgrade-path reasoning (blind spot #3) was applied by nobody.** Six of the
   eight clusters say so explicitly. Two findings sit directly on it —
   `CHAT-01` persists rows and `wiring/store.go`'s `ApplyCompaction` mutates
   existing ones — and neither was checked against a database a previous
   release produced. Per the release-ritual corollary, `v0.64.0` also still
   ships without a snapshot (recorded at §7 item 4), so the `upgrade-path` job
   is green and covering nothing newer than v0.63.2.
2. **Test fixtures (blind spot #2) were audited in one cluster and skipped in
   the other seven.** In the one that looked, two of three fixtures opened were
   the bypass pattern, and both hid a filed finding (`CHAT-01`, `CHAT-02`). The
   base rate is high enough that a dedicated fixture-audit pass is the single
   highest-value thing left in the tree.

---

## C.4 Calibration audit — reproduced verbatim

> A calibration auditor independently re-verified a random sample of this
> sweep's findings after the fact, against four known-answer controls. Its
> result is reproduced here in full and unedited, because the measured error
> rate belongs next to the findings and not buried.

**Controls reproduced:** 4 of 4. **Control disagreements:** none.
**Sample size:** 13. **Sound:** 12. **Wrong line:** 0. **Wrong scope:** 1.
**Impossible fix:** 0. **False:** 0.

**Per-finding:**

- **CHAT-01** (P0, chat-runtime, wire) — **SOUND** — `partial_flush.go:66` quote exact. Verified independently: `api.go:4898` PersistPartial closure calls `mgr.AppendMessage` (`session/manager.go:474-486` mints a fresh `msg.ID` and INSERTs), `runPeriodicFlush` passes whole-turn `bridge.PartialState()` not a delta, and the file's own comment at `:12-13` does claim "write amplification acceptable for a single UPDATE" when no UPDATE exists on the path. I hunted for the read-side filter the finding says is absent: grep for `streaming_failed_at` over non-test Go returns only the migration, the store column list, `MarkStreamingFailure` and comments — no query excludes those rows; `modelHistoryRowsFrom` (`model_history.go:342-359`) copies every row with no filter and `projectRow`'s `case "":` arm (`:184`) includes them. I also hunted for a cleanup/supersede path for partial rows and found none.
- **CHAT-03** (P1, chat-runtime, wire) — **SOUND, with a misattributed supporting comment** — `chat_runner.go:1916` quote exact; `:1917` runs `Kernel.Run(redriveCtx, env)`, `redriveCancel()` fires only after Run returns, no parent link and no deadline. `StopStream` (`:1105-1116`) does `cancelCause.Store` / `sub.cancel()` / `<-sub.done`, and `close(sub.done)` is at `:1214` inside `driveRun`, which cannot run while `recoverFromOverflow` (called at `:1376`) is blocked. Defect confirmed end to end. BUT the evidence claims "the comment on the line above (\"fresh ctx — run ctx may be cancelled\")" — line 1915 is blank; that comment lives at `chat_runner.go:1875`, on the `attemptOverflowRecovery` call the finding separately cites as "the same shape one call earlier". One comment doing double duty across two call sites.
- **CHAT-07** (P2, chat-runtime, wire) — **SOUND, one secondary line slip** — `chat_default.yaml:142` quote exact. Independently confirmed the core claim: `coreag.CompactionInput` (`seams.go:794-819`) has nine fields and no Strategy; `exec_compute.go:1617`'s literal passes Site/RunID/NodeID/SessionID/ProjectID/Messages/ContextWindow/CurrentTokens/TargetTokens; `Pipeline.Compact` (`:450`) builds CompactRequest without Override (grep 'Override' over `pipeline.go` returns only `:166`, `:169`, `:227`), so Run falls to `strat = siteCfg.Strategy` at `:229`. Slip: `a.Strategy`'s two event-payload reads are `exec_compute.go:1592` and `:1640`, not "`:1597, :1640`". Does not touch the cited line.
- **AN-02** (P1, api-nonliteral, wire) — **SOUND defect, prescription wrong as filed** — `api.go:3451` quote exact; `newAttachmentsManager` (`:3441-3453`) passes only `NewSQLStore` + `WithMediaStore`, and `attachments.go:434`'s `if m.library == nil` returns `ErrSourceUnavailable`. But the disposition asserts `a.contextsLib` "exposes `Get(ctx, path) (string, error)`". It does not: `core/contexts/library.go:248` is `func (l *Library) Get(path string) (string, error)` — ONE argument — while `attachments.LibraryReader` (`attachments.go:124-126`) requires two. The named injection would not compile; an adapter is required. The call-ordering citation is also wrong (`newAttachmentsManager` runs at `api.go:1193`, not `:1281`; `contextsLib` is assigned at `:1404-1406`, so the ordering conclusion survives). The attached verifier correction catches both.
- **AN-04** (P2, api-nonliteral, wire) — **SOUND** — `api.go:1296` quote exact. I re-ran the caller hunt across the whole tree including `main.go` and `cmd/` (excluding worktrees): `WithPosture` and `SetPosture` appear only in `core/policy/cedar/prompt_test.go`. I checked for the near-miss that could have fooled the finder — `cedar.WithPostureMode` is a genuinely different, live symbol (a gate wrapper, `posture_test.go:41`) and the finding does not confuse them. Branch sites verified verbatim: `prompt.go:838` `if r.posture == PostureAutoAllow {`, `:847` `if r.posture != PostureAlwaysPrompt {`, and `:1189-1190` normalises the empty field to `PostureDefault`, freezing both.
- **AN-11** (P3, api-nonliteral, delete) — **WRONG-SCOPE** — the facts are all correct (`api.go:1259` quote exact; no reassignment of `a2aAPI`/`workflowAPI`/`trustAPI`/`contextAPI`; `bindings.go:552/567/582/593` exist; `stubs.go:186-203` returns `errNotWired`; zero `.vue` callers). But this is **ALREADY RECORDED** at `docs/dead-code-audit-2026-08-16.md:463-469`, which inventories the same five stub fields from the same constructor literal with per-field grep evidence — and rules "Escalate, then delete", warning in terms that "a2a (agent cards) and trust (secret references) are plausibly wanted, and deleting the consumer half of a wanted feature is the wrong call." AN-11 prescribes a flat delete of all four and overturns that recorded ruling without arguing against it. The finder's dedup covered only the two docs the brief named. Note the verify layer is inconsistent here: the client-to-vue verifier refuted C2V-16..19 on exactly this ground, while the api-nonliteral verifier confirmed AN-11 without running the same search.
- **SD-01 (settings-dials)** (P1, wire) — **SOUND defect, disposition half-blocked** — `impl.go:1551` quote exact; `SetAuditSettings` (`:1548-1552`) writes only the struct field under a mutex and never calls `SaveAll` (whose four validators I read at `:106-122`), and the only reader is `GetAuditSettings` (`:1539-1544`). Independently confirmed `log.RetentionSweep` has zero non-test callers (`grep -rIn 'RetentionSweep\b' core cmd` returns only `core/event/log/retention_test.go` plus one unrelated fleet test name). The verifier's correction is right and load-bearing: "schedule RetentionSweep in the same PR" is not achievable — there is no production `log.Store`, no production emitter, and `api.go:4082-4085` self-documents that audit emission is silenced, which is the already-ledgered ★A4 cluster. Wire the persisted field; escalate the sweep.
- **SD-04 (settings-dials)** (P2, wire) — **SOUND** — `api.go:1036` quote exact. I ran the caller search myself over `core`, `cmd`, `frontend/src` and `frontend/wailsjs`: every hit for `AutoCollapseBranchesInSidebar` is a declaration, doc comment, default constant, the accessor's own body, or the `impl.go:1336` seed — no consumer. `LeftRail.vue`'s `branchCollapsed` initialiser (`:161-170`, one line longer than the finding's "`:161-168`") reads localStorage only and returns an empty Set when absent, so nothing starts collapsed.
- **SD-09 (settings-dials)** (P2, wire) — **SOUND** — `SettingsView.vue:1698` quote exact. Confirmed it is the only substantive consumer: every other `windowSize` hit in `frontend/src` is a `{ width: 1280, height: 800 }` default literal in a component fixture, the type declaration, or the fake client. Go side confirmed: seeded at `impl.go:96-100` and `api.go:6941`, and `main.go:228-229` passes literal `Width: 1280` / `Height: 800` into `wails.Run` with no `WindowSetSize` or `WindowGetSize` call anywhere.
- **CK-02** (P1, compaction-knobs, wire) — **SOUND defect, one prescription clause FALSE and the title over-scoped** — `strategies.go:448` quote exact. Independently confirmed: `NewSemanticClusterStrategy`, `NewCustomSubgraphStrategy` and `NewNarrativeFirstStrategy` have zero non-test callers; production `RegisterStrategy` sites are exactly `api.go:6228` and `:6233`; the panel offers `'semantic_cluster'` and `'custom_subgraph'` at `CompactionStrategyPanel.vue:90-91`; `pipeline.go:239` returns `ErrUnknownStrategy` before any strategy runs. TWO problems. (a) The disposition says custom_subgraph "needs the kernel as KernelRunner, already built at `api.go:6235`" — **FALSE**: `grep -rn RunGraph` over core+cmd returns the interface decl (`compactor.go:246`), the single call site (`strategies.go:697`) and two TEST FAKES only. Nothing implements `KernelRunner`; that half of the wire cannot be written. (b) The title says selecting narrative_first hard-fails, but narrative_first is absent from the panel's strategies list — an orphan constructor, not a user-selectable brick. The evidence body is accurate; the title over-claims.
- **CK-04** (P2, compaction-knobs, delete) — **SOUND** — `config.go:48` quote exact. I re-derived the whole reference set rather than trusting the finder's: `ToolResultMaxBytes` appears only as the declaration + doc, the `fToolResultMaxBytes` presence bit (`:89`/`:103`/`:131`/`:327`/`:394-396`), two default seeds (`:451`, `presets.go:91`), the RPC round trip (`views/compaction/api.go:53`, `impl.go:260`/`:286`/`:305-306`) and two doc comments (`compactor.go:81`, `seams.go:785`). I enumerated `CompactOpts` (`compactor.go:197-225`) — Strategy, MaxRecursionDepth, CustomGraph, SubgraphInputPort/OutputPort, SummaryProvider, SummaryModel, DropOldestKeepRecentN, SemanticClusterCount, Now — no such field, and `pipeline.go` contains zero occurrences, so `mergeOpts` provably cannot carry it. Not one branching read. The verifier's scope note (`validator.go:35` registers the same name as a graph dial and is equally unread) is correct and should ship with the delete.
- **MO-01** (P1, mcp-oauth, wire) — **SOUND** — `slack_signin.go:116` quote exact. I ran the symbol search myself over the whole tree excluding worktrees: `SlackSignIn`, `ResolveSlackClientID`, `SlackLoopbackPort` and `KAMEAS_SLACK_OAUTH_CLIENT_ID` appear ONLY inside `core/mcp/oauth/` plus one comment in `core/mcp/recipes/registry_test.go:359` — zero production reachability. I parsed `registry.json` directly: the slack recipe is `auth.kind=mcp_oauth` with `client_id ""`, so `views/tools/oauth.go:163` `if recipe.Auth.ClientID == ""` rejects it before any provider dispatch, and `ResolveSlackClientID`'s `os.Getenv` fallback is never consulted. The documented escape hatch is genuinely inert.
- **MO-07** (P3, mcp-oauth, delete) — **SOUND** — `oauth.go:145` quote exact. Read `:142-146` directly: the initialiser at `:143` and the guarded assignment at `:145` are character-identical (`tokenEndpoint := issuer + "/access_token"` / `tokenEndpoint = issuer + "/access_token"`), so the GitHub condition at `:144` provably changes nothing. This is the one class of finding no search can refute. The verifier's correction is also right — the "any provider silently gets GitHub's shape" impact belongs to the initialiser, not to the dead branch.

**Estimated unsound of total:**

> Roughly 30-50 of ~200 (15-25%) need rework before implementation — but almost
> none because the DEFECT is imaginary. By failure mode, with my sample rate and
> the reasoning that extrapolates it. **(1) FABRICATED OR WRONG CITATIONS —
> estimated 0-5 findings (0-2%).** 13 of 13 primary `file:line` quotes matched
> byte-for-byte; all eight verifier statements independently claim a
> 100%-or-near quote-match rate on their own module. This is the campaign's
> historical failure mode and it does not appear in this corpus. I did find four
> secondary-prose slips (CHAT-03's comment attributed to the wrong call site,
> CHAT-07's `:1597`-for-`:1592`, AN-02's `:1281`-for-`:1193`, SD-04's
> `:161-168`-for-`:161-170`) — supporting prose runs ~70-80% accurate while
> primary citations run ~100%. **(2) DUPLICATES / SCOPE OVERLAP WITH PRIOR
> RECORDS — estimated 15-30 findings (8-15%), the largest bucket.** 1 of my 13
> (AN-11). The mechanism is structural and predictable: finders deduped against
> the two docs the brief named and nothing else. Where a verifier independently
> checked other sources it found more — settings-dials refuted 2/15 via
> `scripts/ci/allowlists/`, client-to-vue refuted 4/36 via
> `docs/dead-code-audit-2026-08-16.md`. The five modules whose verifiers did not
> run that check (api-nonliteral, chat-runtime, compaction-knobs, mcp-oauth,
> bundle-trust) carry the same latent rate unfound; AN-11 is my proof it is
> latent rather than absent. **(3) WRONG PRESCRIPTIONS (defect real, named fix
> does not compile or the target does not satisfy the interface) — 3 of my 13
> raw (23%), but the verify layer caught 3 of 3, so residual post-correction is
> ~5-8% (10-16 findings).** This class burns implementation hours rather than
> causing damage. **(4) OUTRIGHT FALSE (no defect at all) — estimated 4-10
> (2-5%).** 0 of my 13; verifiers refuted 12 of ~200 (~6%) and my independent
> pass added none.

**Dominant error kind:**

> Not fabrication and not false defects — the two things the owner most feared
> are close to absent. The dominant error is **DISPOSITION**, in two forms.
> First and largest: incomplete deduplication against records the brief did not
> name (`docs/dead-code-audit-2026-08-16.md` and `scripts/ci/allowlists/`),
> producing re-reports whose new disposition silently overturns an earlier
> recorded ruling — AN-11 prescribes deleting four stub domains that the 08-16
> audit expressly ruled must be escalated first. Second: prescriptions naming a
> wire target that does not satisfy the interface it must satisfy (AN-02's
> one-arg `contexts.Library.Get` vs the two-arg `LibraryReader`; CK-02's
> `KernelRunner`, which nothing in the tree implements; SD-01's sweep, blocked
> behind the ledgered ★A4 emitter gap). The finders' OBSERVATIONS are strong;
> their FIX PLANS are the weak layer, and the verify layer caught every instance
> of that in my sample.

**Verdict:**

> Trust the defects; do not trust the fix plans or the novelty claims without
> one more cheap pass. All four known-answer controls reproduced exactly, with
> no disagreement. Across 13 findings re-verified from scratch — five modules,
> P0 through P3, both wire and delete dispositions — I found zero fabricated
> citations, zero false defects, and one finding I would not act on as written.
> That is a materially sound corpus. The earlier 1-in-161 refutation rate that
> prompted this audit reflects genuinely verifiable code (closed import graphs,
> byte-identical branches, zero-caller symbols) more than it reflects
> rubber-stamping; this round's verifiers refuted 12 of ~200 and issued
> substantive corrections on roughly a third, which is real work. **Two caveats
> before committing months of implementation. FIRST: run a mechanical dedup of
> all ~200 findings against `docs/dead-code-audit-2026-08-16.md` and
> `scripts/ci/allowlists/` before scoping any work.** Neither was in the
> finders' search path, both contain prior rulings, and the one failure in my
> sample was exactly this. It costs an afternoon and I estimate it reclassifies
> 15-30 findings — disproportionately in the DELETE lane, which is also the lane
> where acting on a duplicate does irreversible damage. AN-11 is the concrete
> hazard: implemented as filed it deletes the a2a and trust surfaces a prior
> audit explicitly ruled must be escalated first. **SECOND: treat every
> disposition that names a specific type to wire as unverified until someone
> confirms the method set.** Three of my 13 named a target that does not satisfy
> the required interface; the verify layer caught all three, which means the
> corrections are the load-bearing half of this corpus and must not be dropped
> when findings become specs. Two limits on my confidence. My 13 is 6.5% of the
> corpus and was deliberately weighted toward P0/P1 and wire-dispositioned items
> per the task — the most-scrutinised slice. I sampled only three
> delete-dispositioned findings and the one failure was among them, so the
> delete lane's true error rate is probably higher than my headline 1-in-13 and
> is where I would sample next. And I re-verified only what these 13 cite; I did
> not audit the ~187 findings outside the sample, nor the persisted-history half
> of `core/agentgraph/compaction` (`session_engine`, `session_rolling`,
> `session_snap`, `session_sweep`), which the compaction verifier also flagged
> as read but never adversarially re-checked.

---

## C.5 Mandatory pre-implementation step

The calibration auditor's first caveat is not advice; treat it as a gate on the
whole campaign.

**Before any of the six missions is scoped, run a mechanical dedup of all ~327
findings against `docs/dead-code-audit-2026-08-16.md` and every file under
`scripts/ci/allowlists/`.** Neither was in any finder's search path in any of the
three sweeps — every novelty claim in this document and in the 08-18 body rests
on a grep of two files. Two verifiers who independently widened the search found
hits immediately (settings-dials refuted 2 of 15 via the allowlists; client-to-vue
refuted 4 of 36 via the 08-16 audit), and the calibration sample's single failure
(`AN-11`) is the same mechanism. **The DELETE lane is where this matters**:
acting on a duplicate delete is irreversible, and `AN-11` as filed would remove
two surfaces a prior audit expressly ruled must be escalated first.

Second gate, cheaper: **every disposition naming a concrete type to wire is
unverified until its method set is confirmed.** Three of thirteen sampled
findings named a target that does not satisfy the required interface, and all
three were caught only by the verify layer. **The corrections in this document
are load-bearing and must travel with the findings into the specs** — the four
known-impossible wires are `AN-01`/`CK-01` (`LLMCaller` has no `Generate`),
`AN-02` (`Library.Get` takes one argument), `CK-02` (nothing implements
`KernelRunner`), and `SD-01` settings (there is no production audit store to
sweep).

---

## C.6 Mission disposition

Five missions are specced and one — `controls-and-readouts-that-tell-the-truth`
— is identified and owed. This sweep's 141 distinct findings assign as follows.

**To existing missions (81 findings):**

| Mission | Absorbs | Notable |
|---|---|---|
| `model-settings-reach-the-model-01PMZ101` | 6 | `CHAT-08` + the escalation-ladder widening — five executors build their own `LLMRequest` and the sole production provider drops `Provider`/`Model` on all of them, so `chat_default.yaml:267`'s `escalation_ladder` reports an escalation that did not happen. Plus `CHAT-09` (reasoning discarded one hop from the screen), `AN-07` (recommender knows two providers), `CK-08` (compaction cost computed and thrown away — the producer half of the already-filed row). |
| `trust-surfaces-that-fire-01PMZ202` | 24 | The largest absorption. `AN-04` (autonomy tier has no effect on prompting), `CHAT-05` (**no production path produces a `confirm_each` verdict at all** — retracts the negative result at `:1221`), `CHAT-02` (interrupt fabricates cancelled tool_results and produces the duplicate shape providers 400 on), `AN-03` (BulkPurge + two audit kinds), `SD-01`/`SD-02` settings, `SD-01`/`SD-02` serve (fabricated permission posture; **fabricated empty audit trail**), `BT-01`/`BT-02`/`BT-03` + the artifact-content-hash and lockfile-schema extras, `AN-08` (Redactor escalation), `C2V-03`/`C2V-04`, and the three cross-session authorization leaks in served mode. |
| `connector-lifecycle-truth-01PMZ303` | 17 | All twelve `MO-*` OAuth findings plus three verifier extras. `MO-01` is a second, complete provider lane (Slack) that ◆B4 never mentions, with an inert env var shipped as user-visible install-modal copy. `MO-02`/`MO-04`/`MO-12` are **blocking sub-items of ◆B4** — wiring `SignInWithDCR` without them ships a flow RFC-conformant providers reject, with no recovery from a revoked registration. Plus `AN-06` (MCP roots point at the harness data directory). |
| `automation-actually-runs-01PMZ404` | 5 | `C2V-14` (bootstrap resume), `C2V-08` (sync resume escalation), `C2V-30` (session reorder escalation), `C2V-35`, `C2V-01`'s handoff-share prerequisite. |
| `fleet-enforcement-truth-01PMZ505` | 7 | `SD-08` settings (an inert dial that is nonetheless fleet-synced to every device), the `fleet.go` apply-path gap, `SD-09` serve (`session.signed_out` never emitted), `SD-07`/`SD-08` serve (no 401 detector on either token cache), `C2V-07` (remote purge), `AN-10`+`C2V-36` (ACP escalation, now naming both construction sites). |
| `controls-and-readouts-that-tell-the-truth` *(unspecced, 6th)* | **+22 → ~48 findings, ~18 WPs** | This sweep more than doubles it and confirms the framing exactly: `SD-03`/`SD-04`/`SD-05`/`SD-06`/`SD-09`/`SD-10`/`SD-13`/`SD-14` settings, `C2V-09`…`C2V-15`, `C2V-21`…`C2V-34`, the saved-audit-query truncation, and `SD-13`/`SD-15`/`SD-16` serve. **Spec it — it is now the second-largest body of work in the campaign.** |

**Two new missions (60 findings).** Both cover subsystems that no existing
mission touches and that the 08-18 audit's own gap note excluded by name.

---

### New mission 1 — `chat-turn-integrity-01PMZ606` · **P0** · ~14 WPs

> *The transcript is a true record of the turn. What the harness wrote is what
> happened, what the model reads back is what it said, and Stop stops.*

**32 findings** (`CHAT-01`…`CHAT-13`, `CK-01`…`CK-15`, `AN-01`, `AN-12`, and the
seven compaction verifier extras), including the sweep's **only P0** and four
P1s.

**Why it does not fit anywhere else.** No existing mission covers the chat turn
loop or the compaction subsystem, and the 08-18 audit **explicitly excluded**
`core/agentgraph/compaction/**` and the `compaction/wiring/` adapters from its
scope (`:896-899`) — which is precisely why the producer half of the
already-filed `compaction-overhead-row-writerless` row was never traced to its
consumer half. The findings are also causally coupled in a way that forbids
splitting them: `CHAT-01` and `CHAT-02` both write rows that `CHAT-05`'s
permission gap and `CHAT-07`'s strategy mis-report then read back; `CK-02`'s
unregistered strategies and `AN-01`'s nil summariser are the same panel's
options; `CK-03`'s dead site and `CK-04`'s dead dial are the same checkbox row;
and the liveDialResolver boot-reset silently undoes every fix in the Settings
panel until it is closed.

**Sequencing that the evidence dictates:** the boot-reset (`api.go:6024`) must
land **first** or every subsequent compaction fix is invisible on the next
launch. `CHAT-01` needs `session.Manager` to grow an update-in-place before the
flush can be repaired, and its fixture must drive real sqlite (blind spot #2 is
confirmed live here). The `custom_subgraph` arm of `CK-02` is **not
implementable** and must ship as panel-honesty, not as a wire.

---

### New mission 2 — `served-mode-is-a-real-mode-01PMZ707` · **P1** · ~10 WPs

> *Every surface the workbench routes either works, or says plainly that it
> cannot. Nothing renders a default in place of an answer it never got.*

**28 findings** (`SD-01`…`SD-17` serve, four verifier extras, `SD-11`'s missing
signal handler, plus the served-client-overlay gap the client-to-vue cluster
flagged and could not reach).

**Why it does not fit anywhere else.** Served mode is a deployment *variant*,
not a feature area: its findings cut across permissions, audit, chat,
connectors and fleet, and splitting them into those five missions guarantees
nobody owns `docs/served-mode-boundary.md` or the 417-entry gap. The gate that
should catch this class (`check-serve-dispatch-drift.sh`) is one-directional,
has no allowlist, and can therefore **never be promoted from informational** —
`SD-12` is a gate-extension-rule item that belongs to whoever owns the boundary,
not to five separate teams.

**The two findings that set its severity** are class F: `/audit` renders a
clean, empty, non-erroring compliance trail when the backend refused the query,
and `/permissions` paints a safe-looking `'normal'` posture it never read.
Behind them sit three **authorization** leaks the drift gate cannot see —
`tool:confirm-pending` and `elicit:pending` ride a global fan-out with no
session filter at either end, and `Confirm_Resolve`, `Confirm_ApproveBatch` and
`Elicit_SubmitAnswer` are all in `servedMethods`, so the approval lands. Those
three should be sequenced first regardless of the rest.

---

### Escalations that are questions, not work

Four of this sweep's clusters converge on decisions only the owner can make.
Recording them here rather than resolving them by deleting:

1. **Is the bundle/trust subsystem still wanted?** `BT-03`, `BT-05`, `BT-07`,
   `BT-09`, `BT-10`, `BT-11` and `BT-12` all name a blocker owned by
   `kitty-specs/_archive/a2a-signed-cards-trust-01KQ18P9` or
   `_archive/bundle-format-resolver-01KQ1A3J`. **Both are archived**, both have
   stub `status.json` files with zero recorded work packages, and neither
   shipped the WP the justifications point at. Under the ritual's own rule a
   justification must name the change that will delete the line; naming a closed
   mission is "we'll get to it". **That concentration is itself the escalation
   signal.** The answer to this one question disposes of ~16 findings at once.
2. **Is `corpus` retired?** The ledger's product-retirement note says corpora →
   contexts, and the entire nine-method client surface has no UI — but
   `corpus_read` ships in the node catalog, `exec_state.go:106` calls
   `env.Corpus.Search` on the live kernel path, and `corpus_write` has a second
   ingest path through `env_deps_corpus.go:68`. Deleting one side leaves the
   graph kernel lying. (`C2V-02`, `AN-12`.)
3. **Does greedy kernel memory capture get a redaction pipeline, and is it the
   same one as export?** `AN-08` — no implementation of `agentgraph.Redactor`
   exists anywhere, so this is a missing component, not a missing injection,
   and the repo already has two rival redaction vocabularies.
4. **Is `confirm_each` a capability the product offers?** `CHAT-05` — either it
   needs a writer (a permissions UI or a default rule set), and two autonomy
   knobs become live; or it is not, and a large tested trust surface is
   pretending to be a control. Do not resolve by deleting.

---

## C.7 Final tally

| | Sweeps 1–2 | Closing sweep | Total |
|---|---|---|---|
| Confirmed findings (raw) | 183 | 144 | **327** |
| Distinct after dedup | — | 141 | **~324** |
| User-visible | 87+ | 66 | **~153** |
| P0 | 1 | 1 | 2 |

**Mission set: six specced + two new = eight.**

| Mission | WPs | Severity |
|---|---|---|
| `model-settings-reach-the-model-01PMZ101` | 12 | P0 |
| `trust-surfaces-that-fire-01PMZ202` | 17 (+~3) | P0 |
| `automation-actually-runs-01PMZ404` | 12 | P0 |
| `fleet-enforcement-truth-01PMZ505` | 10 | P0 |
| `connector-lifecycle-truth-01PMZ303` | 9 (+~4) | P1 |
| `controls-and-readouts-that-tell-the-truth` | ~18 | P1 · **owed, unspecced** |
| `chat-turn-integrity-01PMZ606` | ~14 | **P0 · new** |
| `served-mode-is-a-real-mode-01PMZ707` | ~10 | **P1 · new** |

**Is the map complete enough to stop looking?** Yes, with two named exceptions
that are cheap and bounded, and one that is not.

Cheap and bounded: the **408-vs-403 `WailsBindingsLike` diff** (one mechanical
pass, closes gap 7b) and the **`createServedHarnessClient` overlay enumeration**
(closes gap 7a and is a prerequisite for mission 2's WP scoping anyway). Both
are hours, not days, and both are inputs to work that is already going to happen.

Not cheap, and the honest answer to "what is left": **the two structural blind
spots in §C.3 — test fixtures that bypass the layer under test, and upgrade-path
defects — were audited in one cluster and zero clusters respectively.** In the
one cluster that looked at fixtures, two of three opened were the bypass pattern
and both concealed a filed P0/P1. That is not a coverage gap in the *map*; it is
a coverage gap in the *tree*, and it will not be closed by more searching. It is
closed by writing the fixtures, which is implementation work and belongs inside
the missions above — `chat-turn-integrity-01PMZ606` WP01 and the compaction
`ApplyCompaction` SQL body are where it starts.

Start fixing. Run §C.5's dedup first.

---

## Dedup pass (2026-08-19) — the gate the calibration audit required

The calibration audit named one blocker before implementation: **no finder in
any of the three sweeps searched `docs/dead-code-audit-2026-08-16.md` or
`scripts/ci/allowlists/`.** Every novelty claim rested on a grep of two files.
It estimated 15–30 duplicates (8–15%). That estimate was wrong, and *why* it
was wrong is the useful part.

**89 prior records indexed** — 30 dispositioned findings from the 08-16 audit
(A1–A11, B1–B18, C1) plus 59 live entries across the 12 allowlist files.

| Category | Count |
|---|---|
| TRUE DUPLICATE (strike) | 1 |
| REFINEMENT (keep, cite prior record) | 5 |
| CONTRADICTION | 1 |
| **Missed prior records — still live, covered by no sweep** | **5** |

**Why the overlap is so low.** The 08-18 sweep's base tree is `55029354` — the
v0.64.0 release commit, which was specced *directly from the 08-16 audit* and
closed nearly all of it (A1–A11, B1, B2, B5, B6, B7, B8, B9a, B10, B13, B14,
B16, B17). The sweeps did not quietly reproduce fixed defects; the code those
findings described mostly no longer exists in that shape. The low rate is real.

### Strike (1)

- `llm-updateprovidercredential-*` ≡ 08-16 **B11**. Same defect, same delete
  disposition, same substitute (`client.llm.updateProvider`). Redundant, not
  harmful — both audits agreed.

### Blocked from shipping as filed (1)

- **`AN-11`** vs 08-16 **B12**. Facts correct; prescription reverses an explicit
  ruling. B12 ruled *"Escalate, then delete"* — *"a2a (agent cards) and trust
  (secret references) are plausibly wanted, and deleting the consumer half of a
  wanted feature is the wrong call."* It carved out exactly ONE exception: the
  singular `Workflow_*` trio, which has a live plural substitute.
  **So 3 of AN-11's 4 targets contradict the prior ruling and 1 does not.**
  Ship the `workflowAPI` deletion; route `a2aAPI`/`trustAPI`/`contextAPI` to the
  escalation register. Already flagged above: *"The 08-16 ruling stands."*

### Missed prior records — fold into the next release (5)

Verified against the current checkout 2026-08-19, not inferred:

1. **08-16 B3** — local telemetry retention sweep. Unbuilt:
   `core/telemetry/instance.go:9` still reads *"WP03 lands retention sweep +
   per-span attribute allowlist"* in future tense. Mentioned nowhere in this
   audit.
2. **08-16 B15** — memory hook journal read path. Tracked as backlog #27 but
   re-covered by no finding: `api.go:1868`'s `memoryview.Config{}` omits
   `Journal:`, `views/memory/impl.go:342` short-circuits on `a.journal == nil`,
   `HookJournalView.vue` is still not imported by `MemoryView.vue`.
3. **08-16 B9 (second half)** — unbound `AuthFailureToast` / `FallbackActivePill`
   emits, no listener at either mount site. Low severity (an unlistened emit is
   not a lie) but uncovered.
4. **i10 `core/storage/db.CheckSQLiteVersion`** — zero callers; only the
   declaration and its doc comment exist. Absent even from this audit's own
   "already recorded" I10 cross-reference, so that internal check was itself
   incomplete.
5. **i10 `core/fleet.VerifySignature`** — zero callers, same shape, also absent
   from the cross-reference.

### Bottom line

**327 → strike 1.** The corpus needs a targeted correction, not a reduction.
The real residue is the five items above, which no sweep re-covers and which
must not be assumed subsumed by the closing sweep's 141-finding tally.

**Standing correction to method:** items 4 and 5 show the I10 cross-reference
that *did* run was incomplete. Any future sweep must index
`scripts/ci/allowlists/` mechanically rather than by spot-check — an allowlist
entry is a prior record, and "known and gated" is not "found again".

---

## ⚠️ READING THIS DOCUMENT — phantom finding ids (added 2026-08-19)

**Range notation in this document is RHETORICAL, not enumerative.** A phrase like
*"32 findings, `CHAT-01`…`CHAT-13`"* does NOT mean every id in that range exists.

Verified 2026-08-19: `CHAT-04`, `CK-07` and `CK-12` have **zero** occurrences in
this file, while `CHAT-01` (9), `CHAT-07` (4) and `CK-02` (6) are real. The
`chat-turn-integrity-01PMZ606` spec caught this while scoping and refused to
spec work for findings that do not exist.

**Rule for anyone scoping from this document: scope by ENUMERATED finding, never
by range.** Grep the id before you spec it.

### A SECOND kind of phantom, which survives that grep (found 2026-08-19)

`SD-06` and `SD-07` occur 4 and 3 times, so a naive existence-grep says they are
real. **Every occurrence is the *serve*-scoped finding or a bare list
membership.** No settings-scoped defect text exists anywhere. Yet the
mission-assignment row at `:1798` buckets `SD-06` as a settings dial, while
`:1404` defines it as serve. **The assignment invented a scope the finding does
not have.**

So the rule is stronger than "grep the id":

> **Grep the id, then confirm the finding's SCOPE matches the row that assigned
> it to you.** An id that exists in another mission's scope is not a finding in
> yours. If the assignment table and the finding body disagree, the body wins
> and the assignment is the defect.

The mission-assignment table (`:1790-1800`) was produced by a clustering pass
that read summaries, not bodies. Treat its counts as estimates and its scopes as
unverified. `controls-and-readouts-01PMZ808` found **12 of its assigned findings
already specced with work packages in other missions** — double-assignment is
common in that table, and the fix is to check the other mission before writing a
WP. A spec that budgets work for a
phantom finding produces a work package with nothing to implement, which is the
same class of lie this audit exists to eliminate — an implementer would either
invent a defect to match the id or quietly drop the WP, and both outcomes are
worse than the gap.

This is a defect in the audit's prose, not in the findings themselves. The
findings that DO exist were independently verified and calibrated (zero
fabricated citations across a 13-item sample); the counts and ranges around them
were not.
