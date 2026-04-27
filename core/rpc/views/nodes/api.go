// Package nodes is the view-scoped RPC surface for the manifest-driven
// node catalog (mission agent-kernel-graph-node-catalog; WP07).
//
// The view exposes the resolved manifest catalog with provenance
// information, an explicit reload entry-point, and a listing of the
// user-override files at <DataDir>/agent_graph/nodes/. WP06 layers the
// frontend palette + attribute editor on top of these accessors; WP07
// itself only ships the data surface.
//
// FR-024 surfaces an `AttrProvenance` shape describing the layer that
// contributed each effective attr (shipped, archetype-X, user-override)
// so the frontend can render an inheritance tooltip.
package nodes

import (
	"context"
	"errors"
)

// ErrManagerUnavailable signals the chassis booted without the catalog
// wired (e.g. the test-harness path with New(nil)). Callers fall back to
// the empty-state surface — methods return this sentinel rather than
// panicking.
var ErrManagerUnavailable = errors.New("nodes: catalog unavailable")

// Layer enumerates the provenance layers a resolved attr may originate
// from. The runtime reports concrete values; the frontend renders them
// alongside the effective default.
//
//	"shipped"        — shipped manifest at <kind-id>'s leaf layer
//	"archetype-<id>" — inherited from the named archetype layer
//	"user-override"  — user manifest at <DataDir>/agent_graph/nodes/
const (
	LayerShipped      = "shipped"
	LayerUserOverride = "user-override"
	// LayerArchetypePrefix prefixes inherited archetype layers; the
	// surface form is `archetype-<id>` (e.g. "archetype-compute").
	LayerArchetypePrefix = "archetype-"
)

// AttrProvenance is the wire shape describing which layer contributed
// the effective value for a given field path. FieldPath uses the same
// dotted notation the resolver writes (e.g. "attrs.max_tokens",
// "defaults.temperature", "ports.inputs").
type AttrProvenance struct {
	FieldPath string `json:"fieldPath"`
	Layer     string `json:"layer"`
}

// AttrSpecWire is the view-shaped attr descriptor. Mirrors the core
// AttrSpec but keeps the optional numeric bounds as pointers so JSON
// distinguishes "unset" from "0".
type AttrSpecWire struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Required    bool        `json:"required,omitempty"`
	Default     interface{} `json:"default,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
	Min         *float64    `json:"min,omitempty"`
	Max         *float64    `json:"max,omitempty"`
	MinLength   *int        `json:"minLength,omitempty"`
	MaxLength   *int        `json:"maxLength,omitempty"`
	Description string      `json:"description,omitempty"`
}

// PortSpecWire is the view-shaped port descriptor.
type PortSpecWire struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	DefaultFor  string `json:"defaultFor,omitempty"`
}

// NodeManifestSummary is the catalog-list row. The frontend palette
// renders these without fetching the full attribute schema.
type NodeManifestSummary struct {
	ID          string   `json:"id"`
	KindName    string   `json:"kindName,omitempty"`
	DisplayName string   `json:"displayName,omitempty"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category,omitempty"`
	Extends     string   `json:"extends,omitempty"`
	Archetype   string   `json:"archetype,omitempty"`
	Callable    bool     `json:"callable"`
	Aliases     []string `json:"aliases,omitempty"`
	Source      string   `json:"source,omitempty"`
	Hash        string   `json:"hash,omitempty"`
	Version     string   `json:"version,omitempty"`
}

// NodeManifestDetail is the full resolved manifest plus per-field
// provenance. Returned by Get(id).
type NodeManifestDetail struct {
	Summary NodeManifestSummary `json:"summary"`

	// Chain is the inheritance walk root-first
	// (e.g. ["state", "read", "corpus_read"]).
	Chain []string `json:"chain"`

	// Attrs maps attr-name → AttrSpecWire (effective merged shape).
	Attrs []AttrSpecWire `json:"attrs"`

	// Ports carries the merged port set.
	Ports PortSetWire `json:"ports"`

	// Defaults is the effective key-by-key merged defaults map.
	Defaults map[string]interface{} `json:"defaults,omitempty"`

	// BudgetConsumes lists the kernel-budget dials this kind consumes.
	BudgetConsumes []string `json:"budgetConsumes,omitempty"`
	Budget         string   `json:"budget,omitempty"`
	Executor       string   `json:"executor,omitempty"`

	// Provenance is the per-field-path layer attribution. Field paths
	// match the resolver's convention (e.g. "attrs.<name>",
	// "defaults.<name>", "ports.inputs").
	Provenance []AttrProvenance `json:"provenance"`
}

// PortSetWire pairs the merged input + output port lists.
type PortSetWire struct {
	Inputs  []PortSpecWire `json:"inputs,omitempty"`
	Outputs []PortSpecWire `json:"outputs,omitempty"`
}

// ReloadResult is what ReloadOverrides returns: the diff against the
// pre-reload catalog state.
type ReloadResult struct {
	Added    []string `json:"added"`
	Removed  []string `json:"removed"`
	Modified []string `json:"modified"`
	// Errors collects per-file load errors so the frontend can surface
	// them (e.g. malformed YAML on disk). An empty slice means a clean
	// reload.
	Errors []string `json:"errors,omitempty"`
	// ReloadedAt is the timestamp of the successful re-resolve. Empty
	// when the reload aborted before producing a fresh catalog.
	ReloadedAt string `json:"reloadedAt,omitempty"`
}

// UserOverrideInfo describes one file under <DataDir>/agent_graph/nodes/
// after the loader's parse pass. Status is "ok" for accepted overrides;
// otherwise the per-file error is in Error.
type UserOverrideInfo struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	// ID is the manifest id parsed from the file (empty when parse failed).
	ID     string `json:"id,omitempty"`
	Status string `json:"status"` // "ok" | "error"
	Error  string `json:"error,omitempty"`
	// SizeBytes records the file size at scan time.
	SizeBytes int64 `json:"sizeBytes,omitempty"`
}

// NodesAPI is the view-scoped accessor for the resolved manifest
// catalog. WP06 builds the frontend palette + attribute editor on top
// of these methods.
type NodesAPI interface {
	// Catalog lists every manifest (callable kinds + archetypes) in
	// stable ID order. Used by the WP06 palette tree.
	Catalog(ctx context.Context) ([]NodeManifestSummary, error)

	// Get returns the full resolved manifest detail (including
	// provenance) for a single manifest id. Returns an error wrapping
	// the underlying ErrUnknownKind when id is missing.
	Get(ctx context.Context, id string) (NodeManifestDetail, error)

	// ReloadOverrides re-scans the user-override directory, deep-merges
	// over the shipped manifests, and atomically swaps the catalog. The
	// returned diff lists IDs added / removed / modified relative to
	// the previous catalog state. Per-file parse errors do not abort
	// the swap; they appear in ReloadResult.Errors.
	ReloadOverrides(ctx context.Context) (ReloadResult, error)

	// ListUserOverrides reports every YAML file at the configured user
	// directory with parse status. Empty (no files) and missing dir are
	// both reported as an empty slice with no error.
	ListUserOverrides(ctx context.Context) ([]UserOverrideInfo, error)
}
