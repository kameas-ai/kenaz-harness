// harness_read_default.cedar — default policy permitting any session to
// invoke harness_read_* tools on the in-process harness-self MCP server.
// Mission harness-self-mcp-onboarding-01KQ8TDU WP06.
//
// Pairs with the family rule that the cedar engine projects from the
// session.Record.Kind into context.session_kind on every tool_call check.
// Read-side tools never mutate state and never return secret material,
// so any session may call them by default.

permit (
    principal == User::"local",
    action == Action::"use_tool",
    resource is Tool
) when {
    context.server_name == "harness-self" &&
    context.tool_name like "*__harness_read_*"
};
