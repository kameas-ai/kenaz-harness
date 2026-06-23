// kenaz-harness default policy bundle (agent-kernel-graph WP14).
//
// Stance: default-allow with logging. The harness is local-only,
// single-user, privacy-first; the user opts in to fail-closed via
// Engine.DefaultDeny when they want a stricter posture.
//
// Each rule below covers one of the five gate categories from spec
// FR-053: tool_exec, file_write, network_request, model_select,
// memory_write. Frontends list the rules and recent decisions for
// auditability.

// --- network access -------------------------------------------------
// Permit network access by default for the local user.
permit (
    principal == User::"local",
    action == Action::"network_request",
    resource
);

// --- model selection -----------------------------------------------
// Permit any model selection by default. Enterprise-flavoured
// deployments override this with a forbid rule for specific models.
permit (
    principal == User::"local",
    action == Action::"model_select",
    resource
);

// --- memory writes -------------------------------------------------
// Permit memory writes by default — greedy memory hooks need to be
// allowed by the policy engine for FR-027 to work end-to-end.
permit (
    principal == User::"local",
    action == Action::"memory_write",
    resource
);

// --- tool execution ------------------------------------------------
// Permit tool execution by default. Per-tool deny rules can be added
// by the user to lock down individual tools (e.g. filesystem write
// surfaces) without invalidating the rest of the bundle.
permit (
    principal == User::"local",
    action == Action::"tool_exec",
    resource
);

// --- file writes ---------------------------------------------------
// Permit file writes by default. The user-confirmed roots in the
// filesystem MCP recipe and `<DataDir>` are the typical targets;
// granular forbid rules can scope this further.
permit (
    principal == User::"local",
    action == Action::"file_write",
    resource
);

// --- file reads ----------------------------------------------------
// Permit file reads by default. The State `read_file` kind and the
// filesystem MCP `read` tool both gate through this action. Granular
// forbid rules can disable reads from specific roots (e.g. `~/.ssh`).
permit (
    principal == User::"local",
    action == Action::"file_read",
    resource
);

// --- state read ----------------------------------------------------
// Permit FR-058b State-kind reads by default. The action UID
// `Action::"state_read"` carries a `State::"<source>"` resource where
// source is one of history|corpus|memory|attachment|file|bash_output.
permit (
    principal == User::"local",
    action == Action::"state_read",
    resource
);

// --- state write ---------------------------------------------------
// Permit FR-058b State-kind writes by default. The action UID
// `Action::"state_write"` carries a `State::"<target>"` resource
// where target is one of memory|corpus|trace|file|artifact.
permit (
    principal == User::"local",
    action == Action::"state_write",
    resource
);
