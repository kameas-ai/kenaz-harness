// Package fleet — synckind.go
//
// SyncKind registry (spec §2.1, fleet-generic-sync-framework-01NSYNC02 WP01).
//
// One registration point that every syncable kind — the five existing
// per-user LWW categories, ORGX01's org-scoped kinds, and future kinds
// (slash commands, hooks, keybindings, workflow templates, …) — plugs into.
// Registering a new user-scoped kind is one SyncKind + a fleet category row;
// no new endpoints (`/sync/{category}` is already generic server-side —
// DESIGN.md §5.6).
//
// This file adds the registry *type surface* and an adapter
// (SyncKind.CategoryConfig) that lets a SyncKind ride the existing
// Syncer/CategoryConfig LWW push-pull machinery in sync.go unchanged. It
// deliberately does not alter Syncer's public behavior — see sync.go's
// RegisterCategory / pollLoop comments for the (behavior-preserving)
// genericity fix that lets a registry-driven kind join the poll loop
// without a hand-edited category list.
//
// Fork-removal recipe: delete this file alongside sync.go (see its header).
package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Scope identifies which layer(s) may carry a SyncKind's payload.
// Per DESIGN.md §6 classification, effective config layers read down as
// org → team → user; a kind declares which of these it participates in.
type Scope string

const (
	// ScopeUser is the per-device, per-user LWW layer (today's five
	// categories all live here).
	ScopeUser Scope = "user"
	// ScopeTeam is a team-provisioned, read-only layer.
	ScopeTeam Scope = "team"
	// ScopeOrg is an org-provisioned, read-only layer (ConfigBundle
	// org_config — WP02+).
	ScopeOrg Scope = "org"
)

// Transport identifies the wire mechanism a SyncKind rides.
type Transport string

const (
	// TransportLWWCategory rides PUT/GET /api/v1/sync/{category} —
	// last-writer-wins per user (the mechanism in sync.go).
	TransportLWWCategory Transport = "lww_category"
	// TransportBundlePushdown rides the signed ConfigBundle's keyed
	// org_config section (Pattern D fan-out — WP02+).
	TransportBundlePushdown Transport = "bundle_pushdown"
	// TransportCatalog rides the catalog publish/list/install surface
	// (Pattern A, on-demand — e.g. skills).
	TransportCatalog Transport = "catalog"
	// TransportUnit rides the unified Unit model (content-like kinds:
	// contexts, artifacts, memory — out of scope for this mission).
	TransportUnit Transport = "unit"
)

// SecretPolicy governs whether a kind's payload may carry credential bytes.
// v1 has exactly one value: every kind's payload must be secret-free. The
// one amendment (org-shared provider keys, spec §6.2) rides a dedicated
// encrypted channel straight into the device credstore — never a SyncKind
// payload — so it does not need a second policy value here.
type SecretPolicy string

// SecretPolicyMustNotContainSecrets is the only SecretPolicy value in v1.
const SecretPolicyMustNotContainSecrets SecretPolicy = "must_not_contain_secrets"

// ConflictPolicy governs how a same-ID collision between layers resolves.
type ConflictPolicy string

const (
	// ConflictPolicyLWW is last-writer-wins by timestamp — today's
	// per-user cross-device semantics. The default for user-only kinds.
	ConflictPolicyLWW ConflictPolicy = "lww"
	// ConflictPolicyOrgWinsReadonly shadows the personal entry with a
	// provenance badge; the org/team entry always wins for effective
	// config, and the personal entry is never deleted (spec §2.2).
	ConflictPolicyOrgWinsReadonly ConflictPolicy = "org_wins_readonly"
	// ConflictPolicyMerge combines layers instead of one replacing the
	// other (kind-declared; no v1 kind uses this yet).
	ConflictPolicyMerge ConflictPolicy = "merge"
)

// KindCollector serializes local user-scope state for a kind just before a
// push. HARD RULE (mirrors CategoryCollector): must never return credential
// bytes — SecretPolicy is enforced centrally by the framework (WP06), but
// every collector is independently responsible for not emitting secrets.
type KindCollector func(ctx context.Context) ([]byte, error)

// KindApplier applies an incoming payload for one layer (scope) of a kind.
// Today (WP01/WP05) only ScopeUser appliers are exercised — the org/team
// dispatch path lands in WP02's composite ConfigBundle applier.
type KindApplier func(ctx context.Context, scope Scope, payload []byte) error

