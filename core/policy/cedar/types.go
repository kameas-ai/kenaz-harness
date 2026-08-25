package cedar

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	cedar "github.com/cedar-policy/cedar-go"
)

// Action constants are the gate-hook operation names the engine
// recognises. Each corresponds to one of the five gate categories
// listed in spec FR-053:
//
//	tool_exec, file_write, network_request, model_select, memory_write.
//
// Cedar identifies actions by EntityUID with type "Action"; the ID is
// the constant string here.
const (
	ActionToolExec       = "tool_exec"
	ActionFileWrite      = "file_write"
	ActionFileRead       = "file_read"
	ActionNetworkRequest = "network_request"
	ActionModelSelect    = "model_select"
	ActionMemoryWrite    = "memory_write"

	// State-kind action UIDs introduced by FR-058b. These coexist with
	// the broad `tool_exec` and `file_*` actions: a State `read_file`
	// node evaluates BOTH `Filesystem::"<path>"` (via ActionFileRead)
	// AND a finer-grained `Read::"<source>"` action UID so policy
	// authors can express "permit reads but deny reads from
	// `~/.ssh`" with a single forbid rule.
	ActionStateRead  = "state_read"
	ActionStateWrite = "state_write"

	// Action UIDs introduced by mission cedar-credential-policy-01KQ8TDE
	// (WP01). They identify the four resource families that the
	// universal interactive permission system gates:
	//
	//   ActionUseCredential    — Credential::"<provider-id>::<purpose>"
	//   ActionRunBashCommand   — BashCommand::"<argv-pattern>"
	//   ActionReadFilesystem   — FilesystemOp::"<canonical-path>" (read)
	//   ActionWriteFilesystem  — FilesystemOp::"<canonical-path>" (write)
	//   ActionUseTool          — Tool::"<fully-qualified-tool-name>"
	//
	// These coexist with the broader `tool_exec` / `file_*` actions
	// from agent-kernel-graph; consumers gradually migrate to the
	// finer-grained surface as each WP lands.
	ActionUseCredential   = "use_credential"
	ActionRunBashCommand  = "run_bash_command"
	ActionReadFilesystem  = "read_filesystem"
	ActionWriteFilesystem = "write_filesystem"
	ActionUseTool         = "use_tool"

	// MCP recipe family actions introduced by mission
	// mcp-server-install-01KQ8TDP (WP10).
	//
	//   ActionAddRecipe   — gates AddRecipe + EditRecipe RPC calls
	//   ActionSpawnRecipe — gates the spawn path when the pool opens a recipe
	ActionAddRecipe   = "add_recipe"
	ActionSpawnRecipe = "spawn_recipe"

	// Workflow-family action UIDs introduced by mission
	// workflows-01KQ8TDG (WP11). They gate the three RPC-level
	// operations on workflow definitions:
	//
	//   ActionWorkflowRun    — gates Engine.Run (workflow dispatch)
	//   ActionWorkflowSave   — gates Save (persist a definition)
	//   ActionWorkflowDelete — gates Delete (remove a definition)
	//
	// The action UIDs use dotted form to match the audit kind names
	// (workflow.executed / workflow.saved / workflow.deleted) so the
	// audit panel cross-references the same surface.
	ActionWorkflowRun    = "workflow.run"
	ActionWorkflowSave   = "workflow.save"
	ActionWorkflowDelete = "workflow.delete"

	// ActionWorkflowNetworkFetch gates a web_fetch / web_scrape step's
	// outbound request (automation-actually-runs-01PMZ404 UNIT-7). The
	// shipped default_workflows_policy.cedar bundle already references
	// this action string (WP05's "network.fetch" section); the constant
	// did not exist until UNIT-7 built GateWorkflowNetworkFetch, the
	// first Go call site.
	ActionWorkflowNetworkFetch = "workflow.network.fetch"

	// ActionArtifactUpdate gates the kenaz__update_artifact builtin
	// (update-artifact-tool-01KQ8TD4). Default-allow under FSWriteEnabled;
	// gated by the same FSWriteDisabled toggle as the write-family fs
	// builtins. The resource UID is Artifact::"<artifact-id>".
	ActionArtifactUpdate = "artifact.update"

	// ── Builtin search-and-elicitation action families ──────────────────
	// Introduced by mission builtin-tools-search-and-elicitation-01KZNP3D
	// and extended by ask-user-question-interactive-01KZNP3G. All families
	// follow the dotted "<domain>.<operation>" naming convention
	// established by the workflow and artifact action families above.
	// Downstream skills-catalog + future elicitation missions MUST follow
	// the same convention when adding new action families.

	// ActionToolReadGlob gates kenaz__glob (glob-match across a directory
	// tree). Resource UID: Filesystem::"<base-dir>". Default-allow when
	// FSReadEnabled is true; gated by the same FSReadDisabled toggle.
	ActionToolReadGlob = "tool.read.glob"

	// ActionToolReadGrep gates kenaz__grep (in-process regex search).
	// Resource UID: Filesystem::"<search-path>". Default-allow when
	// FSReadEnabled is true; gated by the same FSReadDisabled toggle.
	ActionToolReadGrep = "tool.read.grep"

	// ActionToolTodoWrite gates kenaz__todo_write (structured task list
	// write). Resource UID: TodoList::"session" (session-scoped list).
	// Default-allow when TodoEnabled is true; gated by TodoDisabled toggle.
	// The action intentionally lives in the "tool.todo" family rather than
	// "tool.write" so policy authors can grant todo access independently of
	// the broader filesystem write surface.
	ActionToolTodoWrite = "tool.todo.write"

	// ActionToolTodoRead gates future kenaz__todo_read (reserved — not yet
	// implemented). Declared here so Cedar policy snippets written against
	// the action family validate without schema changes when the read tool
	// ships. Resource UID: TodoList::"session".
	ActionToolTodoRead = "tool.todo.read"

	// ActionElicitAsk gates kenaz__ask_user_question (synchronous
	// single-question elicitation: model calls tool → backend opens
	// dialog → user answers → result returns to model). Introduced by
	// ask-user-question-interactive-01KZNP3G WP01. Resource UID:
	// Elicitation::"<kind>" where kind is one of the seven question kinds
	// (radio, checkbox, text, number, slider, date, file).
	ActionElicitAsk = "tool.elicit.ask"

	// ActionPassive is the Cedar action family for passive (no-side-effect)
	// builtin tools. Introduced by mission
	// builtin-tools-search-and-elicitation-01KZNP3D (WP04).
	//
	// Currently only kenaz__sleep uses this action. Passive tools are
	// default-allow and are never gated by a Settings toggle because they
	// have no observable side effects and must remain available for
	// __monitor watch patterns.
	//
	// The resource UID convention is Tool::"kenaz__<tool_name>" — the same
	// shape as ActionUseTool, but evaluated under the tool.passive action
	// ID so policy authors can write targeted rules for passive tools
	// without affecting the broader use_tool gate.
	ActionPassive = "tool.passive"

	// ── Skill-invocation action family ────────────────────────────────────
	// Introduced by mission model-invoked-skills-catalog-01KZNP3E.
	// The "tool.skill" family follows the established "<domain>.<operation>"
	// naming convention. Resource UIDs take the form Skill::"<command-name>"
	// so policy authors can permit or forbid individual skills independently
	// of the broader tool surface.
	//
	//   ActionToolSkillInvoke — gates kenaz__skill calls (model invokes a
	//     user-defined slash command marked model_invokable=true).
	//     Resource UID: Skill::"<command-name>".
	ActionToolSkillInvoke = "tool.skill.invoke"

	// ActionElicitTemplatePrefix is the Cedar action prefix for template-based
	// elicitation (ask-user-question-interactive-01KZNP3G WP07).  The full
	// action ID is built as ActionElicitTemplatePrefix + <template-id>
	// (e.g. "tool.elicit.template.confirm-deploy").  Policy authors can
	// therefore deny individual templates while leaving the general ask open.
	ActionElicitTemplatePrefix = "tool.elicit.template."

	// ActionElicitDeferred gates async / deferred-mode elicitation
	// (ask-user-question-interactive-01KZNP3G WP06).  Non-interactive
	// sub-agents receive declined:true,reason:"non_interactive" regardless.
	ActionElicitDeferred = "tool.elicit.ask_deferred"

	// ── Scheduled chat-run action family ─────────────────────────────────
	// Introduced by mission scheduled-chat-runs-01KX5R8B (WP03).
	//
	// These three actions gate the three RPC-level operations on scheduled
	// chat runs. The "tool.scheduled_run" family follows the established
	// "<domain>.<operation>" naming convention used by
	// workflow.run / workflow.save / workflow.delete.
	//
	//   ActionScheduledRunCreate  — gates Create + Update RPCs.
	//     Resource UID: ScheduledChatRun::"<run-id>" (new) or
	//                   ScheduledChatRun::"*" (blanket).
	//   ActionScheduledRunDelete  — gates Delete.
	//   ActionScheduledRunExecute — gates background dispatch (both
	//     cron-triggered and RunNow paths). Policy authors can deny
	//     scheduled execution entirely without affecting CRUD.
	ActionScheduledRunCreate  = "tool.scheduled_run.create"
	ActionScheduledRunDelete  = "tool.scheduled_run.delete"
	ActionScheduledRunExecute = "tool.scheduled_run.execute"

	// ── Sub-agent dispatch action family ─────────────────────────────────────
	// Introduced by mission branch-subagent-interactive-01KZNP3B (WP03).
	// The "tool.subagent" family follows the established "<domain>.<operation>"
	// naming convention.
	//
	//   ActionToolSubagentDispatch — gates kenaz__subagent_dispatch calls.
	//     Resource UID: SubagentProfile::"<profile-id>". Policy authors can
	//     forbid specific profiles (e.g. the implementer profile in a
	//     read-only session) while leaving others open.
	//
	//   ActionToolSubagentMerge — gates explicit __subagent_merge(branch_id)
	//     calls (MergePolicyManual path). Default-allow; admins can deny
	//     to prevent the parent from absorbing a worker's output.
	ActionToolSubagentDispatch = "tool.subagent.dispatch"
	ActionToolSubagentMerge    = "tool.subagent.merge"

	// ── Model-side secret reference action family ──────────────────────────
	// Introduced by mission model-secret-references-01KW7M5A.
	// Both actions follow the established "<domain>.<operation>" naming
	// convention.
	//
	//   ActionSecretReferenceResolve — gates every call to refs.Resolve that
	//     substitutes an @secret:<locator> token in a tool argument. Resource
	//     UID: SecretReference::"<locator>". Default-deny outside the session's
	//     exposed_secrets set; forbid for untrusted agent_kind; forbid when the
	//     per-session resolution budget is exhausted.
	//
	//   ActionToolListSecrets — gates the kenaz__list_secrets builtin tool.
	//     Resource UID: Tool::"builtin__list_secrets". Default-allow so the
	//     model can discover what references it may use; admins can disable
	//     per-session with an explicit forbid rule.
	ActionSecretReferenceResolve = "secret_reference.resolve"
	ActionToolListSecrets        = "tool.list_secrets"

	// ── Background task monitor action family ─────────────────────────────
	// Introduced by mission background-task-monitor-01KZNP3C (WP03).
	//
	//   ActionToolTasksMonitor  — gates kenaz__monitor (drain + watch mode).
	//     Resource UID: Task::"<task-id>". Default-allow (passive read-only);
	//     can be restricted per-session with an explicit forbid rule.
	//   ActionToolTasksCancel   — gates Tasks_Abort RPC + Abort from the Tasks
	//     panel. Resource UID: Task::"<task-id>".
	ActionToolTasksMonitor = "tool.tasks.monitor"
	ActionToolTasksCancel  = "tool.tasks.cancel"

	// ActionExportSession gates the Sessions_Export RPC (session-export
	// mission). Local-only, user-initiated: exports a session transcript
	// to Markdown or JSON on the user's local disk. Default-allow for the
	// local user. Resource UID: Session::"<session-id>".
	ActionExportSession = "session.export"

	// ActionLLMFallback gates every per-turn fallback chain hop
	// (model-fallback-routing-01NDFSEX04 WP03). The connector's retry
	// loop calls CheckLLMFallback before issuing each hop; a Cedar deny
	// causes the loop to surface the primary error unchanged (fail-closed).
	//
	// Resource UID: FallbackChain::"<chain-id>".
	// Default-allow for the local user; operators can deny specific chains
	// or all chains with an explicit forbid rule.
	ActionLLMFallback = "llm.fallback"

	// ActionAuditBulkPurge gates the Audit_BulkPurge RPC
	// (audit-log-enhancement-01KX5R8F WP08). Allows bulk deletion of
	// audit events from the append-only store.
	//
	// Resource UID: AuditLog::"events".
	// Default-forbid — destructive irreversible operation; operators
	// must explicitly permit with a Cedar policy snippet.
	ActionAuditBulkPurge = "audit.bulk_purge"

	// ── ACP envelope action family ────────────────────────────────────────
	// Introduced by mission acp-orchestration-integration-01NDFSEX06.
	// The "acp" family follows the established "<domain>.<operation>"
	// naming convention. Both actions gate byte transmission before any
	// transport call happens.
	//
	//   ActionACPSend    — gates ACP_Dispatch before the envelope is
	//     signed and handed to the transport. Resource UID:
	//     ACPEnvelope::"<peer_id>". Default-permit only to peers at
	//     trust tier ≥ "verified"; deny → no transport call.
	//
	//   ActionACPReceive — gates acceptance of an inbound envelope after
	//     the transport delivers it but before the payload is dispatched
	//     to the skill router. Resource UID: ACPEnvelope::"<peer_id>".
	//     Default-permit from any tier ≥ "pending" with a warning audit
	//     event; deny → envelope dropped.
	ActionACPSend    = "acp_send"
	ActionACPReceive = "acp_receive"

	// ── Context-bootstrap action family ───────────────────────────────────
	// Introduced by mission context-bootstrap-harness-integration (WP05).
	//
	//   ActionContextBootstrapRun — gates StartContextBootstrapRun +
	//     ResumeContextBootstrapRun (the run-dispatch path that reads
	//     connected sources and writes extracted context nodes). Resource
	//     UID: ContextBootstrap::"run". Default-allow for the local user;
	//     operators can forbid to disable the bootstrap engine entirely.
	ActionContextBootstrapRun = "context.bootstrap.run"

	// ── Graph-authoring action family ───────────────────────────────────
	// Introduced by mission model-authored-graphs-01PMGA01 (UNIT-3). A
	// graph-authoring tool is a code-execution primitive, not another
	// config write (spec §4): a graph composes 34 callable node kinds —
	// write_file, tool_dispatch (whatever the tool registry holds),
	// subagent_dispatch, loop/retry/router — so it earns its own action
	// family rather than riding the generic harness-write permit.
	//
	//   ActionGraphAuthor — gates Manager.saveGraph (persisting a draft).
	//     Resource UID: Graph::"<graph-id>". Context carries
	//     authoring_enabled (the FR-006 consent dial, string "true"/
	//     "false"), session_kind, node_kinds (sorted, comma-joined,
	//     de-duplicated kind set — the FR-008 escalation surface), and
	//     node_count. Shipped default: forbid unless authoring_enabled ==
	//     "true"; forbid outright when node_kinds contains "write_file",
	//     regardless of the consent dial.
	//   ActionGraphRun — gates Manager.startRun for a graph carrying the
	//     model-authored marker. Resource UID: Graph::"<graph-id>".
	//     Context carries spec_provenance, session_kind, initiator.
	//     Shipped default: forbid when spec_provenance == "model_authored"
	//     — the human-review interlock (FR-007/FR-010); a human clears the
	//     marker by saving from the editor.
	//
	// Deliberately no ActionGraphDelete: no surface deletes a graph on a
	// model's behalf (spec §3), and register A-0 (2026-08-19, "no
	// deletion lands") freezes the delete lane — an action constant with
	// no gate call would have to be wired or justified with an owner and
	// a date, so the symbol is not created at all.
	ActionGraphAuthor = "graph.author"
	ActionGraphRun    = "graph.run"

	// ActionBundleInstall gates Bundle_Install
	// (bundle-download-and-verify-01PMZ909, UNIT-7, spec §1.7 F-3).
	// Bundle_Install (core/rpc/bindings.go) consulted no policy gate at
	// all before this — harmless while Install only registered a
	// lockfile pointer, but UNIT-6's http_mirror turns it into a
	// caller-supplied-URL fetch primitive. Consulted BEFORE any channel
	// is opened (spec §5.6: "before any fetch"), so the resource UID is
	// built from the install locator (path or URL) the caller supplied,
	// NOT the bundle name — the manifest that would reveal the name
	// hasn't been fetched yet at gate-check time. Resource UID:
	// Bundle::"<kind>:<locator>". Shipped default: permit the local
	// user (default_bundle_policy.cedar) — E-001 (spec §12) leaves the
	// channel-allowlist question to the operator; this constant and its
	// gate call are the plumbing, not the policy.
	ActionBundleInstall = "bundle.install"
	// ActionApprovalResolve gates Graph_ResolveApproval
	// (approval-node-01PMZC12 UNIT-3, FR-003). A human approval is
	// trust- or compliance-relevant under CLAUDE.md's own rubric; an
	// approval nobody can audit is barely better than one nobody made.
	//
	// Resource UID: Approval::"<runID>:<nodeID>". Default-allow (a
	// missing policy does not block a human's own resolution) — this is
	// NOT the fail-closed no-watcher timeout, which resolves
	// server-side without going through this gate at all (spec.md §5.3;
	// the gate exists for the human-verb path, not the safety path).
	ActionApprovalResolve = "approval.resolve"

	// ── ContextSync purge action family ────────────────────────────────────
	// Introduced by mission fleet-enforcement-truth-01PMZ505 (WP13, owner
	// ruling G-7, docs/escalation-register-2026-08-19.md Part 10: "gate the
	// DESTRUCTIVE operations now — purge and delete — fail-closed").
	//
	// Before this family existed, SessionSync_DeleteRemote /
	// ProjectSync_DeleteRemote (core/rpc/views/contextsync) reached
	// core/fleet's SessionSyncer.DeleteRemote / ProjectSyncer.DeleteRemote —
	// which irreversibly purge every fleet event for the session/project and
	// disable sync — through 21 non-test call sites with zero Cedar action
	// constants and zero gate calls anywhere in the chain. No policy author
	// could write a rule against this surface at all.
	//
	// Deliberately narrow: only the two DESTRUCTIVE operations (purge/
	// delete) are gated. SessionSync_Toggle / ProjectSync_Toggle (enable,
	// disable) and SessionSync_ResumeFrom (replay) are NOT covered by this
	// family — ruling G-7 explicitly leaves toggle/resume ungated for now,
	// a recorded, dated gap rather than one silently closed by extension.
	//
	//   ActionContextSyncSessionPurge — gates SessionSync_DeleteRemote.
	//     Resource UID: Session::"<session-id>" (reuses EntityTypeSession /
	//     SessionUID — session-export-01NDFSEX05's family; a different
	//     action against the same resource type).
	//   ActionContextSyncProjectPurge — gates ProjectSync_DeleteRemote.
	//     Resource UID: Project::"<project-id>".
	//
	// Both gate-hook helpers (CheckContextSyncSessionPurge /
	// CheckContextSyncProjectPurge, hooks.go) deliberately do NOT delegate
	// to the shared enforce() (which maps NotApplicable to nil —
	// default-allow). They require an explicit Cedar Allow; Deny AND
	// NotApplicable both refuse the purge. See
	// policies/default_context_sync_policy.cedar for the shipped permit
	// that keeps the existing (pre-gate) local-user purge UX working, and
	// hooks.go's doc comment for why the fail-closed wrapper exists.
	ActionContextSyncSessionPurge = "context_sync.session.purge"
	ActionContextSyncProjectPurge = "context_sync.project.purge"
)

