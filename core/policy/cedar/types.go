package cedar

import (
	"strings"
	"unicode"

	cedar "github.com/cedar-policy/cedar-go"
)

// Action constants are the gate-hook operation names the engine
// recognises. Each corresponds to one of the five gate categories
// listed in spec FR-053:
//
//	tool_exec, file_write, network_request, model_select, memory_write.
//
// Cedar identifies actions by EntityUID with type "Action"; the ID is
// the constant string here.
const (
	ActionToolExec       = "tool_exec"
	ActionFileWrite      = "file_write"
	ActionFileRead       = "file_read"
	ActionNetworkRequest = "network_request"
	ActionModelSelect    = "model_select"
	ActionMemoryWrite    = "memory_write"

	// State-kind action UIDs introduced by FR-058b. These coexist with
	// the broad `tool_exec` and `file_*` actions: a State `read_file`
	// node evaluates BOTH `Filesystem::"<path>"` (via ActionFileRead)
	// AND a finer-grained `Read::"<source>"` action UID so policy
	// authors can express "permit reads but deny reads from
	// `~/.ssh`" with a single forbid rule.
	ActionStateRead  = "state_read"
	ActionStateWrite = "state_write"

	// Action UIDs introduced by mission cedar-credential-policy-01KQ8TDE
	// (WP01). They identify the four resource families that the
	// universal interactive permission system gates:
	//
	//   ActionUseCredential    — Credential::"<provider-id>::<purpose>"
	//   ActionRunBashCommand   — BashCommand::"<argv-pattern>"
	//   ActionReadFilesystem   — FilesystemOp::"<canonical-path>" (read)
	//   ActionWriteFilesystem  — FilesystemOp::"<canonical-path>" (write)
	//   ActionUseTool          — Tool::"<fully-qualified-tool-name>"
	//
	// These coexist with the broader `tool_exec` / `file_*` actions
	// from agent-kernel-graph; consumers gradually migrate to the
	// finer-grained surface as each WP lands.
	ActionUseCredential   = "use_credential"
	ActionRunBashCommand  = "run_bash_command"
	ActionReadFilesystem  = "read_filesystem"
	ActionWriteFilesystem = "write_filesystem"
	ActionUseTool         = "use_tool"

	// MCP recipe family actions introduced by mission
	// mcp-server-install-01KQ8TDP (WP10).
	//
	//   ActionAddRecipe   — gates AddRecipe + EditRecipe RPC calls
	//   ActionSpawnRecipe — gates the spawn path when the pool opens a recipe
	ActionAddRecipe   = "add_recipe"
	ActionSpawnRecipe = "spawn_recipe"

	// Workflow-family action UIDs introduced by mission
	// workflows-01KQ8TDG (WP11). They gate the three RPC-level
	// operations on workflow definitions:
	//
	//   ActionWorkflowRun    — gates Engine.Run (workflow dispatch)
	//   ActionWorkflowSave   — gates Save (persist a definition)
	//   ActionWorkflowDelete — gates Delete (remove a definition)
	//
	// The action UIDs use dotted form to match the audit kind names
	// (workflow.executed / workflow.saved / workflow.deleted) so the
	// audit panel cross-references the same surface.
	ActionWorkflowRun    = "workflow.run"
	ActionWorkflowSave   = "workflow.save"
	ActionWorkflowDelete = "workflow.delete"

	// ActionArtifactUpdate gates the kaneaz__update_artifact builtin
	// (update-artifact-tool-01KQ8TD4). Default-allow under FSWriteEnabled;
	// gated by the same FSWriteDisabled toggle as the write-family fs
	// builtins. The resource UID is Artifact::"<artifact-id>".
	ActionArtifactUpdate = "artifact.update"

	// ── Builtin search-and-elicitation action families ──────────────────
	// Introduced by mission builtin-tools-search-and-elicitation-01KZNP3D
	// and extended by ask-user-question-interactive-01KZNP3G. All families
	// follow the dotted "<domain>.<operation>" naming convention
	// established by the workflow and artifact action families above.
	// Downstream skills-catalog + future elicitation missions MUST follow
	// the same convention when adding new action families.

	// ActionToolReadGlob gates kaneaz__glob (glob-match across a directory
	// tree). Resource UID: Filesystem::"<base-dir>". Default-allow when
	// FSReadEnabled is true; gated by the same FSReadDisabled toggle.
	ActionToolReadGlob = "tool.read.glob"

	// ActionToolReadGrep gates kaneaz__grep (in-process regex search).
	// Resource UID: Filesystem::"<search-path>". Default-allow when
	// FSReadEnabled is true; gated by the same FSReadDisabled toggle.
	ActionToolReadGrep = "tool.read.grep"

	// ActionToolTodoWrite gates kaneaz__todo_write (structured task list
	// write). Resource UID: TodoList::"session" (session-scoped list).
	// Default-allow when TodoEnabled is true; gated by TodoDisabled toggle.
	// The action intentionally lives in the "tool.todo" family rather than
	// "tool.write" so policy authors can grant todo access independently of
	// the broader filesystem write surface.
	ActionToolTodoWrite = "tool.todo.write"

	// ActionToolTodoRead gates future kaneaz__todo_read (reserved — not yet
	// implemented). Declared here so Cedar policy snippets written against
	// the action family validate without schema changes when the read tool
	// ships. Resource UID: TodoList::"session".
	ActionToolTodoRead = "tool.todo.read"

	// ActionElicitAsk gates kaneaz__ask_user_question (synchronous
	// single-question elicitation: model calls tool → backend opens
	// dialog → user answers → result returns to model). Introduced by
	// ask-user-question-interactive-01KZNP3G WP01. Resource UID:
	// Elicitation::"<kind>" where kind is one of the seven question kinds
	// (radio, checkbox, text, number, slider, date, file).
	ActionElicitAsk = "tool.elicit.ask"
)

