// default_workflows_policy.cedar — Workflow family default bundle
// for mission `workflows-01KQ8TDG` (WP11).
//
// Resource family: `Workflow::"<workflow-id>"`
// Actions:
//   `Action::"workflow.run"`    — dispatches a workflow
//   `Action::"workflow.save"`   — persists a workflow definition
//   `Action::"workflow.delete"` — removes a workflow definition
//
// Context attrs (workflow.run / workflow.save):
//   `mode`        (string) — "permissive" | "strict". Defaults to
//                            "permissive". The chassis sends "strict"
//                            when `cedarStrictWorkflowMode` is true in
//                            the harness settings file, read live on
//                            every run/save via
//                            workflowsview.Config.CedarModeFn.
//                            There is no Settings-panel dial for it
//                            yet — that is a tracked follow-up
//                            (docs/unwired-ledger.md, 2026-08-16).
//   `step_kinds`  (string) — comma-joined sorted list of step kinds in
//                            the workflow (e.g. "model_turn,shell").
//                            Used by `like` rules below to spot
//                            shell-bearing workflows in strict mode.
//
// Default posture (spec WP11):
//   - workflow.run    → permit in permissive mode (default).
//                       In strict mode the rule below leaves the
//                       evaluation as NotApplicable so the chassis
//                       fires an interactive prompt before dispatch.
//   - workflow.save   → permit by default; deny in strict mode when
//                       the workflow contains a `shell` step (the
//                       privacy-sensitive surface — strict users want
//                       an explicit policy file before persisting one).
//   - workflow.delete → permit by default with audit. The chassis
//                       always emits `workflow.deleted`; strict users
//                       can layer a forbid rule on top.

// --- run ----------------------------------------------------------------
// Permissive: any workflow may run.
permit (
    principal == User::"local",
    action == Action::"workflow.run",
    resource is Workflow
) when {
    context.mode == "permissive"
};

// --- save ---------------------------------------------------------------
// Permissive default: any workflow may be saved.
permit (
    principal == User::"local",
    action == Action::"workflow.save",
    resource is Workflow
) when {
    context.mode == "permissive"
};

// Strict: workflows that include a `shell` step are denied at save time.
// `step_kinds` is the comma-joined sorted kind list; `like "*shell*"`
// catches it without forcing the policy author to enumerate every
// possible neighbour kind.
forbid (
    principal == User::"local",
    action == Action::"workflow.save",
    resource is Workflow
) when {
    context.mode == "strict" &&
    context.step_kinds like "*shell*"
};

// --- delete -------------------------------------------------------------
// Allow-with-audit: every delete is permitted by default. The chassis
// emits `workflow.deleted` regardless; strict overrides layer a forbid
// rule on top.
permit (
    principal == User::"local",
    action == Action::"workflow.delete",
    resource is Workflow
);

// --- network.fetch (WP05, strict arm added by automation-actually-runs-
// --- 01PMZ404 UNIT-7) ----------------------------------------------------
// Actions:
//   `Action::"workflow.network.fetch"` — any web_fetch / web_scrape step
//
// Permissive default: all outbound fetches are permitted.
permit (
    principal == User::"local",
    action == Action::"workflow.network.fetch",
    resource is Workflow
) when {
    context.mode == "permissive"
};

// Strict: deny by default. Absence-only ("the permit's `when` just
// doesn't match") is NOT enforcement — core/policy/cedar/hooks.go's
// enforce() maps NotApplicable to nil (proceed), the same outcome as
// Allow. Without this explicit forbid, wiring a NetworkAuthorizer
// adapter cannot make strict mode deny anything: the fetch always
// proceeds regardless of the adapter's own logic (this is the fifth
// appearance of the enforce-treats-NotApplicable-as-allow shape found
// in this campaign — see docs/unwired-ledger.md). The policy file is
// where this posture belongs, not a bespoke
// NotApplicable-means-deny branch duplicated in every adapter.
forbid (
    principal == User::"local",
    action == Action::"workflow.network.fetch",
    resource is Workflow
) when {
    context.mode == "strict"
};
