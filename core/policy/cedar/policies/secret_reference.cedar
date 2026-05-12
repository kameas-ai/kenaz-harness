// secret_reference.cedar — SecretReference family default bundle
// for mission `model-secret-references-01KW7M5A` (WP02).
//
// Resource family: `SecretReference::"<locator>"`
// Action:          `Action::"secret_reference.resolve"`
// Context attrs:
//   tool_name        (string) — name of the invoking tool
//   destination_host (string) — outbound request host (empty for bash)
//   is_streaming     (bool)   — true when session is streaming
//   budget           (long)   — remaining resolution budget; 0 = exhausted
//   agent_kind       (string) — "trusted" | "untrusted"
//
// Default posture (spec §2.4):
//   - permit when budget > 0 AND agent_kind != "untrusted"
//     (exact locator membership in exposed_secrets is enforced by the
//      resolver layer — Cedar sees only whether the resolution is
//      structurally permissible).
//   - forbid when agent_kind == "untrusted" (un-audited MCP servers).
//   - forbid when budget == 0 (per-session resolution count exhausted).
//
// Power users can tighten resolution by adding .cedar files in
// <DataDir>/policy/ that forbid on destination_host or tool_name:
//
//   forbid (
//     principal == User::"local",
//     action == Action::"secret_reference.resolve",
//     resource == SecretReference::"user:example-api-token"
//   ) when {
//     context.destination_host != "api.example.com"
//   };

// permit: structural resolution allowed when budget remains and agent
// is not untrusted.
permit (
    principal == User::"local",
    action == Action::"secret_reference.resolve",
    resource is SecretReference
) when {
    context.budget > 0 &&
    context.agent_kind != "untrusted"
};

// forbid: untrusted agents (un-audited MCP servers) may not resolve
// any secret reference regardless of the exposed_secrets set.
forbid (
    principal == User::"local",
    action == Action::"secret_reference.resolve",
    resource is SecretReference
) when {
    context.agent_kind == "untrusted"
};

// forbid: per-session budget exhausted (resolution_count reached limit).
forbid (
    principal == User::"local",
    action == Action::"secret_reference.resolve",
    resource is SecretReference
) when {
    context.budget == 0
};
