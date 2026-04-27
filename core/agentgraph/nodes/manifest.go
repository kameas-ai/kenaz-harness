// Package nodes implements the manifest-driven node catalog for the
// agent-kernel-graph (mission agent-kernel-graph-node-catalog-01KQ7JDZ,
// WP01).
//
// A manifest is a YAML document describing one archetype or one
// concrete node-kind. The package loads shipped manifests from an
// embedded directory and (in later WPs) merges user overrides from
// `<DataDir>/agent_graph/nodes/`. The inheritance resolver merges
// `extends:` chains at load time and produces a flat ResolvedManifest
// per kind.
//
// WP01 scope is read-only manifest infrastructure. Code generation
// (WP02), validator integration (WP03), and taxonomy renames (WP04)
// land in subsequent work packages.
//
// Charter DIRECTIVE_001: this package depends on nothing inside
// `core/agentgraph` (the parent package). The dependency arrow is
// `agentgraph → agentgraph/nodes`, never the reverse, so kernel-side
// seams in `core/agentgraph/seams.go` will type-assert against an
// interface defined there rather than importing this package directly.
package nodes

// SchemaVersion is the canonical manifest-schema revision. The loader
// rejects unknown major versions.
const SchemaVersion = "1"

// AttrType is the lightweight type system on a manifest attribute. It
// is intentionally string-typed so YAML round-trip is direct; the
// resolver and meta-schema validator (WP01-T3, future) check membership
// against the AllAttrTypes() set.
type AttrType string

const (
	AttrTypeString        AttrType = "string"
	AttrTypeInt           AttrType = "int"
	AttrTypeFloat         AttrType = "float"
	AttrTypeBool          AttrType = "bool"
	AttrTypeDuration      AttrType = "duration"
	AttrTypeEnum          AttrType = "enum"
	AttrTypeObject        AttrType = "object"
	AttrTypeArray         AttrType = "array"
	AttrTypeMap           AttrType = "map"
	AttrTypeModelRef      AttrType = "model_ref"
	AttrTypeToolRef       AttrType = "tool_ref"
	AttrTypeActivityRef   AttrType = "activity_ref"
	AttrTypeCorpusRef     AttrType = "corpus_ref"
	AttrTypeAttachmentRef AttrType = "attachment_ref"
	AttrTypeNodeIDRef     AttrType = "node_id_ref"
	AttrTypePortRef       AttrType = "port_ref"
	AttrTypeMessagesRef   AttrType = "messages_ref"
	// AttrTypeStringSlice is a convenience shorthand authors may use in
	// manifests as `[]string`. The resolver normalises it to AttrTypeArray
	// with an implicit element-type string. We keep the string token
	// distinct so meta-validation can warn on slice-of-non-string usage
	// in a later WP.
	AttrTypeStringSlice AttrType = "[]string"
)

// AllAttrTypes returns every recognised AttrType in declaration order.
// Used by the meta-schema validator and tests.
func AllAttrTypes() []AttrType {
	return []AttrType{
		AttrTypeString, AttrTypeInt, AttrTypeFloat, AttrTypeBool,
		AttrTypeDuration, AttrTypeEnum, AttrTypeObject, AttrTypeArray,
		AttrTypeMap, AttrTypeModelRef, AttrTypeToolRef, AttrTypeActivityRef,
		AttrTypeCorpusRef, AttrTypeAttachmentRef, AttrTypeNodeIDRef,
		AttrTypePortRef, AttrTypeMessagesRef, AttrTypeStringSlice,
	}
}

// IsKnown reports whether t is a recognised AttrType.
func (t AttrType) IsKnown() bool {
	for _, candidate := range AllAttrTypes() {
		if candidate == t {
			return true
		}
	}
	return false
}

// Category is the top-level taxonomy: organisational only.
type Category string

const (
	CategoryCompute Category = "compute"
	CategoryControl Category = "control"
	CategoryState   Category = "state"
)

// AllCategories returns the three first-class categories.
func AllCategories() []Category { return []Category{CategoryCompute, CategoryControl, CategoryState} }

// IsKnown reports whether c is a recognised Category.
func (c Category) IsKnown() bool {
	for _, candidate := range AllCategories() {
		if candidate == c {
			return true
		}
	}
	return false
}

// BudgetSignature names which kernel-budget dials a kind consumes. The
// archetype layer typically sets this; concrete kinds may override.
type BudgetSignature string

const (
	BudgetNone    BudgetSignature = "none"
	BudgetLLM     BudgetSignature = "llm"
	BudgetTool    BudgetSignature = "tool"
	BudgetInherit BudgetSignature = "inherit"
)

