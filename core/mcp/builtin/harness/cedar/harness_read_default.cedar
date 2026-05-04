// harness_read_default.cedar — default policy permitting any session to
// invoke harness_read_* tools on the in-process harness-self MCP server.
// Mission harness-self-mcp-onboarding-01KQ8TDU WP06.
//
// Context attributes set by the cedar engine's populateFamilyContext for
// ActionUseTool requests:
//   context.server_name  — bare server portion (before __): "harness-self"
//   context.tool_name    — tool portion (after __): e.g. "harness_read_list_providers"
//   context.session_kind — session.Record.Kind injected by the caller:
//                          "onboarding" | "chat" | ""
//
// Read-side tools never mutate state and never return secret material,
// so any session may call them by default.

permit (
    principal == User::"local",
    action == Action::"use_tool",
    resource is Tool
) when {
    context.server_name == "harness-self" &&
    context.tool_name like "harness_read_*"
};
