// mcp-no-npx.cedar — Recommended security template for MCP recipe gating.
// Introduced by mission mcp-server-install-01KQ8TDP (WP10).
//
// PURPOSE
// -------
// Denies installation and spawning of any MCP recipe whose first command
// is `npx` (the Node package executor). The `npx` binary fetches and
// executes arbitrary npm packages at spawn time, which introduces an
// uncontrolled supply-chain risk: a package name typo, a package takeover,
// or a malicious registry mirror can silently execute attacker code the
// moment a recipe spawns.
//
// USAGE
// -----
// Copy this file to <DataDir>/policy/mcp-no-npx.cedar.
// The engine picks it up on the next Reload and the gate immediately
// applies to new AddRecipe / InstallRecipe / EditRecipe calls.
// Already-running servers are NOT terminated — stop and remove the recipe
// through the UI if you want the running server gone.
//
// TUNING
// ------
// To also deny `uvx` (uv's package runner) or other launchers that
// download-and-exec at runtime, duplicate the forbid block and change
// the `recipe_command` value. Example:
//
//   forbid (
//       principal == User::"local",
//       action in [Action::"add_recipe", Action::"spawn_recipe"],
//       resource is MCPRecipe
//   ) when {
//       context.recipe_command == "uvx"
//   };
//
// To allow a specific recipe even when its command is npx, add a
// narrow permit above this forbid:
//
//   permit (
//       principal == User::"local",
//       action in [Action::"add_recipe", Action::"spawn_recipe"],
//       resource == MCPRecipe::"my-trusted-npx-recipe"
//   );
//
// Cedar's forbid/permit resolution: a forbid wins unless a *specific*
// permit matches the same (principal, action, resource) triple (the
// "permit overrides forbid" rule). A permit on the exact recipe id above
// is specific enough to override.
//
// CONTEXT ATTRIBUTES
// ------------------
//   recipe_id        (string) — canonical recipe identifier.
//   recipe_command   (string) — Command[0], the first argv element, e.g.
//                    "npx", "uvx", "node", "/usr/local/bin/my-server".
//   recipe_transport (string) — "stdio" | "http" | "sse".

forbid (
    principal == User::"local",
    action in [Action::"add_recipe", Action::"spawn_recipe"],
    resource is MCPRecipe
) when {
    context.recipe_command == "npx"
};
