// harness_write_forbid.cedar — default forbid for harness-self write
// tools in non-onboarding sessions. Mission harness-self-mcp-onboarding-01KQ8TDU
// WP06.
//
// Context attributes set by the cedar engine's populateFamilyContext for
// ActionUseTool requests:
//   context.server_name  — bare server portion (before __): "harness-self"
//   context.tool_name    — tool portion (after __): e.g. "harness_write_set_setting"
//   context.session_kind — session.Record.Kind injected by the caller:
//                          "onboarding" | "chat" | ""
//
// Cedar evaluates forbids strictly; this rule guarantees that even if a
// future permit broadens write access by accident, sessions whose Kind
// is not "onboarding" still cannot call harness_write_*.

forbid (
    principal == User::"local",
    action == Action::"use_tool",
    resource is Tool
) when {
    context.server_name == "harness-self" &&
    context.tool_name like "harness_write_*" &&
    context.session_kind != "onboarding"
};