// SyncKind is one registration in the generic sync framework (spec §2.1).
// It carries no wire logic of its own — Collect/Apply are plain functions
// the registry's owner (core/rpc, which may see both core/fleet and the
// settings/slashcmd stores) supplies as closures.
type SyncKind struct {
	// ID is the kind identifier: "provider_profiles", "installed_mcp",
	// "slash_commands", … — the same string used as the SyncCategory /
	// org_config map key on the wire.
	ID string
	// Scopes lists which layers may carry this kind's payload.
	Scopes []Scope
	// Transport identifies the wire mechanism.
	Transport Transport
	// Collect serializes local user-scope state. Nil for org-only kinds
	// that the harness never originates (e.g. a future admin-authored
	// kind with no personal counterpart).
	Collect KindCollector
	// Apply applies an incoming payload for the given scope. Nil for
	// kinds that are collect-only (none in v1, but the type allows it).
	Apply KindApplier
	// SecretPolicy declares the kind's secret posture. Required.
	SecretPolicy SecretPolicy
	// ConflictPolicy declares how same-ID collisions across layers
	// resolve. Required.
	ConflictPolicy ConflictPolicy
}

// HasScope reports whether the kind is declared for the given scope.
func (k SyncKind) HasScope(s Scope) bool {
	for _, sc := range k.Scopes {
		if sc == s {
			return true
		}
	}
	return false
}

// validate checks the required fields of a SyncKind registration. Called by
// KindRegistry.Register so a mis-registered kind fails loudly at boot
// rather than misbehaving silently at sync time.
func (k SyncKind) validate() error {
	if k.ID == "" {
		return fmt.Errorf("fleet/synckind: ID must not be empty")
	}
	if len(k.Scopes) == 0 {
		return fmt.Errorf("fleet/synckind: %s: at least one Scope is required", k.ID)
	}
	if k.Transport == "" {
		return fmt.Errorf("fleet/synckind: %s: Transport is required", k.ID)
	}
	if k.SecretPolicy == "" {
		return fmt.Errorf("fleet/synckind: %s: SecretPolicy is required", k.ID)
	}
	if k.ConflictPolicy == "" {
		return fmt.Errorf("fleet/synckind: %s: ConflictPolicy is required", k.ID)
	}
	return nil
}

// CategoryConfig adapts a SyncKind's Collect/Apply into the CategoryConfig
// shape the Syncer's LWW push/pull loop understands (sync.go). This is
// purely an adapter — user-scope kinds ride the existing per-user
// /api/v1/sync/{category} transport unchanged; no new mechanism is
// introduced. Apply is always invoked with ScopeUser here because the LWW
// transport is per-user by construction (org/team application dispatches
// through the WP02 ConfigBundle applier instead, not through this adapter).
func (k SyncKind) CategoryConfig() CategoryConfig {
	cfg := CategoryConfig{}
	if k.Collect != nil {
		collect := k.Collect
		cfg.Collector = func(ctx context.Context) (json.RawMessage, error) {
			raw, err := collect(ctx)
			if err != nil {
				return nil, err
			}
			return json.RawMessage(raw), nil
		}
	}
	if k.Apply != nil {
		apply := k.Apply
		cfg.Applier = func(ctx context.Context, raw json.RawMessage) error {
			return apply(ctx, ScopeUser, []byte(raw))
		}
	}
	return cfg
}

// KindRegistry is the single registration point every SyncKind plugs into.
// Safe for concurrent use.
type KindRegistry struct {
	mu    sync.RWMutex
	kinds map[string]SyncKind
}

// NewKindRegistry constructs an empty registry.
func NewKindRegistry() *KindRegistry {
	return &KindRegistry{kinds: make(map[string]SyncKind)}
}

// Register adds a SyncKind to the registry. Returns an error if the kind
// fails validation or its ID is already registered — re-registration is a
// call-site bug and must fail loudly rather than silently overwrite.
func (r *KindRegistry) Register(k SyncKind) error {
	if err := k.validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.kinds[k.ID]; exists {
		return fmt.Errorf("fleet/synckind: kind %q already registered", k.ID)
	}
	r.kinds[k.ID] = k
	return nil
}

// Kind returns the registered SyncKind for id.
func (r *KindRegistry) Kind(id string) (SyncKind, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k, ok := r.kinds[id]
	return k, ok
}

// Kinds returns every registered SyncKind, sorted by ID for determinism.
func (r *KindRegistry) Kinds() []SyncKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SyncKind, 0, len(r.kinds))
	for _, k := range r.kinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// IDs returns every registered kind ID, sorted.
func (r *KindRegistry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.kinds))
	for id := range r.kinds {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
