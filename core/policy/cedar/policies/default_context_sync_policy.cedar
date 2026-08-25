// default_context_sync_policy.cedar — ContextSync purge default policy
// for mission fleet-enforcement-truth-01PMZ505 (WP13, owner ruling G-7,
// docs/escalation-register-2026-08-19.md Part 10).
//
// Resource families:
//   Session::"<session-id>" — SessionSync_DeleteRemote
//   Project::"<project-id>" — ProjectSync_DeleteRemote
// Actions:
//   Action::"context_sync.session.purge" — purges every fleet event for a
//     session and disables sync (irreversible — core/fleet/session_sync.go
//     DeleteRemote's own doc).
//   Action::"context_sync.project.purge" — same, for a project.
//
// Default posture: permit the local user to purge THEIR OWN uploaded
// fleet data. This is the pre-existing v0.71.0 "Purge remote" UX (spec
// fleet-enforcement-truth-01PMZ505 §5.12, goal G11: "a user can withdraw
// what they published, and can purge what they uploaded") — this policy
// keeps it working, it does not newly restrict it. Unlike
// default_audit_bulk_purge_policy.cedar (which forbids by default because
// bulk-purging the shared audit LOG is an integrity/compliance concern),
// purging a user's OWN synced session/project stream is squarely
// something the local user should be able to do without an operator
// opt-in.
//
// THIS FILE'S PRESENCE IS WHAT MAKES THE FAIL-CLOSED GATE NON-VACUOUS FOR
// THE ORDINARY CASE. core/policy/cedar/hooks.go's
// CheckContextSyncSessionPurge / CheckContextSyncProjectPurge do NOT use
// the package's shared enforce() (which maps NotApplicable to Allow) —
// they require an explicit Allow outcome, so Deny AND NotApplicable both
// refuse the purge. Delete this file (or replace it) and every purge
// refuses, full stop — that is ruling G-7's fail-closed intent, not a
// regression. An operator who wants to lock this surface down further
// layers their own forbid rule on top; it wins over this permit exactly
// as Cedar's forbid-overrides-permit semantics dictate.
permit (
    principal == User::"local",
    action == Action::"context_sync.session.purge",
    resource is Session
);

permit (
    principal == User::"local",
    action == Action::"context_sync.project.purge",
    resource is Project
);
