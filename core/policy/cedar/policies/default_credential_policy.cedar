// default_credential_policy.cedar — Credential family default bundle
// for mission `cedar-credential-policy-01KQ8TDE` (WP01).
//
// Resource family: `Credential::"<provider-id>::<purpose>"`
// Action:          `Action::"use_credential"`
// Context attrs:   `purpose` (string) — one of
//                  provider_call | provider_test | tool_dispatch |
//                  mcp_spawn | manual_export
//
// Default posture (spec FR-002):
//   - Routine purposes (provider_call, provider_test, tool_dispatch)
//     → permit. The chat runner pulls API keys for normal model calls
//     and recipe-time tool dispatch without any prompt.
//   - manual_export → forbid. Exporting a raw key to the user's
//     clipboard / disk is the privacy-sensitive surface; default-deny
//     forces an explicit Cedar policy or Settings override.
//   - mcp_spawn → no rule. Falls through to NotApplicable so the
//     universal prompt flow (WP02) can fire on first MCP server spawn.
//
// Power users override this file by writing additional `.cedar` files
// in `<DataDir>/policy/`; on-disk files compose with this embedded
// bundle in lexicographic order (Reload semantics).

permit (
    principal == User::"local",
    action == Action::"use_credential",
    resource is Credential
) when {
    context.purpose == "provider_call" ||
    context.purpose == "provider_test" ||
    context.purpose == "tool_dispatch"
};

forbid (
    principal == User::"local",
    action == Action::"use_credential",
    resource is Credential
) when {
    context.purpose == "manual_export"
};
