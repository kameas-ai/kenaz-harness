package cedar

import (
	"strings"

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
	ActionNetworkRequest = "network_request"
	ActionModelSelect    = "model_select"
	ActionMemoryWrite    = "memory_write"
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
