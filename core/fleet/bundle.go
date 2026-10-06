// Package fleet — bundle.go
//
// Bundle is the wire shape for fleet-distributed config bundles. The server
// signs a canonical JSON representation of the bundle (all fields except
// "signature") with an ed25519 private key whose public counterpart is
// embedded at build time via ldflags into signing_key.go.
//
// Verification pipeline (mission fleet-config-pull-01NDFSEX10 WP01):
//  1. Canonicalize: marshal the bundle without the "signature" field → SHA-256 hash.
//  2. Decode the base64url signature field.
//  3. ed25519.Verify against the pinned key the signed "key_id" selects
//     (or, for a bundle without key_id, any pinned key) — FleetSigningKeys().
//  4. Monotonic bundle_id guard: new ID must be strictly greater than lastID.
package fleet

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// Bundle is the wire-shape of a fleet config bundle. All fields are exported
// so they round-trip through JSON cleanly.
//
// Wire shape (locked decision, spec §5):
//
//	{
//	  "bundle_id":         42,
//	  "key_id":            "0123456789abcdef",
//	  "issued_at":         "2026-05-16T12:00:00Z",
//	  "cedar_delta":       {...},
//	  "mcp_allowlist":     ["github", "slack", ...],
//	  "model_prefs":       {"default_model": "...", "provider_allowlist": [...]},
//	  "kameas_ml_weight_urls": ["https://..."],
//	  "mandated_items":    [{"catalog_id": "<uuid>", "kind": "skill", "version": "1.0.0", "payload": {...}}],
//	  "provisioned_mcp":   [{"recipe_id": "slack", "primary_auth": "oauth", ...}],
//	  "provider_setups":   [{"provider": "anthropic", "access_mode": "org_shared_key", ...}],
//	  "signature":         "<base64 ed25519>"
//	}
//
// "signature" is the ed25519 signature over SHA-256(canonical JSON minus the
// "signature" field). Base64 standard-encoding, no padding (URL-safe alphabet
// accepted on decode).
type Bundle struct {
	// BundleID is a monotonically increasing integer used to prevent replay attacks.
	// The harness tracks the last applied bundle_id on disk and rejects any bundle
	// whose ID is not strictly greater.
	BundleID int64 `json:"bundle_id"`

	// KeyID names the key that signed this bundle: lowercase hex of the
	// first 8 bytes of SHA-256 over the RAW 32-byte ed25519 public key
	// (SigningKeyID; cross-repo contract with kenaz-fleet's
	// KeyIDForPublicKey). It is INSIDE the signed payload, so it cannot be
	// rewritten in transit to steer verification. When present,
	// VerifyWithKeySet verifies ONLY with the pinned key of that key_id (an
	// unmatched key_id is ErrSigningKeyUnknown); when absent, every pinned
	// key is tried.
	//
	// omitempty: a bundle without key_id must marshal its signing payload
	// exactly as a pre-key_id signer produced it. Field position (directly
	// after bundle_id) matches kenaz-fleet's struct order — the signing
	// payload is struct-ordered JSON, so the order is part of the contract.
	KeyID string `json:"key_id,omitempty"`

	// IssuedAt is the server-side issuance time in RFC 3339.
	IssuedAt time.Time `json:"issued_at"`

	// CedarDelta carries new Cedar policy statements to merge into the engine.
	// The value is opaque JSON forwarded as-is to cedar_apply.go.
	CedarDelta json.RawMessage `json:"cedar_delta,omitempty"`

	// MCPAllowlist is the slice of MCP recipe IDs the fleet admin permits.
	// nil means "no fleet restriction" (all recipes allowed).
	// An empty (non-nil) slice means "block all" — no recipes may be installed.
	// The field is NOT omitempty so an explicit empty array round-trips through
	// JSON as [] (distinct from nil/absent) and the ed25519 signature covers it.
	MCPAllowlist []string `json:"mcp_allowlist"`

	// ModelPrefs specifies the fleet-managed model preferences.
	ModelPrefs *BundleModelPrefs `json:"model_prefs,omitempty"`

	// KameasMLWeightURLs is the list of weight-file URLs for kameas-ml.
	// Persisted to disk for the kameas-ml consumer; not applied in-process.
	KameasMLWeightURLs []string `json:"kameas_ml_weight_urls,omitempty"`

	// MandatedItems is the push-down section for org-admin-REQUIRED catalog
	// items of every kind (owner wire-contract ruling 2026-10-06, WP02). It
	// REPLACES mandated_skills, which carried every mandated kind as a raw
	// payload with no kind or catalog id, so the harness installed mandated
	// workflows as broken "skills" (audit §0-F). There is no compat field:
	// nothing was ever delivered on mandated_skills.
	//
	// Each envelope names its catalog_id, kind (skill|workflow|pack|bundle),
	// version and raw payload; the applier dispatches by kind and reconciles
	// against the previously applied set (mandated.go). Fleet sorts items by
	// (kind, catalog_id) so the signed bytes are stable.
	//
	// Wire contract: kenaz-fleet PR #178 (service/config_bundle.go
	// BundleMandatedItem / Bundle.MandatedItems) — json tag, item field
	// order, and the slot directly after kameas_ml_weight_urls (fleet's
	// bundle has no provisioned_mcp/provider_setups/org_config after it;
	// those harness-only fields are omitempty and absent from fleet bundles).
	// Payload bytes are OPAQUE: never re-marshalled on the apply path.
	//
	// omitempty: a bundle without mandated items keeps the signing payload
	// minimal — and an absent section means "nothing is mandated", so the
	// applier removes everything previously mandated.
	MandatedItems []BundleMandatedItem `json:"mandated_items,omitempty"`

	// ProvisionedMCP is the push-down section for org-provisioned MCP
	// servers (fleet-org-config-inheritance-01NORGX01 §3.1). Each entry
	// names a recipe, an optional transport/URL override, the org's
	// declared auth mechanism, and (for OAuth) the org's PUBLIC PKCE
	// client_id + scopes — never a bearer token, bot token, or API key.
	//
	// SECURITY: an entry here is a command line the harness spawns or a
	// URL it connects to. It MUST be covered by the ed25519 signature
	// (see bundleSigningPayload below) — an unsigned ProvisionedMCP
	// section would let anyone who can modify the bundle in transit
	// dictate what the harness executes. Applied by
	// compositeConfigApplier.ApplyBundle -> recipes.ApplyProvisionedMCP
	// (core/rpc/views/settings/fleet.go, core/mcp/recipes/org.go —
	// fleet-org-config-inheritance-01NORGX01 WP02), which installs each
	// entry as the highest-precedence ("org_wins_readonly") layer of
	// core/mcp/recipes' MergedCatalog.
	//
	// omitempty: orgs not using this feature keep the signing payload
	// minimal. Unlike MCPAllowlist there is no "block all" semantic for
	// provisioning — nil, an empty slice, and an absent key are all
	// equivalent ("no org-provisioned MCP entries"), so the nil-vs-empty
	// distinction that matters for MCPAllowlist does not apply here. See
	// bundle_test.go's TestBundle_ProvisionedMCP_EmptyVsAbsentVsNil.
	ProvisionedMCP []ProvisionedMCP `json:"provisioned_mcp,omitempty"`

	// ProviderSetups is the push-down section for org-provisioned model
	// providers (fleet-org-config-inheritance-01NORGX01 §3.1, as amended
	// by the owner resolution recorded in
	// fleet-generic-sync-framework-01NSYNC02 §6.2, 2026-07-18: fleet is
	// never in the inference path — no broker, no org-hosted gateway
	// distributed this way). AccessMode is "org_shared_key" (the actual
	// key rides a DEDICATED ENCRYPTED CHANNEL directly into the device
	// credstore — never this field, never any bundle, never Wails RPC,
	// frontend state, or logs) or "byo_key" (config inherits; the member
	// supplies their own key locally).
	//
	// SECURITY: same signing requirement as ProvisionedMCP — this section
	// steers which provider/model a member's harness talks to, so it must
	// be covered by the signature. WP01 (this field) only adds the wire
	// contract; the apply pipeline (WP04) is DEFERRED — see
	// bundle_knob_coverage.go's RegisterDeferred entry and
	// docs/unwired-ledger.md. Blocker: kenaz-fleet org endpoints and the
	// dedicated encrypted org-key channel WP04 requires do not exist yet
	// (plan.md Gates: "no harness WP04 merge before a fleet dev
	// environment can exercise it"). Owner: alec.
	//
	// omitempty: same nil/empty/absent equivalence as ProvisionedMCP —
	// there is no meaningful distinction for a push-down-only section.
	ProviderSetups []ProviderSetup `json:"provider_setups,omitempty"`

	// OrgConfig is the generalized keyed section for org/team-scoped
	// SyncKind payloads (fleet-generic-sync-framework-01NSYNC02 §2.1 WP02).
	// The map key is a registered SyncKind.ID ("provider_profiles",
	// "installed_mcp", a future kind, …); the value is that kind's opaque,
	// kind-defined payload. This supersedes the pattern of adding a
	// bespoke Bundle field per org-scoped kind (as MandatedSkills did, and
	// as ProvisionedMCP/ProviderSetups above do today) — new org kinds add
	// a registry entry, not a new field here.
	//
	// Canonicalization: encoding/json marshals Go maps with string keys in
	// sorted-key order (documented stdlib behavior), so the signing
	// payload below is deterministic across repeated marshals of the same
	// map without any extra sorting step here. TestOrgConfig_SigningPayload_
	// StableKeyOrder pins this so a future encoding/json change — or a
	// hand-rolled "optimization" that bypasses json.Marshal — cannot
	// silently reintroduce non-determinism into a signed payload.
	//
	// SECURITY: same signing requirement as ProvisionedMCP/ProviderSetups —
	// every entry here is applied read-only on the member's device, so it
	// must be covered by the ed25519 signature (see bundleSigningPayload
	// below). FR-006's central secret-shape rejection (WP06) still governs
	// what may be collected into a kind's payload; this field only carries
	// entries a kind already agreed are secret-free — the one exception
	// (org-shared provider keys) rides the dedicated encrypted credstore
	// channel described in spec §6.2, never this map.
	//
	// omitempty: an org with no org_config kinds configured keeps the
	// signing payload minimal, same rationale as MandatedSkills/
	// ProvisionedMCP above.
	OrgConfig map[string]json.RawMessage `json:"org_config,omitempty"`

	// Signature is the base64-encoded ed25519 signature over the SHA-256 of
	// the canonical JSON of this bundle with the "signature" field absent.
	// This field is excluded from the signing input.
	Signature string `json:"signature"`
}