// Entity-type names mirror spec §4.10's recommended mapping:
//
//	Tool::"<server>__<tool>"
//	Model::"<provider>:<id>"
//	Network::"<host>"
//	Filesystem::"<path>"
//	Memory::"<scope>"
//	User::"local" (single-user invariant)
const (
	EntityTypeTool       = "Tool"
	EntityTypeModel      = "Model"
	EntityTypeNetwork    = "Network"
	EntityTypeFilesystem = "Filesystem"
	EntityTypeMemory     = "Memory"
	EntityTypeUser       = "User"
	EntityTypeAction     = "Action"

	// EntityTypeStateSource and EntityTypeStateTarget back the FR-058b
	// finer-grained read/write gating. Resource IDs match the State
	// archetype's `source` / `target` enums (e.g. "file", "bash_output",
	// "history" for read; "file", "artifact", "trace" for write).
	EntityTypeStateSource = "State"
	EntityTypeStateTarget = "State"

	// Entity-type names introduced by mission
	// cedar-credential-policy-01KQ8TDE (WP01). The Tool entity already
	// exists from the agent-kernel-graph mission (chat-runner gate);
	// the new families add Credential, BashCommand, and FilesystemOp.
	EntityTypeCredential   = "Credential"
	EntityTypeBashCommand  = "BashCommand"
	EntityTypeFilesystemOp = "FilesystemOp"

	// EntityTypeMCPRecipe is the Cedar entity type for MCP recipe resources.
	// Introduced by mission mcp-server-install-01KQ8TDP (WP10).
	// Resource UIDs take the shape MCPRecipe::"<recipe-id>".
	EntityTypeMCPRecipe = "MCPRecipe"

	// EntityTypeWorkflow is the Cedar entity type for workflow
	// definitions. Introduced by mission workflows-01KQ8TDG (WP11).
	// Resource UIDs take the shape Workflow::"<workflow-id>".
	EntityTypeWorkflow = "Workflow"

	// EntityTypeTodoList is the Cedar entity type for the session-scoped
	// todo list. Introduced by mission
	// builtin-tools-search-and-elicitation-01KZNP3D (WP05).
	// Resource UIDs take the shape TodoList::"session".
	EntityTypeTodoList = "TodoList"

	// EntityTypeElicitation is the Cedar entity type for the
	// ask-user-question elicitation surface (mission
	// ask-user-question-interactive-01KZNP3G WP01). Resource UIDs take
	// the shape Elicitation::"<kind>" where kind is one of the seven
	// question kinds: radio, checkbox, text, number, slider, date, file.
	EntityTypeElicitation = "Elicitation"

	// EntityTypeSkill is the Cedar entity type for model-invokable user
	// skills. Introduced by mission model-invoked-skills-catalog-01KZNP3E.
	// Resource UIDs take the shape Skill::"<command-name>" where command-name
	// is the bare slash command token (e.g. "summarize", "daily-standup").
	EntityTypeSkill = "Skill"

	// EntityTypeScheduledChatRun is the Cedar entity type for scheduled
	// chat runs. Introduced by mission scheduled-chat-runs-01KX5R8B (WP03).
	// Resource UIDs take the shape ScheduledChatRun::"<run-id>".
	EntityTypeScheduledChatRun = "ScheduledChatRun"

	// EntityTypeSecretReference is the Cedar entity type for model-side
	// secret references. Introduced by mission
	// model-secret-references-01KW7M5A (WP02).
	// Resource UIDs take the shape SecretReference::"<locator>" where locator
	// is the keychain locator (e.g. "user:example-api-token").
	EntityTypeSecretReference = "SecretReference"

	// EntityTypeSession is the Cedar entity type for chat sessions.
	// Introduced by mission session-export-01NDFSEX05 (WP01).
	// Resource UIDs take the shape Session::"<session-id>".
	EntityTypeSession = "Session"

	// EntityTypeFallbackChain is the Cedar entity type for LLM fallback
	// chains. Introduced by mission model-fallback-routing-01NDFSEX04 (WP03).
	// Resource UIDs take the shape FallbackChain::"<chain-id>".
	EntityTypeFallbackChain = "FallbackChain"

	// EntityTypeAuditLog is the Cedar entity type for the audit log store.
	// Introduced by mission audit-log-enhancement-01KX5R8F (WP08).
	// Resource UIDs take the shape AuditLog::"events" (currently a singleton).
	EntityTypeAuditLog = "AuditLog"

	// EntityTypeACPEnvelope is the Cedar entity type for ACP peer envelopes.
	// Introduced by mission acp-orchestration-integration-01NDFSEX06.
	// Resource UIDs take the shape ACPEnvelope::"<peer_id>" — one entity
	// per peer so policy authors can target individual peers.
	//
	// Relevant attributes available on the entity at eval time:
	//   peer_id        — string: the peer's stable identifier
	//   peer_trust_tier — string: "pending" | "verified" | "revoked"
	//   transport      — string: "uds" | "http_loopback" | "http_lan"
	//   direction      — string: "send" | "receive"
	EntityTypeACPEnvelope = "ACPEnvelope"

	// EntityTypeContextBootstrap is the Cedar entity type for the
	// context-bootstrap run resource. Introduced by mission
	// context-bootstrap-harness-integration (WP05). Resource UIDs take the
	// shape ContextBootstrap::"run" (a singleton — one bootstrap engine).
	EntityTypeContextBootstrap = "ContextBootstrap"

	// EntityTypeGraph is the Cedar entity type for agent-graph resources.
	// Introduced by mission model-authored-graphs-01PMGA01 (UNIT-3).
	// Resource UIDs take the shape Graph::"<graph-id>".
	EntityTypeGraph = "Graph"

	// EntityTypeBundle is the Cedar entity type for the bundle-install
	// resource. Introduced by mission
	// bundle-download-and-verify-01PMZ909 (UNIT-7). Resource UIDs take
	// the shape Bundle::"<kind>:<locator>" — see BundleUID and
	// ActionBundleInstall's doc for why the locator (not the not-yet-
	// fetched bundle name) is the identity the gate checks against.
	EntityTypeBundle = "Bundle"
	// EntityTypeApproval is the Cedar entity type for a graph run's
	// approval-node decision. Introduced by mission
	// approval-node-01PMZC12 (UNIT-3). Resource UIDs take the shape
	// Approval::"<runID>:<nodeID>".
	EntityTypeApproval = "Approval"

	// EntityTypeProject is the Cedar entity type for a harness project.
	// Introduced by mission fleet-enforcement-truth-01PMZ505 (WP13, owner
	// ruling G-7) for the ContextSync purge action family. Resource UIDs
	// take the shape Project::"<project-id>".
	EntityTypeProject = "Project"

	// PrincipalLocal is the canonical EntityUID id for the single
	// local user. The harness is single-user / privacy-first
	// (NFR-005); the policy surface is built around this invariant.
	PrincipalLocal = "local"
)

