package events

// BundleRef identifies the bundle a payload refers to. Values only —
// no credential material.
type BundleRef struct {
	Name    string
	Version string
}

// BundleResolved is emitted after a complete Resolve.
type BundleResolved struct {
	SnapshotID  string
	ContentHash string
	BundleCount int
	DurationMS  int64
}

// ArtifactFetched is emitted after a successful channel fetch.
type ArtifactFetched struct {
	Bundle       BundleRef
	ArtifactName string
	ChannelKind  string
	Bytes        int64
	DurationMS   int64
}

// ArtifactVerified is emitted after a SHA-256 match.
type ArtifactVerified struct {
	Bundle       BundleRef
	ArtifactName string
	ContentHash  string
}

// ArtifactActivated is emitted after a kind handler activates an
// artifact.
type ArtifactActivated struct {
	Bundle       BundleRef
	ArtifactName string
	Kind         string
}

// ArtifactDeactivated is emitted after a kind handler deactivates
// (rollback, removal).
type ArtifactDeactivated struct {
	Bundle       BundleRef
	ArtifactName string
	Kind         string
}

// ArtifactRejected is emitted on hash mismatch, signature failure, or
// schema failure.
type ArtifactRejected struct {
	Bundle       BundleRef
	ArtifactName string
	Reason       string
	ErrorCode    string
}

// BundleSignatureVerified is emitted on a successful signature
// verification.
type BundleSignatureVerified struct {
	Bundle        BundleRef
	SignatureKind string
	KeyID         string
	AnchorID      string
}

// BundleSignatureFailed is emitted on a failed or missing-but-required
// signature.
type BundleSignatureFailed struct {
	Bundle BundleRef
	Reason string
}

// LockfileUpdated is emitted after a new lockfile is written.
type LockfileUpdated struct {
	PriorHash   string
	NewHash     string
	DiffSummary string
}

// CacheHit is emitted on a CAS lookup hit.
type CacheHit struct {
	ContentHash string
	Bytes       int64
}

// CacheMiss is emitted on a CAS lookup miss.
type CacheMiss struct {
	ContentHash string
}

// ChannelUnreachable is emitted on pre-flight or fetch reachability
// failure.
type ChannelUnreachable struct {
	ChannelKind string
	Endpoint    string // sanitized — never carries credentials
	Reason      string
}
