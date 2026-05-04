// harness_write_onboarding.cedar — default policy permitting harness-self
// write tools ONLY in sessions tagged kind=onboarding.
// Mission harness-self-mcp-onboarding-01KQ8TDU WP06.
//
// Spec §3 Q3: write-side tools (harness_write_*) are gated to
// kind=onboarding by default. Power users override via the cedar editor.

permit (
    principal == User::"local",
    action == Action::"use_tool",
    resource is Tool
) when {
    context.server_name == "harness-self" &&
    context.tool_name like "*__harness_write_*" &&
    context.session_kind == "onboarding"
};