// Outcome is a closed enum mirroring Cedar's allow/deny but with an
// explicit "not applicable" state used when no policy matches AND the
// engine's DefaultDeny flag is false. Default-allow is the spec's
// stance ("observable, not blocking by default; user opts in to
// fail-closed"). Frontends pattern-match on this value.
type Outcome int

const (
	// Allow — at least one permit policy matched and no forbid policy.
	Allow Outcome = iota
	// Deny — at least one forbid policy matched OR no policy matched
	// AND DefaultDeny is true.
	Deny
	// NotApplicable — no policy matched AND DefaultDeny is false.
	// Callers treat this as "allow with audit" by default.
	NotApplicable
)

// String renders Outcome for logs and audit lines.
func (o Outcome) String() string {
	switch o {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	case NotApplicable:
		return "not_applicable"
	default:
		return "unknown"
	}
}

// MarshalJSON renders Outcome as its String() form ("allow" / "deny" /
// "not_applicable") rather than the bare int the iota otherwise encodes
// to. This is the doc comment above's "Frontends pattern-match on this
// value" made true: RecentDecisions crosses the RPC boundary as JSON,
// and every frontend consumer (frontend/src/lib/types.ts's
// PolicyDecision.outcome, consumed by the WP06 denial panel) declares a
// STRING union, not a number — before this method existed the wire
// value was a plain 0/1/2 and every such consumer would have silently
// mismatched (found while wiring consent-surfaces-truth-01PMTR01 WP06,
// the first real frontend reader of this field).
func (o Outcome) MarshalJSON() ([]byte, error) {
	return json.Marshal(o.String())
}