// Entity-type names mirror spec §4.10's recommended mapping:
//
//	Tool::"<server>__<tool>"
//	Model::"<provider>:<id>"
//	Network::"<host>"
//	Filesystem::"<path>"
//	Memory::"<scope>"
//	User::"local" (single-user invariant)
const (
	EntityTypeTool       = "Tool"
	EntityTypeModel      = "Model"
	EntityTypeNetwork    = "Network"
	EntityTypeFilesystem = "Filesystem"
	EntityTypeMemory     = "Memory"
	EntityTypeUser       = "User"
	EntityTypeAction     = "Action"

	// EntityTypeStateSource and EntityTypeStateTarget back the FR-058b
	// finer-grained read/write gating. Resource IDs match the State
	// archetype's `source` / `target` enums (e.g. "file", "bash_output",
	// "history" for read; "file", "artifact", "trace" for write).
	EntityTypeStateSource = "State"
	EntityTypeStateTarget = "State"

	// Entity-type names introduced by mission
	// cedar-credential-policy-01KQ8TDE (WP01). The Tool entity already
	// exists from the agent-kernel-graph mission (chat-runner gate);
	// the new families add Credential, BashCommand, and FilesystemOp.
	EntityTypeCredential   = "Credential"
	EntityTypeBashCommand  = "BashCommand"
	EntityTypeFilesystemOp = "FilesystemOp"

	// EntityTypeMCPRecipe is the Cedar entity type for MCP recipe resources.
	// Introduced by mission mcp-server-install-01KQ8TDP (WP10).
	// Resource UIDs take the shape MCPRecipe::"<recipe-id>".
	EntityTypeMCPRecipe = "MCPRecipe"

	// EntityTypeWorkflow is the Cedar entity type for workflow
	// definitions. Introduced by mission workflows-01KQ8TDG (WP11).
	// Resource UIDs take the shape Workflow::"<workflow-id>".
	EntityTypeWorkflow = "Workflow"

	// EntityTypeTodoList is the Cedar entity type for the session-scoped
	// todo list. Introduced by mission
	// builtin-tools-search-and-elicitation-01KZNP3D (WP05).
	// Resource UIDs take the shape TodoList::"session".
	EntityTypeTodoList = "TodoList"

	// EntityTypeElicitation is the Cedar entity type for the
	// ask-user-question elicitation surface (mission
	// ask-user-question-interactive-01KZNP3G WP01). Resource UIDs take
	// the shape Elicitation::"<kind>" where kind is one of the seven
	// question kinds: radio, checkbox, text, number, slider, date, file.
	EntityTypeElicitation = "Elicitation"

	// PrincipalLocal is the canonical EntityUID id for the single
	// local user. The harness is single-user / privacy-first
	// (NFR-005); the policy surface is built around this invariant.
	PrincipalLocal = "local"
)

// Outcome is a closed enum mirroring Cedar's allow/deny but with an
// explicit "not applicable" state used when no policy matches AND the
// engine's DefaultDeny flag is false. Default-allow is the spec's
// stance ("observable, not blocking by default; user opts in to
// fail-closed"). Frontends pattern-match on this value.
type Outcome int

const (
	// Allow — at least one permit policy matched and no forbid policy.
	Allow Outcome = iota
	// Deny — at least one forbid policy matched OR no policy matched
	// AND DefaultDeny is true.
	Deny
	// NotApplicable — no policy matched AND DefaultDeny is false.
	// Callers treat this as "allow with audit" by default.
	NotApplicable
)

