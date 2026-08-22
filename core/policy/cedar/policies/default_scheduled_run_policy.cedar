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
// scheduled_chat_runs.tool_allowlist, migration sessions/0340). Per-run
// enforcement of that allowlist against the live tool catalogue is a
// separate seam (harness-self-attach-01PMHS01's merged
// PermissionResolver); this rule is the policy-level half of the
// fail-safe, not the whole of it.
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
