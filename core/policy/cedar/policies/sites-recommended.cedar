// sites-recommended.cedar
// =====================================================================
// RECOMMENDED Cedar policy for the fleet-sites MCP recipe.
// NOT applied by default — copy this file into your DataDir at
// <DataDir>/policy/sites-recommended.cedar to activate.
//
// Stance:
//   - Read tools (sites_list, sites_status, sites_logs) are permitted
//     unconditionally for the signed-in principal.
//   - Mutating tools (sites_deploy, sites_delete) are left NotApplicable,
//     routing through the PromptOnFirstUse gate defined in shipped.json
//     (pre_seeding_policy: "prompt_only", prompt_on_first_use: [sites_deploy, sites_delete]).
//
// Cedar resource shape: Tool::"fleet-sites__<tool_name>"
// Context attrs:
//   - server_name  (string) — "fleet-sites"
//   - tool_name    (string) — fully qualified tool name
//   - prompt_on_first_use (bool)
//
// Mission: sites-mcp-server-01NSITE05 WP06.
// =====================================================================

// --- Read tools: always permitted ---
// sites_list, sites_status, sites_logs require authentication but no
// additional user confirmation. The first-use gate does not apply to them.

permit (
    principal == User::"local",
    action == Action::"use_tool",
    resource == Tool::"fleet-sites__sites_list"
);

permit (
    principal == User::"local",
    action == Action::"use_tool",
    resource == Tool::"fleet-sites__sites_status"
);

permit (
    principal == User::"local",
    action == Action::"use_tool",
    resource == Tool::"fleet-sites__sites_logs"
);

// sites_init is a local-only scaffolding tool — no fleet call is made.
// Permit unconditionally.
permit (
    principal == User::"local",
    action == Action::"use_tool",
    resource == Tool::"fleet-sites__sites_init"
);

// --- Mutating tools: NotApplicable (PromptOnFirstUse gate) ---
// sites_deploy and sites_delete are listed in the recipe's
// prompt_on_first_use field. No Cedar permit rule is written here;
// the toolloop dispatcher routes them through the interactive prompt
// registry on first invocation. Once the user grants permission, a
// tool_allow_*.cedar snippet is written by the install path.
//
// To suppress the prompt and always permit these tools, uncomment:
//
//   permit (
//       principal == User::"local",
//       action == Action::"use_tool",
//       resource in [Tool::"fleet-sites__sites_deploy",
//                    Tool::"fleet-sites__sites_delete"]
//   );
//
// You MUST keep sites_delete behind the confirm:true gate regardless
// of what Cedar policy you apply — that check lives in the handler.
