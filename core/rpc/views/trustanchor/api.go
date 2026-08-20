// Package trustanchor defines the TrustAnchorAPI view-scoped accessor —
// the RPC-facing "writer" for core/trust's persisted AnchorStore
// (bundle-download-and-verify-01PMZ909 UNIT-3, spec §5.3 step 4).
//
// Without this surface, an install refused for "anchor_missing" (UNIT-4)
// has no user remedy: nothing lets an operator register the public key
// that would make verification succeed. The name is distinct from
// core/rpc/views/trust.TrustAPI (secret-reference listing, an unrelated
// concern that happens to have claimed the shorter package name first).
package trustanchor

import "context"

// PublicKey is the wire shape of a core/trust.PublicKey. Bytes travel
// as base64 — this surface is the operator-facing management path, not
// a live signing path, so a flat string is the simplest wire shape.
type PublicKey struct {
	Algorithm   string `json:"algorithm"`
	KeyB64      string `json:"keyB64"`
	Fingerprint string `json:"fingerprint"`
}

// Anchor is the wire shape of a core/trust.Anchor.
type Anchor struct {
	AnchorID    string    `json:"anchorId"`
	Kind        string    `json:"kind"` // "raw_public_key" | "org_identifier" | "pinned_peer"
	PeerID      string    `json:"peerId,omitempty"`
	OrgID       string    `json:"orgId,omitempty"`
	Algorithm   string    `json:"algorithm"`
	PublicKey   PublicKey `json:"publicKey"`
	InstalledAt string    `json:"installedAt"` // RFC3339
	Removed     bool      `json:"removed"`
}

// InstallAnchorRequest is the parameter shape for registering a new
// trust anchor. KeyB64 is the raw public-key bytes (e.g. 32 bytes for
// Ed25519), base64-encoded. Fingerprint is computed server-side
// (trust.ComputeFingerprint) — the caller never supplies it, so it
// cannot be spoofed independently of the key bytes.
type InstallAnchorRequest struct {
	AnchorID  string `json:"anchorId"`
	Kind      string `json:"kind"` // "raw_public_key" | "pinned_peer"; default raw_public_key
	PeerID    string `json:"peerId,omitempty"`
	Algorithm string `json:"algorithm"` // default "ed25519"
	KeyB64    string `json:"keyB64"`
}

// TrustAnchorAPI is the view-scoped accessor for anchor management.
type TrustAnchorAPI interface {
	ListAnchors(ctx context.Context) ([]Anchor, error)
	InstallAnchor(ctx context.Context, req InstallAnchorRequest) (Anchor, error)
}
