package trust

import "context"

// Verifier is the integration seam consumed by core/bundle/integrity and
// other packages that need to delegate signature verification without
// depending on TrustEngine internals. The bundle resolver, context-pack
// distribution, and policy artifact loaders all consume this surface.
//
// The shape is intentionally narrow: a request carrying the canonical
// payload bytes plus a SignatureRef, and a response indicating whether
// the signature passed plus a structured reason on failure.
//
// v1 ships with no concrete implementation here — TrustEngine's verify
// pipeline will provide one in a follow-up wire-up; bundle / context
// callers may pass nil and rely on policy SigningOptional to skip.
type Verifier interface {
	Verify(ctx context.Context, req VerifyRequest) (VerifyResult, error)
}

// VerifyRequest is the input to Verifier.Verify.
type VerifyRequest struct {
	Payload []byte
	// Signature carries the reference metadata (kind/locator/algorithm/
	// key id) — never the signature bytes themselves. See SignatureBytes.
	Signature SignatureRef
	// SignatureBytes carries the raw detached-signature bytes the
	// caller resolved from Signature.Locator. core/trust never resolves
	// a locator itself (verifier.go's own doc: "intentionally narrow" —
	// core/trust stays ignorant of what a Locator string means); the
	// caller (e.g. core/bundle/integrity, which knows a Locator is a
	// bundle-relative file path) reads the bytes and hands them here.
	// Additive field: zero-value callers behave exactly as before
	// (an empty envelope signature, which fails shape validation).
	SignatureBytes []byte
	Anchors        []Anchor
}

// VerifyResult reports the outcome of a Verifier.Verify call. OK=true on
// success; otherwise Reason carries a short operator-readable explanation
// (matched to the FR-017 RejectionCode taxonomy by the consumer when
// surfacing audit events).
type VerifyResult struct {
	OK     bool
	Reason string
}

// SignatureRef is the shape consumers pass to Verifier.Verify. Mirrors
// the on-disk SignatureRef carried by manifests, lockfiles, and policy /
// context-pack artifacts; field types are deliberately string-typed so
// callers do not have to import core/trust just to fill the struct.
type SignatureRef struct {
	Kind      string // sigstore_referrer | ed25519_detached | ...
	Locator   string // OCI referrer ref, file path, content hash, etc.
	Algorithm string // canonical algorithm id (e.g. "ed25519")
	KeyID     string // SHA-256 fingerprint of the public key
}