// UnmarshalJSON is the inverse of MarshalJSON — accepts the string form.
// Kept alongside MarshalJSON so Outcome round-trips through JSON in
// either direction (tests, and any future consumer that decodes a
// Decision back into Go).
func (o *Outcome) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	switch s {
	case "allow":
		*o = Allow
	case "deny":
		*o = Deny
	case "not_applicable":
		*o = NotApplicable
	default:
		return fmt.Errorf("cedar: unknown Outcome %q", s)
	}
	return nil
}

// ToolUID builds a Cedar EntityUID for a tool reference, using the
// kenaz-harness "<server>__<tool>" convention. server may be empty
// for first-party tools (websearch, bash) — those are stored as
// "builtin__<tool>" so the entity space stays uniform.
func ToolUID(server, tool string) cedar.EntityUID {
	id := tool
	if server == "" {
		server = "builtin"
	}
	id = server + "__" + tool
	return cedar.NewEntityUID(EntityTypeTool, cedar.String(id))
}

// ModelUID builds a Cedar EntityUID for a "<provider>:<id>" model
// reference. provider is e.g. "openai", "anthropic"; modelID is the
// adapter-internal id like "gpt-4o" or "claude-sonnet-4".
func ModelUID(provider, modelID string) cedar.EntityUID {
	return cedar.NewEntityUID(
		EntityTypeModel,
		cedar.String(provider+":"+modelID),
	)
}

