// harness_write_onboarding.cedar — default policy permitting harness-self
// write tools ONLY in sessions tagged kind=onboarding.
// Mission harness-self-mcp-onboarding-01KQ8TDU WP06.
//
// Context attributes set by the cedar engine's populateFamilyContext for
// ActionUseTool requests:
//   context.server_name  — bare server portion (before __): "harness-self"
//   context.tool_name    — tool portion (after __): e.g. "harness_write_set_setting"
//   context.session_kind — session.Record.Kind injected by the caller:
//                          "onboarding" | "chat" | ""
//
// Spec §3 Q3: write-side tools (harness_write_*) are gated to
// kind=onboarding by default. Power users override via the cedar editor.

permit (
    principal == User::"local",
    action == Action::"use_tool",
    resource is Tool
) when {
    context.server_name == "harness-self" &&
    context.tool_name like "harness_write_*" &&
    context.session_kind == "onboarding"
};