// AllBudgetSignatures returns every recognised BudgetSignature. Note
// the canonical surface form on disk uses `budget: <signature>` for
// archetypes. The richer, multi-dial `budget_consumes:` form is parsed
// into ConsumedBudgets on Manifest.
func AllBudgetSignatures() []BudgetSignature {
	return []BudgetSignature{BudgetNone, BudgetLLM, BudgetTool, BudgetInherit}
}

// IsKnown reports whether b is a recognised BudgetSignature.
func (b BudgetSignature) IsKnown() bool {
	for _, candidate := range AllBudgetSignatures() {
		if candidate == b {
			return true
		}
	}
	return false
}

// AttrSpec describes one attribute on a manifest. Exactly one entry per
// distinct attr name; deep-merge across the inheritance chain is by
// attr-name key.
//
// Field tags use yaml.v3's reflect-based decoder. Both `default:` and
// `enum:` carry user-typed values whose Go shape is
// per-AttrType-specific; we keep them as `interface{}` for round-trip
// fidelity and let the meta-validator (a later task) tighten the types.
type AttrSpec struct {
	// Type is the AttrType discriminator (FR-003).
	Type AttrType `yaml:"type" json:"type"`
	// Required marks the attr as required. Default false.
	Required bool `yaml:"required,omitempty" json:"required,omitempty"`
	// Default is the per-attr default value (typed by Type).
	Default interface{} `yaml:"default,omitempty" json:"default,omitempty"`
	// Enum lists the allowed values when Type == AttrTypeEnum.
	Enum []string `yaml:"enum,omitempty" json:"enum,omitempty"`
	// Min / Max bound numeric attrs. We use *float64 so the meta
	// schema can distinguish "0" from "unset".
	Min *float64 `yaml:"min,omitempty" json:"min,omitempty"`
	Max *float64 `yaml:"max,omitempty" json:"max,omitempty"`
	// MinLength / MaxLength bound string and array attrs.
	MinLength *int `yaml:"min_length,omitempty" json:"min_length,omitempty"`
	MaxLength *int `yaml:"max_length,omitempty" json:"max_length,omitempty"`
	// Description becomes the generated Go doc-comment + frontend tooltip.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// JSONField overrides the on-the-wire JSON field name (defaults to the
	// attr-map key).
	JSONField string `yaml:"json_field,omitempty" json:"json_field,omitempty"`
	// GoField overrides the generated Go struct field name (defaults to
	// PascalCase of the attr-map key).
	GoField string `yaml:"go_field,omitempty" json:"go_field,omitempty"`
	// DeprecatedAliasOf maps a renamed attr to its current name during
	// YAML load (FR-003).
	DeprecatedAliasOf string `yaml:"deprecated_alias_of,omitempty" json:"deprecated_alias_of,omitempty"`
}

// PortSpec describes one port on a manifest. PortSpec mirrors the
// existing agentgraph.Port type plus a `default_for: <archetype>` flag
// (FR-002). The archetype layer typically declares the canonical ports;
// kinds may override with the same merge rule (whole-list replace).
type PortSpec struct {
	Name        string `yaml:"name" json:"name"`
	Type        string `yaml:"type" json:"type"`
	Required    bool   `yaml:"required,omitempty" json:"required,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// DefaultFor names the archetype the port is the canonical "default"
	// for; surfaced to the frontend palette.
	DefaultFor string `yaml:"default_for,omitempty" json:"default_for,omitempty"`
}

// PortSet groups a manifest's input + output ports. Whole-list replace
// across inheritance: a kind that sets `ports.inputs:` replaces the
// archetype's inputs entirely (FR-004).
type PortSet struct {
	Inputs  []PortSpec `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Outputs []PortSpec `yaml:"outputs,omitempty" json:"outputs,omitempty"`
}

