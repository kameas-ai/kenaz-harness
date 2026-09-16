// tool-dispatch-never-allow-recommended.cedar
// =====================================================================
// RECOMMENDED hardening policy for risk-rated-autonomy-01PMRA01's layer 1
// (cedar.ThreeLayerResolve, core/policy/cedar/risk_layer.go). NOT applied
// by default — copy this file into your DataDir at
// <DataDir>/policy/tool-dispatch-never-allow-recommended.cedar (or
// install it via the recipe-install / "Install policy template" UI) to
// activate.
//
// WHY THIS FILE EXISTS
// ---------------------
// Layer 3 (the risk rater) only ever runs on a tool dispatch Cedar has NO
// opinion about (Outcome NotApplicable). Today's shipped default policy
// set (default_tool_policy.cedar) is thin: it permits every built-in
// `kenaz__*` tool unconditionally and says nothing else, so almost every
// MCP-server tool dispatch falls through to layer 3 and is decided by a
// model call. The mission spec is explicit that this must be closed by
// EXPANDING layer 1, not by trusting the rater harder: "Anything that
// must never happen belongs in a forbid at layer 1, where no rating can
// reach it" (spec.md, "Security: the rated input is attacker-influenced").
// A `forbid` here is a HARD stop — cedar.ThreeLayerResolve returns Deny
// at layer 1 before the rater is ever constructed or called, regardless
// of what a (possibly prompt-injected) rating would have said.
//
// MATCHING MECHANICS (why `context.tool_name` / `context.server_name`
// reliably fire)
// ---------------------------------------------------------------------
// Unlike some other shipped policy files whose forbid rules referenced
// action/resource/context shapes their own evaluator never produced or
// populated (finding #52 — filesystem-full-recommended.cedar's forbid
// rules were dead for months for exactly that reason), `Action::"use_tool"`
// dispatch is different: core/policy/cedar/engine.go's Engine.Evaluate
// unconditionally runs populateFamilyContext for every use_tool call,
// which derives `context.tool_name` and `context.server_name`
// (CtxKeyToolName / CtxKeyServerName) FROM THE RESOURCE UID ITSELF
// (cedar.ToolUID's "<server>__<tool>" split) — the caller never has to
// remember to pass them, and there is no context key here this file's
// own author could get wrong the way #52 did. See
// scripts/ci/check-shipped-policy-matchable.sh (G-4), which verifies
// exactly this claim for every `.cedar` file under this directory: that
// `use_tool`'s evaluator populates every context key a `when` clause
// here reads.
//
// WHY WILDCARDS, NOT EXACT NAMES
// -------------------------------
// MCP tool catalogs are runtime-discovered (a server's `tools/list`
// response), not statically enumerable from this repo — there is no
// compile-time list of every tool name a future-installed MCP server
// might expose. That means this file cannot cite a specific,
// currently-installed tool the way default_tool_policy.cedar cites
// `kenaz__*` (a fixed, harness-controlled namespace). Instead it forbids
// by NAME PATTERN against the classes the mission spec names explicitly
// (credential exfiltration, force-push, mass delete, spend/payment):
// `like` wildcards against `context.tool_name`, so a not-yet-installed
// tool whose name matches one of these patterns is forbidden the moment
// it is first dispatched, the same defense-in-depth posture
// filesystem-full-recommended.cedar already takes against speculative
// sensitive paths (`*/.ssh/*` etc.) rather than a fixed, currently-real
// list of files. This is deliberately broad — false positives here are
// the intended, opt-in trade for a hard stop on real matches, and any
// operator who hits a false positive can always add a more specific
// `permit` in their own on-disk policy (permits and forbids at the same
// layer compose; see default_credential_policy.cedar's own note on
// on-disk composition order).
//
// NOTE ON AUDIT-BULK-PURGE (deliberately NOT duplicated here)
// -------------------------------------------------------------------
// "Audit purge" is one of the mission spec's five named never-allow
// classes, but it is NOT a tool-dispatch action at all: audit bulk purge
// is a Wails-bound RPC method the frontend calls directly
// (`Audit_BulkPurge` -> `Action::"audit.bulk_purge"` on resource
// `AuditLog`), gated by `default_audit_bulk_purge_policy.cedar`'s
// existing forbid — already a layer-1 hard stop, just not through
// `cedar.ThreeLayerResolve`/`Action::"use_tool"` (no model tool call can
// reach that RPC method). Adding a `use_tool`-shaped forbid for "audit
// purge" here would be exactly the #52 shape this file's own header
// warns against: a rule with no real action to match. See that file
// instead; this file does not repeat its coverage.
//
// You SHOULD review and extend the pattern list for your own MCP
// servers' actual tool naming conventions once installed.
// =====================================================================

// --- credential exfiltration -----------------------------------------
// A tool whose name advertises reading/exporting/dumping raw secret or
// credential material. Deliberately does NOT match `kenaz__list_secrets`
// (server_name "kenaz", tool_name "list_secrets") — that built-in lists
// secret REFERENCE NAMES by design (model-secret-reference-01K...), never
// raw values, and default_tool_policy.cedar already permits it as part
// of the `kenaz` builtin bundle; forbidding it would break a shipped,
// intentional capability rather than close a hole. `like` matches full
// substrings, so "export_secret", "dump_credential" etc. all match; the
// bare word "secret"/"credential" is deliberately NOT used alone since
// it would also forbid the legitimate list_secrets tool_name.
forbid (
    principal == User::"local",
    action == Action::"use_tool",
    resource is Tool
) when {
    context.tool_name like "*export_secret*" ||
    context.tool_name like "*export_credential*" ||
    context.tool_name like "*dump_secret*" ||
    context.tool_name like "*dump_credential*" ||
    context.tool_name like "*read_raw_secret*" ||
    context.tool_name like "*exfiltrate*"
};

// --- force-push / history-rewriting git operations --------------------
forbid (
    principal == User::"local",
    action == Action::"use_tool",
    resource is Tool
) when {
    context.tool_name like "*force_push*" ||
    context.tool_name like "*force-push*" ||
    context.tool_name like "*push_force*"
};

// --- mass / bulk delete -------------------------------------------------
forbid (
    principal == User::"local",
    action == Action::"use_tool",
    resource is Tool
) when {
    context.tool_name like "*delete_all*" ||
    context.tool_name like "*bulk_delete*" ||
    context.tool_name like "*mass_delete*" ||
    context.tool_name like "*purge_all*" ||
    context.tool_name like "*delete_repo*" ||
    context.tool_name like "*drop_all*"
};

// --- spend / payment ----------------------------------------------------
// Recipe registry.json ships Ramp / Brex / Square / Stripe "spend
// management" MCP recipes today; their actual tool catalogs are
// runtime-discovered, so this matches by the common verb shape rather
// than a specific server's tool name.
forbid (
    principal == User::"local",
    action == Action::"use_tool",
    resource is Tool
) when {
    context.tool_name like "*charge*" ||
    context.tool_name like "*create_payment*" ||
    context.tool_name like "*transfer_funds*" ||
    context.tool_name like "*send_payment*" ||
    context.tool_name like "*issue_refund*" ||
    context.tool_name like "*wire_transfer*"
};

// --- end of recommended forbids -------------------------------------
//
// Cedar evaluates forbid rules with priority over permits, so anything
// added here overrides both the default-allow stance from
// default_policy.cedar and any layer-3 rating that would otherwise apply
// to a tool that has not yet matched a rule above.
