// default_session_export_policy.cedar — Session export default policy
// for mission `session-export-01NDFSEX05` (WP01).
//
// Resource family: `Session::"<session-id>"`
// Actions:
//   `Action::"session.export"` — exports a session transcript to disk
//                                (Markdown or JSON). Local-only,
//                                user-initiated, explicit.
//
// Default posture: permit for the local user (default-allow, single-user
// harness). Operators who want to disable export entirely can layer a
// forbid rule in their own policy file — e.g. in a kiosk / shared
// session environment.

permit (
    principal == User::"local",
    action == Action::"session.export",
    resource is Session
);
