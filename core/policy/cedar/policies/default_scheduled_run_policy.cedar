// default_scheduled_run_policy.cedar — shipped default for the
// `tool.scheduled_run.*` action family (model-scheduled-jobs-01PMSJ01
// WP09, FR-005).
//
// Owner ruling B-3 (2026-08-19,
// docs/escalation-register-2026-08-19.md:1314): "PERMIT ONLY WITHIN A
// TOOL ALLOWLIST." A model-created scheduled chat run
// (context.created_by == "model") may execute unattended — the ruling
// removes the human review moment that would otherwise gate it — but
// ONLY when its row carries a non-empty tool allowlist declared at
// creation time (context.has_tool_allowlist == true, sourced from
// scheduled_chat_runs.tool_allowlist, migration sessions/0340). This
// rule decides only WHETHER the run may start. What the started run may
// call is enforced per tool call, not here: the dispatcher binds the
// run's session to the allowlist (scheduler.ResolveRunContainment) and
// the session arm of the merged tool-permission resolver
// (core/rpc/harness_session_kind_resolver.go, via
// ScheduledRunContainmentRegistry) denies and records every off-list
// call (model-harness-toolset-01MHTS001 WP02, finding H-1 — until that
// WP nothing enforced it, although this comment said a seam did).
//
// THIS FILE'S PRESENCE IS WHAT MAKES THE FAIL-SAFE NON-VACUOUS.
// core/policy/cedar/hooks.go's GateScheduledChatExecute does NOT use
// this package's shared enforce() (which maps NotApplicable to
// Allow — "default-allow") when context.created_by == "model": it
// requires an explicit Allow outcome, so Deny AND NotApplicable both
// refuse. This file is the ONLY rule in the shipped bundle that can
// produce that Allow. Delete this file and every model-created
// schedule refuses to execute, full stop — that is the required
// behaviour (spec.md §6.1, AC-011b), not a regression.
//
// User-created schedules (context.created_by == "user", or absent on a
// pre-migration/legacy row) are UNAFFECTED by this file: no rule here
// mentions them, so GateScheduledChatExecute's ordinary enforce()
// (NotApplicable -> Allow) continues to govern them exactly as it did
// before this WP — this file narrows nothing for the human-authored
// case.
permit (
    principal == User::"local",
    action == Action::"tool.scheduled_run.execute",
    resource is ScheduledChatRun
) when {
    context.created_by == "model" &&
    context.has_tool_allowlist == true
};
