// Package pack parses and validates context-pack artifacts on disk.
//
// A pack is a directory tree of the form:
//
//	pack-root/
//	  pack.yaml          metadata, layer designation, signature ref
//	  entries/
//	    <kind>/
//	      <name>.md      YAML frontmatter + Markdown body
//	  signatures/
//	    pack.sig         detached signature envelope (opaque to this package)
//
// The parser produces a typed [ContextPack] value. The validator runs
// before any provenance call and enforces schema, naming uniqueness,
// size budgets, and signature-reference presence.
package pack

import (
	"fmt"
	"sort"
	"time"
)

// Layer is the spec's three-tier precedence designation.
// Precedence is personal > team > org (FR-001).
type Layer string

const (
	LayerOrg      Layer = "org"
	LayerTeam     Layer = "team"
	LayerPersonal Layer = "personal"
)

// Precedence returns the spec-declared rank: personal=3, team=2, org=1.
// Higher values win in override-by-precedence policy.
func (l Layer) Precedence() int {
	switch l {
	case LayerPersonal:
		return 3
	case LayerTeam:
		return 2
	case LayerOrg:
		return 1
	default:
		return 0
	}
}

// Valid reports whether l is one of the spec's three tiers.
func (l Layer) Valid() bool {
	switch l {
	case LayerOrg, LayerTeam, LayerPersonal:
		return true
	}
	return false
}

// EntryKind enumerates the four spec-declared kinds of context entry
// (Key Entities → Context Entry).
type EntryKind string

const (
	KindGlossary    EntryKind = "glossary"
	KindExplanation EntryKind = "explanation"
	KindSkill       EntryKind = "skill"
	KindGuidance    EntryKind = "guidance"
)

// Valid reports whether k is one of the spec-declared kinds.
func (k EntryKind) Valid() bool {
	switch k {
	case KindGlossary, KindExplanation, KindSkill, KindGuidance:
		return true
	}
	return false
}

// PackRef identifies a pack across cache, lockfile, and event-log boundaries.
type PackRef struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Layer       Layer  `json:"layer"`
	ContentHash string `json:"content_hash"`
}

// String renders a stable human-readable identifier.
func (r PackRef) String() string {
	return fmt.Sprintf("%s@%s/%s#%s", r.Layer, r.Name, r.Version, r.ContentHash)
}

// Scope captures workflow / agent restrictions parsed from entry frontmatter
// (FR-015).  An empty Scope means "applies everywhere".
type Scope struct {
	Workflows []string `json:"workflows,omitempty" yaml:"workflows,omitempty"`
	Agents    []string `json:"agents,omitempty" yaml:"agents,omitempty"`
}

// Empty reports whether the scope is unrestricted.
func (s Scope) Empty() bool { return len(s.Workflows) == 0 && len(s.Agents) == 0 }

// Allows reports whether an entry with this scope applies for the given
// workflow / agent. An entry with no workflow restriction applies to all
// workflows; the same for agents. Both restrictions, when present, must
// match.
func (s Scope) Allows(workflow, agent string) bool {
	if len(s.Workflows) > 0 && !contains(s.Workflows, workflow) {
		return false
	}
	if len(s.Agents) > 0 && !contains(s.Agents, agent) {
		return false
	}
	return true
}

func contains(haystack []string, needle string) bool {
	if needle == "" {
		return false
	}
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

// AccessPolicy describes the gating predicates a pack carries
// (FR-009 / NFR-005). Concrete enforcement lives in the scope sub-package;
// this struct captures only the declared intent.
type AccessPolicy struct {
	RequiredRoles    []string `json:"required_roles,omitempty" yaml:"required_roles,omitempty"`
	CredentialRefID  string   `json:"credential_ref,omitempty" yaml:"credential_ref,omitempty"`
	RestrictedToOrg  string   `json:"restricted_to_org,omitempty" yaml:"restricted_to_org,omitempty"`
	RestrictedToTeam string   `json:"restricted_to_team,omitempty" yaml:"restricted_to_team,omitempty"`
}

// Empty reports whether the access policy imposes no restrictions.
func (p AccessPolicy) Empty() bool {
	return len(p.RequiredRoles) == 0 && p.CredentialRefID == "" &&
		p.RestrictedToOrg == "" && p.RestrictedToTeam == ""
}

// SignatureRef is the on-pack pointer at the detached signature envelope
// (the envelope itself is opaque to this package — it is consumed by the
// trust verifier).
type SignatureRef struct {
	Path      string `json:"path" yaml:"path"`             // relative path within pack
	Algorithm string `json:"algorithm" yaml:"algorithm"`   // illustrative (e.g. "sigstore-bundle", "ed25519")
	AnchorID  string `json:"anchor_id" yaml:"anchor_id"`   // identity claimed by the signer
}

// Empty reports whether s carries no signature reference at all.
func (s SignatureRef) Empty() bool { return s.Path == "" && s.AnchorID == "" }

// ContextEntry is one named unit within a pack. The merge engine keys
// override decisions on Name (FR-002).
type ContextEntry struct {
	Name        string                 `json:"name"`
	Kind        EntryKind              `json:"kind"`
	Body        []byte                 `json:"body"`
	Frontmatter map[string]any         `json:"frontmatter,omitempty"`
	Scope       Scope                  `json:"scope,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
	SourceLayer Layer                  `json:"source_layer"`
	SourcePack  PackRef                `json:"source_pack"`
	ContentHash string                 `json:"content_hash"`
	SizeBytes   int64                  `json:"size_bytes"`
	RelPath     string                 `json:"rel_path"`
}

// ContextPack is a parsed, validated pack.
type ContextPack struct {
	Ref         PackRef        `json:"ref"`
	Issuer      string         `json:"issuer,omitempty"`
	License     string         `json:"license,omitempty"`
	Description string         `json:"description,omitempty"`
	Required    bool           `json:"required"`
	Access      AccessPolicy   `json:"access,omitempty"`
	Signature   SignatureRef   `json:"signature,omitempty"`
	Entries     []ContextEntry `json:"entries"`
	SizeBytes   int64          `json:"size_bytes"`
	GeneratedAt time.Time      `json:"generated_at,omitempty"`
	// Warnings are non-fatal, structured messages emitted by the validator
	// (e.g. soft size-budget overflow). Trimming is policy, not parser.
	Warnings []Warning `json:"warnings,omitempty"`
}

// Warning is a structured, non-fatal validation observation.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Subject string `json:"subject,omitempty"`
}

// EntryByName returns the pack's entry with the given name, if any.
func (p ContextPack) EntryByName(name string) (ContextEntry, bool) {
	for _, e := range p.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return ContextEntry{}, false
}

// EntryNames returns entry names in deterministic order.
func (p ContextPack) EntryNames() []string {
	names := make([]string, 0, len(p.Entries))
	for _, e := range p.Entries {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	return names
}
