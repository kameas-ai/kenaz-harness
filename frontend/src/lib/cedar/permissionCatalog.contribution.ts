/**
 * permissionCatalog.contribution — Cedar editor permission catalog.
 *
 * Pure-data module (no logic, no wailsjs imports). Consumed by the
 * Cedar policy editor's autocomplete to suggest entity types, action
 * IDs, and example permit/forbid policy bodies when the user is
 * editing a `.cedar` file in the Policy panel.
 *
 * One entry per resource family introduced by mission
 * cedar-credential-policy-01KQ8TDE (WP01). Each entry carries:
 *
 *   - `family`      — human label for the entity type.
 *   - `entityType`  — Cedar entity type name (matches core/policy/cedar/types.go).
 *   - `actions`     — the action ID(s) that apply to this family.
 *   - `examplePermit` — a ready-to-paste permit policy snippet.
 *   - `exampleForbid` — a ready-to-paste forbid policy snippet.
 *
 * DIRECTIVE_001: this file is frontend-only pure data; it MUST NOT
 * import from `wailsjs/` or any Go-generated module.
 */

export interface PermissionCatalogEntry {
  /** Human-readable resource family label. */
  family: string;
  /** Cedar entity type name (e.g. "Credential"). */
  entityType: string;
  /**
   * Cedar action IDs supported by this resource family. The editor
   * autocomplete surfaces these when the user types `action ==`.
   */
  actions: string[];
  /**
   * A complete, ready-to-paste Cedar permit policy for this family.
   * The snippet uses a representative resource ID as a placeholder.
   */
  examplePermit: string;
  /**
   * A complete, ready-to-paste Cedar forbid policy for this family.
   * The snippet uses a representative resource ID as a placeholder.
   */
  exampleForbid: string;
}

/**
 * PERMISSION_CATALOG — the 4 resource families gated by the universal
 * interactive permission system (FR-009, FR-014, FR-017, FR-018).
 *
 * Entries are ordered by decreasing specificity: Credential (highest
 * privilege, fewest matches) → BashCommand → FilesystemOp → Tool.
 */
export const PERMISSION_CATALOG: readonly PermissionCatalogEntry[] = [
  {
    family: 'Credential',
    entityType: 'Credential',
    actions: ['use_credential'],
    examplePermit: `// Allow the local user to use a specific credential.
permit(
  principal == User::"local",
  action == Action::"use_credential",
  resource == Credential::"openai::provider_call"
);`,
    exampleForbid: `// Deny use of a specific credential (e.g. production key).
forbid(
  principal == User::"local",
  action == Action::"use_credential",
  resource == Credential::"openai::provider_call"
);`,
  },
  {
    family: 'BashCommand',
    entityType: 'BashCommand',
    actions: ['run_bash_command'],
    examplePermit: `// Allow the local user to run a specific bash command pattern.
permit(
  principal == User::"local",
  action == Action::"run_bash_command",
  resource == BashCommand::"git status"
);`,
    exampleForbid: `// Deny a bash command pattern (e.g. destructive rm -rf).
forbid(
  principal == User::"local",
  action == Action::"run_bash_command",
  resource == BashCommand::"rm"
);`,
  },
  {
    family: 'FilesystemOp',
    entityType: 'FilesystemOp',
    actions: ['read_filesystem', 'write_filesystem'],
    examplePermit: `// Allow read access to a specific filesystem path.
permit(
  principal == User::"local",
  action == Action::"read_filesystem",
  resource == FilesystemOp::"/home/user/projects"
);`,
    exampleForbid: `// Deny write access to a sensitive path.
forbid(
  principal == User::"local",
  action == Action::"write_filesystem",
  resource == FilesystemOp::"/etc"
);`,
  },
  {
    family: 'Tool',
    entityType: 'Tool',
    actions: ['use_tool'],
    examplePermit: `// Allow use of a specific MCP or built-in tool.
permit(
  principal == User::"local",
  action == Action::"use_tool",
  resource == Tool::"builtin__websearch"
);`,
    exampleForbid: `// Deny use of a specific tool (e.g. a high-risk MCP tool).
forbid(
  principal == User::"local",
  action == Action::"use_tool",
  resource == Tool::"filesystem__write_file"
);`,
  },
] as const;
