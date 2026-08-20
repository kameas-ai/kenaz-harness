// graph_run_unreviewed_forbid.cedar — the FR-007/FR-010 human-review
// interlock. Mission model-authored-graphs-01PMGA01 (UNIT-3).
//
// A graph whose spec_provenance is "model_authored" (the UNIT-5 marker,
// stamped server-side and overwritten on every model-initiated save —
// never accepted from submitted YAML) must not run until a human opens
// it on the canvas and saves it, which clears the marker. Enforcement
// lives at Manager.startRun (UNIT-4's GateGraphRun call); this is the
// policy half.
//
// Context attributes set by GateGraphRun:
//   context.spec_provenance — "" | "library_fallback" | "model_authored"
//   context.session_kind    — carried for future policy authoring.
//   context.initiator       — "user" | "model"; today's only production
//                              initiator is "user" (the Graphs view's
//                              Run button — no model-initiated run
//                              exists in this mission, FR-007).
//
// This is a control over UI-initiated runs, not a containment mechanism
// for unattended execution — no unattended graph execution exists to
// contain (EnvDeps has no LLM/Tools field; every Manager.startRun
// installs nilLLM/nilTools). The day one does, this policy is not what
// will hold it; see spec.md §0.4.

forbid (
    principal == User::"local",
    action == Action::"graph.run",
    resource is Graph
) when {
    context.spec_provenance == "model_authored"
};