// String renders Outcome for logs and audit lines.
func (o Outcome) String() string {
	switch o {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	case NotApplicable:
		return "not_applicable"
	default:
		return "unknown"
	}
}

// ToolUID builds a Cedar EntityUID for a tool reference, using the
// kaneaz-harness "<server>__<tool>" convention. server may be empty
// for first-party tools (websearch, bash) — those are stored as
// "builtin__<tool>" so the entity space stays uniform.
func ToolUID(server, tool string) cedar.EntityUID {
	id := tool
	if server == "" {
		server = "builtin"
	}
	id = server + "__" + tool
	return cedar.NewEntityUID(EntityTypeTool, cedar.String(id))
}

// ModelUID builds a Cedar EntityUID for a "<provider>:<id>" model
// reference. provider is e.g. "openai", "anthropic"; modelID is the
// adapter-internal id like "gpt-4o" or "claude-sonnet-4".
func ModelUID(provider, modelID string) cedar.EntityUID {
	return cedar.NewEntityUID(
		EntityTypeModel,
		cedar.String(provider+":"+modelID),
	)
}

// NetworkUID builds a Cedar EntityUID for a network host. The host
// is lowercased and stripped of any trailing dot to keep the entity
// space deterministic.
func NetworkUID(host string) cedar.EntityUID {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return cedar.NewEntityUID(EntityTypeNetwork, cedar.String(host))
}

// FilesystemUID builds a Cedar EntityUID for a filesystem path.
// Callers SHOULD pass an absolute, clean path (filepath.Clean +
// filepath.Abs) so policy match results are deterministic.
func FilesystemUID(path string) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeFilesystem, cedar.String(path))
}

// MemoryUID builds a Cedar EntityUID for a memory scope. scope is one
// of "global", "project", or "session" per FR-029.
func MemoryUID(scope string) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeMemory, cedar.String(scope))
}

// StateSourceUID builds a Cedar EntityUID for a State `read` source
// class. source is one of "history", "corpus", "memory", "attachment",
// "file", "bash_output" — matching the `source:` enum on the read
// archetype (FR-058b).
func StateSourceUID(source string) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeStateSource, cedar.String(source))
}

// StateTargetUID builds a Cedar EntityUID for a State `write` target
// class. target is one of "memory", "corpus", "trace", "file",
// "artifact" — matching the `target:` enum on the write archetype
// (FR-058b).
func StateTargetUID(target string) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeStateTarget, cedar.String(target))
}

// UserUID returns the canonical local-user principal.
func UserUID() cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeUser, cedar.String(PrincipalLocal))
}

// ActionUID builds a Cedar Action EntityUID. The string MUST be one
// of the Action* constants in this package; unknown strings still
// produce a valid UID but match nothing in the default policy.
func ActionUID(name string) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeAction, cedar.String(name))
}

// invalidUIDID is the canonical replacement-id substituted when one of
// the family-aware UID constructors below rejects malformed input. The
// resource-type stays unchanged so the policy bundle's `resource is
// <T>` clauses keep type-matching, but the literal value is something
// no realistic call site would ever match — a typo therefore never
// silently authorises against a permit policy.
const invalidUIDID = "invalid"