// NetworkUID builds a Cedar EntityUID for a network host. The host
// is lowercased and stripped of any trailing dot to keep the entity
// space deterministic.
func NetworkUID(host string) cedar.EntityUID {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return cedar.NewEntityUID(EntityTypeNetwork, cedar.String(host))
}

// FilesystemUID builds a Cedar EntityUID for a filesystem path.
// Callers SHOULD pass an absolute, clean path (filepath.Clean +
// filepath.Abs) so policy match results are deterministic.
func FilesystemUID(path string) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeFilesystem, cedar.String(path))
}

// MemoryUID builds a Cedar EntityUID for a memory scope. scope is one
// of "global", "project", or "session" per FR-029.
func MemoryUID(scope string) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeMemory, cedar.String(scope))
}

// StateSourceUID builds a Cedar EntityUID for a State `read` source
// class. source is one of "history", "corpus", "memory", "attachment",
// "file", "bash_output" — matching the `source:` enum on the read
// archetype (FR-058b).
func StateSourceUID(source string) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeStateSource, cedar.String(source))
}

// StateTargetUID builds a Cedar EntityUID for a State `write` target
// class. target is one of "memory", "corpus", "trace", "file",
// "artifact" — matching the `target:` enum on the write archetype
// (FR-058b).
func StateTargetUID(target string) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeStateTarget, cedar.String(target))
}

