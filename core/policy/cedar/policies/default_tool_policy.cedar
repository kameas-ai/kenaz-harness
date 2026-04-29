// default_tool_policy.cedar — Tool family default bundle
// for mission `cedar-credential-policy-01KQ8TDE` (WP01).
//
// Resource family: `Tool::"<fully_qualified_name>"`
//                  Built-in tools follow the `kaneaz__<tool>` shape
//                  (e.g. `kaneaz__bash`, `kaneaz__websearch`,
//                  `kaneaz__save_artifact`). MCP-server tools follow
//                  `<server>__<tool>` (e.g. `filesystem__read_file`).
// Action:          `Action::"use_tool"`
// Context attrs:
//   - `tool_name`              (string) — fully qualified name above.
//   - `server_name`            (string) — bare server portion before
//                              the `__` separator. "kaneaz" for
//                              built-ins.
//   - `prompt_on_first_use`    (bool)   — recipe-metadata flag (FR-024)
//                              that opts a specific tool into the
//                              universal prompt flow on first call,
//                              even when its server is otherwise
//                              pre-permitted.
//
// Default posture (spec FR-005):
//   - Built-in kaneaz tools (anything under server `kaneaz`) → permit.
//     The bash, websearch, and save_artifact builtins ship with the
//     harness; their per-call gates live in family-specific policy
//     (BashCommand for bash; out-of-scope for v1 for the others).
//   - Anything else → no rule (NotApplicable). MCP-server tools rely
//     on either the recipe's install-time grant (a sibling
//     `tool_allow_<name>.cedar` written by `mcp-server-install`) or
//     the universal prompt flow on first call.
//
// Note on shape: we match by `server_name` rather than a string-prefix
// `like` against `tool_name` because the cedar-go evaluator treats
// `like` as a glob over strings, and we want explicit semantics
// (`kaneaz__bash` is a builtin; `kaneazstuff__bash` is NOT).

permit (
    principal == User::"local",
    action == Action::"use_tool",
    resource is Tool
) when {
    context.server_name == "kaneaz"
};