// validateFamilyID checks a resource-id fragment is safe to embed in a
// Cedar EntityUID literal for the WP01 resource families. The four
// invariants below mirror the spec's threat model (FR-009, FR-017):
//
//   - Empty strings collapse the family namespace ("Tool::\"\"" matches
//     no realistic call site, but a typo here would silently bypass
//     intended denies).
//   - Control characters and the NUL byte are rejected because they
//     can corrupt log lines, audit records, and downstream consumers
//     that assume printable IDs.
//   - Leading `..` is rejected to prevent path-traversal-style abuse
//     of the FilesystemOp / BashCommand UIDs (and to harmonise with
//     the spec's `..`-rejection rule for FilesystemOp paths).
//
// The function returns true when id is acceptable. Callers use the
// boolean to swap in invalidUIDID without forfeiting the family type.
func validateFamilyID(id string) bool {
	if id == "" {
		return false
	}
	if strings.HasPrefix(id, "..") {
		return false
	}
	for _, r := range id {
		if r == 0 {
			return false
		}
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// CredentialUID builds a Cedar EntityUID for the Credential family
// introduced in WP01. The id encodes the provider id and the access
// purpose using the spec §3 "<provider-id>::<purpose>" shape (e.g.
// "openai::provider_call"). Callers SHOULD pass a non-empty provider
// and a member of the credstore.AccessPurpose enum; malformed input
// (empty / control chars / leading "..") is replaced with the literal
// "invalid" so the resulting UID type-matches in policy `is Credential`
// clauses but never satisfies any real permit.
func CredentialUID(provider, purpose string) cedar.EntityUID {
	id := provider + "::" + purpose
	if !validateFamilyID(provider) || !validateFamilyID(purpose) {
		id = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeCredential, cedar.String(id))
}

// BashCommandUID builds a Cedar EntityUID for the BashCommand family.
// pattern is the derived "argv[0] [argv[1]?]" pattern from FR-014
// (e.g. "git status", "ls", "run.sh"). Pattern derivation lives in
// `core/tools/bash/pattern.go` (WP03); this constructor only validates
// and embeds. Empty / control-char / leading-".." patterns are
// replaced with the literal "invalid".
func BashCommandUID(pattern string) cedar.EntityUID {
	id := pattern
	if !validateFamilyID(pattern) {
		id = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeBashCommand, cedar.String(id))
}

// FilesystemOpUID builds a Cedar EntityUID for the FilesystemOp family.
// canonicalPath is the `filepath.Abs` + `filepath.Clean`'d path from
// FR-017 (e.g. "/Users/alec/projects/kaneaz/main.go"). Callers MUST
// canonicalise the path before invoking this constructor; the
// constructor itself only enforces the universal "no empty / no
// control / no leading .." invariant — it does NOT re-canonicalise so
// the caller's traversal-rejection layer keeps a single source of
// truth.
func FilesystemOpUID(canonicalPath string) cedar.EntityUID {
	id := canonicalPath
	if !validateFamilyID(canonicalPath) {
		id = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeFilesystemOp, cedar.String(id))
}

// PermissionToolUID builds a Cedar EntityUID for the Tool family in
// the WP01 universal-permission shape. toolName is the fully qualified
// "<server>__<tool>" name (e.g. "kaneaz__bash", "filesystem__write_file").
// This constructor differs from the older ToolUID(server, tool) helper
// — that one composes the id from two arguments and is kept for
// backwards compatibility with the agent-kernel-graph chat-runner
// gate. New gate sites that already have the canonical fully-qualified
// name (mcp registry, recipe metadata) call PermissionToolUID directly
// to avoid re-splitting + re-joining.
func PermissionToolUID(toolName string) cedar.EntityUID {
	id := toolName
	if !validateFamilyID(toolName) {
		id = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeTool, cedar.String(id))
}

// WorkflowUID builds a Cedar EntityUID for the Workflow family
// introduced by mission workflows-01KQ8TDG (WP11). id is the workflow's
// canonical identifier (e.g. "wf-abc123", "summarize-code"). Malformed
// ids (empty / control characters / leading "..") are replaced with the
// literal "invalid" so the resulting UID type-matches in `resource is
// Workflow` clauses but never satisfies any real permit.
func WorkflowUID(id string) cedar.EntityUID {
	safeID := id
	if !validateFamilyID(id) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeWorkflow, cedar.String(safeID))
}

// MCPRecipeUID builds a Cedar EntityUID for the MCPRecipe family
// introduced by mission mcp-server-install-01KQ8TDP (WP10). id is the
// recipe's canonical identifier (e.g. "github", "sqlite", "my-custom").
//
// Malformed ids (empty / control characters / leading "..") are replaced
// with the literal "invalid" so the resulting UID type-matches in
// `resource is MCPRecipe` clauses but never satisfies any real permit —
// a typo therefore never silently authorises.
func MCPRecipeUID(id string) cedar.EntityUID {
	safeID := id
	if !validateFamilyID(id) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeMCPRecipe, cedar.String(safeID))
}

// TodoListUID builds a Cedar EntityUID for the TodoList family introduced
// by mission builtin-tools-search-and-elicitation-01KZNP3D (WP05). scope
// is the list's scope discriminator — "session" for the current session's
// list (the only scope in v1). Malformed scopes (empty / control characters
// / leading "..") are replaced with "invalid" so the resulting UID
// type-matches in `resource is TodoList` clauses but never satisfies any
// real permit.
func TodoListUID(scope string) cedar.EntityUID {
	safeScope := scope
	if !validateFamilyID(scope) {
		safeScope = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeTodoList, cedar.String(safeScope))
}

// ElicitationUID builds a Cedar EntityUID for the Elicitation family
// introduced by mission ask-user-question-interactive-01KZNP3G (WP01).
// kind is one of the seven question kinds (radio, checkbox, text, number,
// slider, date, file). Malformed kinds (empty / control characters /
// leading "..") are replaced with the literal "invalid" so the resulting
// UID type-matches in `resource is Elicitation` clauses but never
// satisfies any real permit — a typo therefore never silently authorises.
func ElicitationUID(kind string) cedar.EntityUID {
	safeID := kind
	if !validateFamilyID(kind) {
		safeID = invalidUIDID
	}
	return cedar.NewEntityUID(EntityTypeElicitation, cedar.String(safeID))
}