// Manifest is the on-disk YAML shape of one archetype or kind. Per
// FR-002 every shipped manifest YAML conforms to this struct.
//
// The `Archetype` and `ID` fields encode WHICH layer this manifest
// represents:
//   - Archetype manifests set `archetype: <name>` and leave Kind empty.
//   - Kind manifests set `id: <kebab>` (matches filename stem) and
//     `extends: <archetype-id>`.
//
// We deliberately accept either spelling at the top level for layer-zero
// flexibility; the loader normalises Archetype into ID at parse time so
// the rest of the package only deals with `ID`.
type Manifest struct {
	// SchemaVersion pins the manifest-schema revision (manifest format
	// itself, not the kind's content version). Optional; defaults to "1".
	SchemaVersion string `yaml:"schema_version,omitempty" json:"schema_version,omitempty"`

	// ID is the canonical identifier, kebab-case, matching the filename
	// stem. Required for kind manifests; for archetype manifests the
	// loader fills ID from the Archetype field.
	ID string `yaml:"id,omitempty" json:"id,omitempty"`

	// Archetype is the archetype name (e.g. "compute", "read"). When
	// non-empty the manifest is treated as an archetype declaration; the
	// loader sets ID = Archetype and Callable defaults to false.
	Archetype string `yaml:"archetype,omitempty" json:"archetype,omitempty"`

	// KindName is the on-the-wire NodeKind value. Defaults to ID.
	KindName string `yaml:"kind_name,omitempty" json:"kind_name,omitempty"`

	// DisplayName is the frontend palette label.
	DisplayName string `yaml:"display_name,omitempty" json:"display_name,omitempty"`

	// Description is the frontend tooltip + generated doc-comment.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Category is required at the archetype layer (compute / control /
	// state); inherited at the kind layer through the resolver.
	Category Category `yaml:"category,omitempty" json:"category,omitempty"`

	// Extends references another manifest's ID (single-parent only;
	// FR-004). Empty at the top of a chain.
	Extends string `yaml:"extends,omitempty" json:"extends,omitempty"`

	// Callable defaults to true for kinds and false for archetypes. The
	// loader applies the default after parsing using the `callablePtr`
	// shim so we can distinguish "explicitly false" from "unset".
	Callable    *bool `yaml:"callable,omitempty" json:"callable,omitempty"`

	// Attrs maps attr-name → AttrSpec. Deep-merged across inheritance.
	Attrs map[string]AttrSpec `yaml:"attrs,omitempty" json:"attrs,omitempty"`

	// Ports declares input + output ports.
	Ports PortSet `yaml:"ports,omitempty" json:"ports,omitempty"`

	// Budget is the single-token budget signature (one of llm / tool /
	// none / inherit). Mutually-exclusive with the BudgetConsumes list
	// (which is the richer, multi-dial form).
	Budget BudgetSignature `yaml:"budget,omitempty" json:"budget,omitempty"`

	// BudgetConsumes lists the kernel-budget dials this manifest
	// consumes (e.g. [tokens, llm_calls, cost_usd]). Surfaced to the
	// kernel's budget bookkeeper in a later WP.
	BudgetConsumes []string `yaml:"budget_consumes,omitempty" json:"budget_consumes,omitempty"`

	// Executor is the Go-symbol reference (for callable kinds). Resolved
	// through the executor registry at startup. Empty at archetype layer.
	Executor string `yaml:"executor,omitempty" json:"executor,omitempty"`

	// Aliases is the list of deprecated names that resolve to this kind.
	// Whole-list replace under inheritance.
	Aliases []string `yaml:"aliases,omitempty" json:"aliases,omitempty"`

	// Defaults maps attr-name → default value. Merged key-by-key.
	Defaults map[string]interface{} `yaml:"defaults,omitempty" json:"defaults,omitempty"`

	// Version is the manifest revision (separate from SchemaVersion). The
	// loader records it alongside the catalog entry and exposes it for
	// trace/log surfaces.
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
}

// IsArchetype reports whether the manifest declares the abstract layer.
// True when `archetype:` was set in YAML; the loader normalises ID to
// Archetype for archetype manifests.
func (m *Manifest) IsArchetype() bool { return m.Archetype != "" }

// IsCallable reports whether the manifest is callable in v1. Archetype
// manifests are non-callable by spec (FR-007). Kind manifests default
// to callable; YAML may set `callable: false` explicitly.
func (m *Manifest) IsCallable() bool {
	if m.IsArchetype() {
		return false
	}
	if m.Callable == nil {
		return true
	}
	return *m.Callable
}

// Provenance maps a field-path within the resolved manifest to the
// manifest layer that contributed it. Layer values are manifest IDs
// (e.g. "compute", "read", "model"). For user-override merges (WP07)
// the loader writes "user-override" as the layer.
//
// Field paths use a dotted notation:
//   - Top-level fields: "category", "ports.inputs", "budget"
//   - Per-attr entries: "attrs.<name>" (and per-property where the
//     resolver is fine-grained; v1 resolves to attr-name granularity)
//   - Defaults entries: "defaults.<name>"
type Provenance map[string]string

// ResolvedManifest is the merged result of an inheritance chain. Every
// callable kind has exactly one ResolvedManifest after the loader runs.
//
// The `Manifest` field is the merged shape; `Chain` records the
// inheritance walk root-first (e.g. ["state", "read", "corpus_read"]);
// `Provenance` traces every field back to its contributing layer.
//
// Hash is the sha256 of the canonical YAML bytes of the kind manifest
// only (NOT the merged shape). The frontend uses Hash for cache busting
// and the trace records it alongside the manifest version.
type ResolvedManifest struct {
	Manifest   Manifest
	Chain      []string
	Provenance Provenance
	Hash       string
	Source     string
}

// boolPtr is a tiny convenience used by tests + the loader to set
// Manifest.Callable.
func boolPtr(b bool) *bool { return &b }