// ProvisionedMCP is one org-provisioned MCP server entry (see
// Bundle.ProvisionedMCP's doc for the security rationale). All fields are
// non-secret by construction — there is deliberately no field here that
// could carry key/token material; see bundle_test.go's
// TestBundle_ProvisionedMCP_NoSecretField for the negative-shape check that
// backs FR-008.
type ProvisionedMCP struct {
	// RecipeID identifies which MCP recipe this entry configures.
	RecipeID string `json:"recipe_id"`
	// Transport optionally overrides the recipe's default transport
	// (e.g. "http", "stdio").
	Transport string `json:"transport,omitempty"`
	// URL is the remote server endpoint for transports that use one.
	URL string `json:"url,omitempty"`
	// PrimaryAuth is the org's declared auth mechanism for this recipe:
	// "oauth" | "device_code" | "none" | "keys".
	PrimaryAuth string `json:"primary_auth,omitempty"`
	// OAuth carries the org's PUBLIC OAuth client_id + scopes for a PKCE
	// flow. ClientID is NOT a secret (PKCE public client) — it is never a
	// bearer or bot token.
	OAuth *ProvisionedMCPOAuth `json:"oauth,omitempty"`
	// Config carries non-secret config_options overrides for the recipe.
	// MUST NOT contain credential bytes (FR-008) — this is not the
	// dedicated org-secret channel and never will be.
	Config json.RawMessage `json:"config,omitempty"`
}

