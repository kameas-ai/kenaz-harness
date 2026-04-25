// Package channels defines the distribution-channel abstraction and the
// registry that the resolver dispatches through.
//
// DIRECTIVE_001: every channel implementation lives in its own
// subpackage (channels/git/, channels/oci/, channels/http/,
// channels/localpath/). The resolver has no compile-time dependency on
// any concrete channel — it speaks only to the Channel interface and
// the Registry.
//
// Adding a new channel kind is a matter of:
//   1. Creating a new subpackage (e.g. channels/myreg/)
//   2. Implementing Channel + a Factory func.
//   3. Calling channels.NewRegistry().Register("myreg", factory).
//
// No edit to any file outside the new subpackage is required.
package channels

import (
	"context"
	"io"

	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// Channel is the contract for a distribution-channel adapter.
type Channel interface {
	// Kind returns the registered kind id ("git", "oci", "http_mirror",
	// "local_path"). Stable for the lifetime of the channel.
	Kind() string

	// Reachable performs a pre-flight reachability probe. Called at
	// startup (FR-012) so misconfiguration surfaces before first use.
	// Implementations should return ErrChannelUnreachable on failure.
	Reachable(ctx context.Context) error

	// Fetch streams artifact bytes for ref into sink. The channel does
	// NOT verify content hashes — that is the resolver's job (FR-006);
	// channel-layer integrity checks are defense in depth where they
	// happen to be cheap (e.g., OCI digest match) but never authoritative.
	Fetch(ctx context.Context, ref ArtifactCoord, sink io.Writer) (FetchResult, error)

	// LookupSignatures returns any signatures the channel knows about
	// for ref. May return an empty list (not an error) when no
	// signatures are present.
	LookupSignatures(ctx context.Context, ref ArtifactCoord) ([]SignatureRef, error)
}

// ChannelSpec describes the operator's channel configuration. The same
// shape that lives in kaneaz.yaml `dependencies[].source` and the lockfile
// `[[bundle]].source`.
type ChannelSpec struct {
	Kind string // "git" | "oci" | "http_mirror" | "local_path"
	Ref  string // version-qualified reference (e.g. tag, commit SHA)
	URL  string // remote URL (HTTP, OCI registry, git remote)
	Path string // local filesystem root (local_path only)
	Auth *AuthRef
}

// AuthRef is an indirect credential reference resolved through
// secrets.Resolver at fetch time. The bundle layer never sees the
// resolved value.
type AuthRef struct {
	Keychain string // name of the keychain entry
}

// ArtifactCoord identifies one artifact within a channel.
type ArtifactCoord struct {
	// BundleName / BundleVersion qualify the artifact's parent bundle.
	BundleName    string
	BundleVersion string
	// ArtifactName is the artifact's name within the bundle.
	ArtifactName string
	// Path is the in-bundle relative path to the artifact (validated by
	// the manifest layer to be non-escaping).
	Path string
	// ContentHash is the expected SHA-256 digest. Channels MAY use this
	// for digest-by-name lookups (OCI) but MUST NOT skip resolver-
	// layer verification on this basis.
	ContentHash string
}

// FetchResult records information about a completed fetch.
type FetchResult struct {
	Bytes    int64
	Endpoint string // sanitized endpoint (no credentials)
}

// SignatureRef is the channel-layer view of a signature pointer. It is
// converted into the manifest-layer SignatureRef shape by the resolver
// when handed to the trust verifier.
type SignatureRef struct {
	Kind      string // "sigstore_referrer" | "ed25519_detached"
	Locator   string // OCI digest or filesystem path
	Algorithm string
	KeyID     string
}

// Factory is a constructor for Channel implementations. Each channel
// subpackage exposes a Factory that the registry binds to a kind id.
type Factory func(spec ChannelSpec, creds secrets.Resolver) (Channel, error)