// UserUID returns the canonical local-user principal.
func UserUID() cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeUser, cedar.String(PrincipalLocal))
}

// ActionUID builds a Cedar Action EntityUID. The string MUST be one
// of the Action* constants in this package; unknown strings still
// produce a valid UID but match nothing in the default policy.
func ActionUID(name string) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeAction, cedar.String(name))
}

// invalidUIDID is the canonical replacement-id substituted when one of
// the family-aware UID constructors below rejects malformed input. The
// resource-type stays unchanged so the policy bundle's `resource is
// <T>` clauses keep type-matching, but the literal value is something
// no realistic call site would ever match — a typo therefore never
// silently authorises against a permit policy.
const invalidUIDID = "invalid"

// validateFamilyID checks a resource-id fragment is safe to embed in a
// Cedar EntityUID literal for the WP01 resource families. The four
// invariants below mirror the spec's threat model (FR-009, FR-017):
//
//   - Empty strings collapse the family namespace ("Tool::\"\"" matches
//     no realistic call site, but a typo here would silently bypass
//     intended denies).
//   - Control characters and the NUL byte are rejected because they
//     can corrupt log lines, audit records, and downstream consumers
//     that assume printable IDs.
//   - Leading `..` is rejected to prevent path-traversal-style abuse
//     of the FilesystemOp / BashCommand UIDs (and to harmonise with
//     the spec's `..`-rejection rule for FilesystemOp paths).
//
// The function returns true when id is acceptable. Callers use the
// boolean to swap in invalidUIDID without forfeiting the family type.
func validateFamilyID(id string) bool {
	if id == "" {
		return false
	}
	if strings.HasPrefix(id, "..") {
		return false
	}
	for _, r := range id {
		if r == 0 {
			return false
		}
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// CredentialUID builds a Cedar EntityUID for the Credential family
// introduced in WP01. The id encodes the provider id and the access
// purpose using the spec §3 "<provider-id>::<purpose>" shape (e.g.
// "openai::provider_call"). Callers SHOULD pass a non-empty provider
// and a member of the credstore.AccessPurpose enum; malformed input
// (empty / control chars / leading "..") is replaced with the literal
// "invalid" so the resulting UID type-matches in policy `is Credential`
// clauses but never satisfies any real permit.
func CredentialUID(provider, purpose string) cedar.EntityUID {
	id := provider + "::" + purpose
	if !validateFamilyID(provider) || !validateFamilyID(purpose) {
		id = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeCredential, cedar.String(id))
}

// BashCommandUID builds a Cedar EntityUID for the BashCommand family.
// pattern is the derived "argv[0] [argv[1]?]" pattern from FR-014
// (e.g. "git status", "ls", "run.sh"). Pattern derivation lives in
// `core/tools/bash/pattern.go` (WP03); this constructor only validates
// and embeds. Empty / control-char / leading-".." patterns are
// replaced with the literal "invalid".
func BashCommandUID(pattern string) cedar.EntityUID {
	id := pattern
	if !validateFamilyID(pattern) {
		id = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeBashCommand, cedar.String(id))
}

// FilesystemOpUID builds a Cedar EntityUID for the FilesystemOp family.
// canonicalPath is the `filepath.Abs` + `filepath.Clean`'d path from
// FR-017 (e.g. "/Users/alec/projects/kenaz/main.go"). Callers MUST
// canonicalise the path before invoking this constructor; the
// constructor itself only enforces the universal "no empty / no
// control / no leading .." invariant — it does NOT re-canonicalise so
// the caller's traversal-rejection layer keeps a single source of
// truth.
func FilesystemOpUID(canonicalPath string) cedar.EntityUID {
	id := canonicalPath
	if !validateFamilyID(canonicalPath) {
		id = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeFilesystemOp, cedar.String(id))
}

// PermissionToolUID builds a Cedar EntityUID for the Tool family in
// the WP01 universal-permission shape. toolName is the fully qualified
// "<server>__<tool>" name (e.g. "kenaz__bash", "filesystem__write_file").
// This constructor differs from the older ToolUID(server, tool) helper
// — that one composes the id from two arguments and is kept for
// backwards compatibility with the agent-kernel-graph chat-runner
// gate. New gate sites that already have the canonical fully-qualified
// name (mcp registry, recipe metadata) call PermissionToolUID directly
// to avoid re-splitting + re-joining.
func PermissionToolUID(toolName string) cedar.EntityUID {
	id := toolName
	if !validateFamilyID(toolName) {
		id = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeTool, cedar.String(id))
}

// WorkflowUID builds a Cedar EntityUID for the Workflow family
// introduced by mission workflows-01KQ8TDG (WP11). id is the workflow's
// canonical identifier (e.g. "wf-abc123", "summarize-code"). Malformed
// ids (empty / control characters / leading "..") are replaced with the
// literal "invalid" so the resulting UID type-matches in `resource is
// Workflow` clauses but never satisfies any real permit.
func WorkflowUID(id string) cedar.EntityUID {
	safeID := id
	if !validateFamilyID(id) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeWorkflow, cedar.String(safeID))
}

// GraphUID builds a Cedar EntityUID for the Graph family introduced by
// mission model-authored-graphs-01PMGA01 (UNIT-3). id is the graph's
// canonical identifier (e.g. "my_graph", "chat_default"). Malformed ids
// (empty / control characters / leading "..") are replaced with the
// literal "invalid" so the resulting UID type-matches in `resource is
// Graph` clauses but never satisfies any real permit.
func GraphUID(id string) cedar.EntityUID {
	safeID := id
	if !validateFamilyID(id) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeGraph, cedar.String(safeID))
}

