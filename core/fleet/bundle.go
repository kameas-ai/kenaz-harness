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
//  3. ed25519.Verify against FleetSigningKey().
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
//	  "issued_at":         "2026-05-16T12:00:00Z",
//	  "cedar_delta":       {...},
//	  "mcp_allowlist":     ["github", "slack", ...],
//	  "model_prefs":       {"default_model": "...", "provider_allowlist": [...]},
//	  "kameas_ml_weight_urls": ["https://..."],
//	  "mandated_skills":   [{...}],
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

	// MandatedSkills is the push-down section for org-admin-required skills
	// (fleet-skills-sync-01NDFSEX18 WP05). Each entry is an opaque JSON
	// object containing the skill payload that will be installed read-only
	// on every org member's device (FR-301).
	//
	// The field is omitted from bundles that carry no mandated skills so
	// the signing payload stays minimal for orgs that don't use this feature.
	MandatedSkills []json.RawMessage `json:"mandated_skills,omitempty"`

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
	// dictate what the harness executes. WP01 (this field) only adds the
	// wire contract; the apply pipeline that actually registers these as
	// read-only recipes lands in WP02.
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
	// contract; the apply pipeline lands in WP04.
	//
	// omitempty: same nil/empty/absent equivalence as ProvisionedMCP —
	// there is no meaningful distinction for a push-down-only section.
	ProviderSetups []ProviderSetup `json:"provider_setups,omitempty"`

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
// (no restriction) from [] (block-all). Must stay in sync with Bundle above.
type bundleSigningPayload struct {
	BundleID           int64             `json:"bundle_id"`
	IssuedAt           time.Time         `json:"issued_at"`
	CedarDelta         json.RawMessage   `json:"cedar_delta,omitempty"`
	MCPAllowlist       []string          `json:"mcp_allowlist"`
	ModelPrefs         *BundleModelPrefs `json:"model_prefs,omitempty"`
	KameasMLWeightURLs []string          `json:"kameas_ml_weight_urls,omitempty"`
	MandatedSkills     []json.RawMessage `json:"mandated_skills,omitempty"`
	ProvisionedMCP     []ProvisionedMCP  `json:"provisioned_mcp,omitempty"`
	ProviderSetups     []ProviderSetup   `json:"provider_setups,omitempty"`
}

// signingPayload produces the canonical JSON bytes that were signed (all
// fields except "signature"). The SHA-256 of these bytes is what ed25519
// signs.
func (b *Bundle) signingPayload() ([]byte, error) {
	p := bundleSigningPayload{
		BundleID:           b.BundleID,
		IssuedAt:           b.IssuedAt,
		CedarDelta:         b.CedarDelta,
		MCPAllowlist:       b.MCPAllowlist,
		ModelPrefs:         b.ModelPrefs,
		KameasMLWeightURLs: b.KameasMLWeightURLs,
		MandatedSkills:     b.MandatedSkills,
		ProvisionedMCP:     b.ProvisionedMCP,
		ProviderSetups:     b.ProviderSetups,
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
//   - ErrInvalidSignature       — signature mismatch; hard-reject
//   - ErrBundleIDNonMonotonic   — bundle_id is not strictly > lastAppliedID
//
// For accept-set verification (key rotation), use VerifyWithKeySet.
func Verify(b *Bundle, key ed25519.PublicKey, lastAppliedID int64) error {
	if len(key) == 0 {
		return fmt.Errorf("%w: cannot verify without a signing key", ErrSigningKeyNotConfigured)
	}
	return VerifyWithKeySet(b, []ed25519.PublicKey{key}, lastAppliedID)
}

// VerifyWithKeySet verifies the bundle signature against any key in the
// accept-set and enforces the monotonic bundle_id guard. An empty accept-set
// is treated as ErrSigningKeyNotConfigured (fail-closed).
//
// FR-003: during key rotation, the accept-set contains both the outgoing and
// the incoming key. Any bundle signed by either key is accepted. Once all
// binaries with the old key are retired, the old key is removed from the set.
func VerifyWithKeySet(b *Bundle, keys []ed25519.PublicKey, lastAppliedID int64) error {
	if len(keys) == 0 {
		return fmt.Errorf("%w: cannot verify without a signing key", ErrSigningKeyNotConfigured)
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
	// Try each key in the accept-set; accept if any key matches.
	verified := false
	for _, key := range keys {
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
