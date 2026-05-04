// Package catalog provides the workflow catalog: a browsable list of
// installable workflow templates sourced from builtin/*.yaml and (in a
// future WP) a remote registry.
//
// WP03 — catalog UI + install flow (workflows-agentic-01KW2D3X).
package catalog

import (
	"context"
)

// Catalog is the narrow interface the RPC view and tests code against.
// The concrete implementation lives in concrete.go.
type Catalog interface {
	// List returns every Entry the catalog knows about. InstallStatus
	// reflects the current storage state — "not_installed" for builtin-
	// only, "installed" for persisted user copies.
	List(ctx context.Context) ([]Entry, error)

	// Get returns the parsed Workflow for the given catalog id.
	Get(ctx context.Context, id string) (WorkflowDoc, error)

	// Install copies the YAML to storage via Store.Save, optionally
	// arms a cron schedule, and returns a ref describing what happened.
	// Returns ErrNotFound when id is unknown.
	Install(ctx context.Context, id string) (InstalledRef, error)
}

// Entry is one card in the catalog grid.
type Entry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"` // "builtin" | "user"
	Version     string `json:"version"`
	Icon        string `json:"icon,omitempty"` // optional emoji / URL

	// RequiresCedarGrants are the Cedar action names the workflow needs,
	// e.g. ["network", "shell"]. Derived by scanning step kinds.
	RequiresCedarGrants []string `json:"requiresCedarGrants,omitempty"`

	// RequiresCredentials are the MCP server names referenced by
	// mcp_call steps whose server is not already in the recipe registry.
	RequiresCredentials []string `json:"requiresCredentials,omitempty"`

	// EstimatedCostUSD is a per-run projection derived from each
	// model_turn step's model pricing for average prompt + completion
	// token counts. Zero when no model_turn steps exist or the model is
	// unknown.
	EstimatedCostUSD float64 `json:"estimatedCostUSD"`

	// InstallStatus is one of: "not_installed" | "installed" | "installed_outdated".
	InstallStatus string `json:"installStatus"`
}

// WorkflowDoc bundles the parsed YAML source with its Entry metadata so
// the preview drawer can show both the raw text and the structured fields
// without a second round-trip.
type WorkflowDoc struct {
	Entry    Entry  `json:"entry"`
	YAMLSource string `json:"yamlSource"`
}

// InstalledRef is the result of Install.
type InstalledRef struct {
	// WorkflowID is the id assigned by Store.Save (may differ from the
	// catalog id when Import assigns a fresh id).
	WorkflowID string `json:"workflowId"`

	// Scheduled is true when the workflow had a schedule: field and the
	// Scheduler was wired and successfully armed the cron job.
	Scheduled bool `json:"scheduled"`

	// MissingCredentials lists mcp_call.server names that are
	// referenced by the workflow but not found in the recipe registry.
	MissingCredentials []string `json:"missingCredentials,omitempty"`
}

// ErrNotFound is returned by Get and Install when the catalog id is
// unknown.
var ErrNotFound = errNotFound("catalog: workflow not found")

type errNotFound string

func (e errNotFound) Error() string { return string(e) }