// MCPRecipeUID builds a Cedar EntityUID for the MCPRecipe family
// introduced by mission mcp-server-install-01KQ8TDP (WP10). id is the
// recipe's canonical identifier (e.g. "github", "sqlite", "my-custom").
//
// Malformed ids (empty / control characters / leading "..") are replaced
// with the literal "invalid" so the resulting UID type-matches in
// `resource is MCPRecipe` clauses but never satisfies any real permit —
// a typo therefore never silently authorises.
func MCPRecipeUID(id string) cedar.EntityUID {
	safeID := id
	if !validateFamilyID(id) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeMCPRecipe, cedar.String(safeID))
}

// TodoListUID builds a Cedar EntityUID for the TodoList family introduced
// by mission builtin-tools-search-and-elicitation-01KZNP3D (WP05). scope
// is the list's scope discriminator — "session" for the current session's
// list (the only scope in v1). Malformed scopes (empty / control characters
// / leading "..") are replaced with "invalid" so the resulting UID
// type-matches in `resource is TodoList` clauses but never satisfies any
// real permit.
func TodoListUID(scope string) cedar.EntityUID {
	safeScope := scope
	if !validateFamilyID(scope) {
		safeScope = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeTodoList, cedar.String(safeScope))
}

// ElicitationUID builds a Cedar EntityUID for the Elicitation family
// introduced by mission ask-user-question-interactive-01KZNP3G (WP01).
// kind is one of the seven question kinds (radio, checkbox, text, number,
// slider, date, file). Malformed kinds (empty / control characters /
// leading "..") are replaced with the literal "invalid" so the resulting
// UID type-matches in `resource is Elicitation` clauses but never
// satisfies any real permit — a typo therefore never silently authorises.
func ElicitationUID(kind string) cedar.EntityUID {
	safeID := kind
	if !validateFamilyID(kind) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeElicitation, cedar.String(safeID))
}

// SkillUID builds a Cedar EntityUID for the Skill family introduced by
// mission model-invoked-skills-catalog-01KZNP3E. name is the bare command
// name (the token after the slash, e.g. "summarize", "daily-standup").
// Malformed names (empty / control characters / leading "..") are replaced
// with the literal "invalid" so the resulting UID type-matches in
// `resource is Skill` clauses but never satisfies any real permit — a typo
// therefore never silently authorises a skill invocation.
func SkillUID(name string) cedar.EntityUID {
	safeID := name
	if !validateFamilyID(name) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeSkill, cedar.String(safeID))
}

// ApprovalUID builds a Cedar EntityUID for a graph run's approval-node
// decision (approval-node-01PMZC12 UNIT-3). id is "<runID>:<nodeID>".
// Malformed ids are replaced with "invalid" so the resulting UID never
// satisfies a real permit.
func ApprovalUID(runID, nodeID string) cedar.EntityUID {
	id := runID + ":" + nodeID
	if !validateFamilyID(id) {
		id = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeApproval, cedar.String(id))
}

