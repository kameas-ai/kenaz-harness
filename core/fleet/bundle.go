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

	// Signature is the base64-encoded ed25519 signature over the SHA-256 of
	// the canonical JSON of this bundle with the "signature" field absent.
	// This field is excluded from the signing input.
	Signature string `json:"signature"`
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
	}
	return json.Marshal(p)
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
func Verify(b *Bundle, key ed25519.PublicKey, lastAppliedID int64) error {
	if len(key) == 0 {
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
	if !ed25519.Verify(key, hash[:], sig) {
		return ErrInvalidSignature
	}

	// Monotonic bundle_id guard.
	if b.BundleID <= lastAppliedID {
		return fmt.Errorf("%w: received %d, last applied %d", ErrBundleIDNonMonotonic, b.BundleID, lastAppliedID)
	}

	return nil
}