// ProvisionedMCPOAuth is the org-owned PKCE client identity a
// ProvisionedMCP entry inherits. ClientID is a public PKCE client
// identifier, not a secret.
type ProvisionedMCPOAuth struct {
	ClientID string   `json:"client_id,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

// ProviderSetup is one org-provisioned model-provider entry (see
// Bundle.ProviderSetups's doc for the access-mode resolution history).
// There is deliberately no field here that could carry or reference key
// material — the org-shared key rides the dedicated encrypted channel
// straight into the device credstore, never this struct.
type ProviderSetup struct {
	// Provider is the provider identifier (e.g. "anthropic", "openai").
	Provider string `json:"provider"`
	// AccessMode is "org_shared_key" (key delivered out-of-band to the
	// credstore) or "byo_key" (member supplies their own key; only this
	// config is inherited).
	AccessMode string `json:"access_mode"`
	// Models optionally restricts/lists the models exposed for this
	// provider.
	Models []string `json:"models,omitempty"`
	// Default marks this provider as the harness's default profile when
	// applied (FR-005).
	Default bool `json:"default,omitempty"`
}

// BundleMandatedItem is one org-mandated catalog item envelope inside the
// signed bundle (Bundle.MandatedItems). NO omitempty on any field: the item
// shape is fixed so the signed bytes are identical on both sides. Mirrors
// kenaz-fleet PR #178 service/config_bundle.go BundleMandatedItem.
type BundleMandatedItem struct {
	// CatalogID is the fleet catalog item UUID — the stable reconciliation key.
	CatalogID string `json:"catalog_id"`
	// Kind is skill | workflow | pack | bundle (fleet's catalog kind set).
	Kind string `json:"kind"`
	// Version is the catalog item version mandated.
	Version string `json:"version"`
	// Payload is the catalog item's raw payload (a slashcmd.Skill JSON for a
	// skill, a workflow document for a workflow).
	Payload json.RawMessage `json:"payload"`
}

// BundleModelPrefs is the model-preferences section of a config bundle.
type BundleModelPrefs struct {
	// DefaultModel is the fleet-preferred default model identifier.
	DefaultModel string `json:"default_model,omitempty"`
	// ProviderAllowlist is the set of provider names the fleet admin permits.
	// nil means "no fleet restriction".
	ProviderAllowlist []string `json:"provider_allowlist,omitempty"`
}

// bundleSigningPayload is the shape marshalled to produce the signature input.
// It mirrors Bundle but omits the Signature field.
// MCPAllowlist is NOT omitempty so an explicit empty array (block-all) is
// included in the signature and the verify+apply path can distinguish nil
// (no restriction) from [] (block-all). Must stay in sync with Bundle above —
// same fields, same ORDER (encoding/json emits struct order, and kenaz-fleet
// signs its own struct with key_id directly after bundle_id).
// TestSigningPayload_EveryBundleFieldAffectsPayload catches a missing field;
// TestSigningPayload_KeyIDFieldOrder pins the key_id position.
type bundleSigningPayload struct {
	BundleID           int64                      `json:"bundle_id"`
	KeyID              string                     `json:"key_id,omitempty"`
	IssuedAt           time.Time                  `json:"issued_at"`
	CedarDelta         json.RawMessage            `json:"cedar_delta,omitempty"`
	MCPAllowlist       []string                   `json:"mcp_allowlist"`
	ModelPrefs         *BundleModelPrefs          `json:"model_prefs,omitempty"`
	KameasMLWeightURLs []string                   `json:"kameas_ml_weight_urls,omitempty"`
	MandatedItems      []BundleMandatedItem       `json:"mandated_items,omitempty"` // slot: fleet PR #178
	ProvisionedMCP     []ProvisionedMCP           `json:"provisioned_mcp,omitempty"`
	ProviderSetups     []ProviderSetup            `json:"provider_setups,omitempty"`
	OrgConfig          map[string]json.RawMessage `json:"org_config,omitempty"`
}

// signingPayload produces the canonical JSON bytes that were signed (all
// fields except "signature"). The SHA-256 of these bytes is what ed25519
// signs.
func (b *Bundle) signingPayload() ([]byte, error) {
	p := bundleSigningPayload{
		BundleID:           b.BundleID,
		KeyID:              b.KeyID,
		IssuedAt:           b.IssuedAt,
		CedarDelta:         b.CedarDelta,
		MCPAllowlist:       b.MCPAllowlist,
		ModelPrefs:         b.ModelPrefs,
		KameasMLWeightURLs: b.KameasMLWeightURLs,
		MandatedItems:      b.MandatedItems,
		ProvisionedMCP:     b.ProvisionedMCP,
		ProviderSetups:     b.ProviderSetups,
		OrgConfig:          b.OrgConfig,
	}
	return json.Marshal(p)
}

// SignBundleForTesting computes the ed25519 signature over b's canonical
// signing payload using priv and sets b.Signature (base64 standard, no
// padding — the same encoding VerifyWithKeySet accepts). Mutates b in
// place and returns an error only on marshal failure.
//
// Test seam ONLY, mirroring NewClientForTesting /
// SetSigningKeyForTesting: production bundles are signed server-side.
// Exists so cross-package tests (e.g. core/rpc/views/settings driving the
// REAL compositeConfigApplier through fleet.ConfigPoller) can build a
// bundle that passes real signature verification instead of stubbing it
// out — spec §8 rule 2 requires VerifyWithKeySet actually be reached.
func SignBundleForTesting(b *Bundle, priv ed25519.PrivateKey) error {
	payload, err := b.signingPayload()
	if err != nil {
		return err
	}
	hash := sha256.Sum256(payload)
	sig := ed25519.Sign(priv, hash[:])
	b.Signature = base64.RawStdEncoding.EncodeToString(sig)
	return nil
}

// Verify verifies the ed25519 signature of the bundle against key and
// enforces the monotonic bundle_id guard (lastAppliedID is the last
// successfully applied bundle_id, or 0 on first apply).
//
// Return values:
//   - nil              — valid, bundle may be applied
//   - ErrSigningKeyNotConfigured — key is nil; hard-reject
//   - ErrSigningKeyUnknown      — bundle key_id is not key's key_id; hard-reject
//   - ErrInvalidSignature       — signature mismatch; hard-reject
//   - ErrBundleIDNonMonotonic   — bundle_id is not strictly > lastAppliedID
//
// For pinned-set verification (key rotation, key_id routing), use
// VerifyWithKeySet.
func Verify(b *Bundle, key ed25519.PublicKey, lastAppliedID int64) error {
	if len(key) == 0 {
		return fmt.Errorf("%w: cannot verify without a signing key", ErrSigningKeyNotConfigured)
	}
	return VerifyWithKeySet(b, []ed25519.PublicKey{key}, lastAppliedID)
}

// VerifyWithKeySet verifies the bundle signature against the pinned key set
// and enforces the monotonic bundle_id guard. Key selection:
//
//   - empty key set              → ErrSigningKeyNotConfigured (fail-closed)
//   - bundle HAS key_id          → verify ONLY with the pinned key whose
//     SigningKeyID equals it; no pinned key has that id → ErrSigningKeyUnknown
//     (the install's pins predate the signer's key); the selected key does
//     not verify → ErrInvalidSignature
//   - bundle LACKS key_id        → try every pinned key; any one verifying
//     is valid, none → ErrInvalidSignature
//
// key_id is covered by the signature (bundleSigningPayload), so routing on it
// cannot be abused: a tampered key_id either selects no key (unknown) or a
// key under which the — now changed — payload no longer verifies.
//
// Rotation (docs/fleet-key-rotation.md): a release pins current+next; fleet
// flips to next; a later release drops current.
//
// The monotonic bundle_id guard runs only AFTER a signature verifies and is
// identical for every key-selection path.
func VerifyWithKeySet(b *Bundle, keys []ed25519.PublicKey, lastAppliedID int64) error {
	if len(keys) == 0 {
		return fmt.Errorf("%w: cannot verify without a signing key", ErrSigningKeyNotConfigured)
	}

	// Select candidate keys by the signed key_id.
	candidates := keys
	if b.KeyID != "" {
		candidates = nil
		for _, key := range keys {
			if len(key) == ed25519.PublicKeySize && SigningKeyID(key) == b.KeyID {
				candidates = []ed25519.PublicKey{key}
				break
			}
		}
		if candidates == nil {
			return fmt.Errorf("%w: key_id %q matches none of the %d key(s) pinned in this build — this install's pins predate the fleet's current signing key; update the harness",
				ErrSigningKeyUnknown, b.KeyID, len(keys))
		}
	}

	// Decode the signature field.
	sig, err := base64.RawStdEncoding.DecodeString(b.Signature)
	if err != nil {
		// Also accept standard base64 with padding.
		sig, err = base64.StdEncoding.DecodeString(b.Signature)
		if err != nil {
			return fmt.Errorf("%w: cannot decode signature: %v", ErrInvalidSignature, err)
		}
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature length %d != %d", ErrInvalidSignature, len(sig), ed25519.SignatureSize)
	}

	// Build the signing payload and hash it.
	payload, err := b.signingPayload()
	if err != nil {
		return fmt.Errorf("fleet: marshal signing payload: %w", err)
	}
	hash := sha256.Sum256(payload)

	// ed25519 signs the message directly (not the hash), but to align with the
	// server's convention we sign the 32-byte SHA-256 digest.
	verified := false
	for _, key := range candidates {
		if len(key) == ed25519.PublicKeySize && ed25519.Verify(key, hash[:], sig) {
			verified = true
			break
		}
	}
	if !verified {
		return ErrInvalidSignature
	}

	// Monotonic bundle_id guard.
	if b.BundleID <= lastAppliedID {
		return fmt.Errorf("%w: received %d, last applied %d", ErrBundleIDNonMonotonic, b.BundleID, lastAppliedID)
	}

	return nil
}