// ElicitTemplateActionID returns the Cedar action ID for a named template
// (ask-user-question-interactive-01KZNP3G WP07). The full action ID is
// ActionElicitTemplatePrefix + templateName. Malformed names are replaced with
// "invalid" so the resulting action ID never satisfies a real permit.
func ElicitTemplateActionID(templateName string) string {
	if !validateFamilyID(templateName) {
		return ActionElicitTemplatePrefix + invalidUIDID
	}
	return ActionElicitTemplatePrefix + templateName
}

// ScheduledChatRunUID builds a Cedar EntityUID for the ScheduledChatRun
// family introduced by mission scheduled-chat-runs-01KX5R8B (WP03).
// id is the scheduled_chat_runs.id primary key. Malformed ids are replaced
// with "invalid" so the resulting UID never satisfies a real permit.
func ScheduledChatRunUID(id string) cedar.EntityUID {
	safeID := id
	if !validateFamilyID(id) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeScheduledChatRun, cedar.String(safeID))
}

// SecretReferenceUID builds a Cedar EntityUID for the SecretReference family
// introduced by mission model-secret-references-01KW7M5A (WP02). locator is
// the keychain locator string (e.g. "user:example-api-token",
// "provider:bedrock-prod"). The locator must not be empty and must not
// contain control characters; malformed values are replaced with the literal
// "invalid" so the resulting UID type-matches in `resource is SecretReference`
// clauses but never satisfies any real permit — a typo therefore never
// silently authorises a resolution.
func SecretReferenceUID(locator string) cedar.EntityUID {
	safeID := locator
	if !validateFamilyID(locator) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeSecretReference, cedar.String(safeID))
}

// SessionUID builds a Cedar EntityUID for the Session family introduced by
// mission session-export-01NDFSEX05 (WP01). id is the session's primary-key
// identifier. Malformed ids (empty / control characters / leading "..") are
// replaced with the literal "invalid" so the resulting UID type-matches in
// `resource is Session` clauses but never satisfies any real permit.
func SessionUID(id string) cedar.EntityUID {
	safeID := id
	if !validateFamilyID(id) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeSession, cedar.String(safeID))
}

// FallbackChainUID builds a Cedar EntityUID for the FallbackChain family
// introduced by mission model-fallback-routing-01NDFSEX04 (WP03). id is the
// chain's canonical slug identifier (e.g. "anthropic-with-openrouter-fallback").
// Malformed ids (empty / control characters / leading "..") are replaced with
// the literal "invalid" so the resulting UID type-matches in
// `resource is FallbackChain` clauses but never satisfies any real permit.
func FallbackChainUID(id string) cedar.EntityUID {
	safeID := id
	if !validateFamilyID(id) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeFallbackChain, cedar.String(safeID))
}

// AuditLogUID builds a Cedar EntityUID for the AuditLog family introduced
// by mission audit-log-enhancement-01KX5R8F (WP08). The audit log is a
// singleton; the canonical id is "events". Callers should pass "events"
// or a specific log segment id; malformed ids are replaced with "invalid".
func AuditLogUID(id string) cedar.EntityUID {
	safeID := id
	if !validateFamilyID(id) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeAuditLog, cedar.String(safeID))
}

// ACPEnvelopeUID builds a Cedar EntityUID for the ACPEnvelope family
// introduced by mission acp-orchestration-integration-01NDFSEX06.
// peerID is the peer's stable identifier. Malformed ids (empty / control
// characters / leading "..") are replaced with the literal "invalid" so
// the resulting UID type-matches in `resource is ACPEnvelope` clauses
// but never satisfies any real permit — a typo therefore never silently
// authorises an ACP envelope exchange.
func ACPEnvelopeUID(peerID string) cedar.EntityUID {
	safeID := peerID
	if !validateFamilyID(peerID) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeACPEnvelope, cedar.String(safeID))
}

// ContextBootstrapUID builds a Cedar EntityUID for the context-bootstrap run
// resource introduced by mission context-bootstrap-harness-integration (WP05).
// The bootstrap engine is a singleton; the canonical id is "run". Malformed
// ids are replaced with the literal "invalid".
func ContextBootstrapUID(id string) cedar.EntityUID {
	safeID := id
	if !validateFamilyID(id) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeContextBootstrap, cedar.String(safeID))
}

// BundleUID builds a Cedar EntityUID for the Bundle family introduced by
// mission bundle-download-and-verify-01PMZ909 (UNIT-7). id is the
// channel-qualified install locator the caller supplied — "<kind>:<path>"
// or "<kind>:<url>" — NOT the bundle's own name, which is only known
// after the manifest is fetched (and this gate is deliberately consulted
// before that fetch, spec §5.6). Malformed ids are replaced with the
// literal "invalid" so the resulting UID type-matches in
// `resource is Bundle` clauses but never satisfies any real permit.
func BundleUID(id string) cedar.EntityUID {
	safeID := id
	if !validateFamilyID(id) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeBundle, cedar.String(safeID))
}

// ProjectUID builds a Cedar EntityUID for the Project family introduced by
// mission fleet-enforcement-truth-01PMZ505 (WP13, owner ruling G-7). id is
// the project's canonical identifier. Malformed ids (empty / control
// characters / leading "..") are replaced with the literal "invalid" so the
// resulting UID type-matches in `resource is Project` clauses but never
// satisfies any real permit.
func ProjectUID(id string) cedar.EntityUID {
	safeID := id
	if !validateFamilyID(id) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeProject, cedar.String(safeID))
}
