// harness_write_forbid.cedar — default forbid for harness-self write
// tools in non-onboarding sessions. Mission harness-self-mcp-onboarding-01KQ8TDU
// WP06.
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
    context.tool_name like "*__harness_write_*" &&
    context.session_kind != "onboarding"
};
