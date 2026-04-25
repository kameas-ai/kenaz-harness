// Package secrets is the cross-mission stub for secrets-keychain.
//
// The full secrets-keychain mission owns the OS-level keychain wiring,
// the credential-reference machinery, and the operator UX (`kaneaz
// keychain login`, etc.). Until that mission lands, this package
// supplies just the interfaces the bundle layer needs to compile and
// test against — nothing more.
//
// IMPORTANT: do NOT add concrete keychain logic here. Replace the file
// from the secrets-keychain mission's worktree at integration time.
package secrets

import "context"

// Resolver is the narrow interface the bundle layer consumes. The
// secrets-keychain mission is expected to provide a concrete
// implementation that backs onto the OS keychain or a local SQLite
// table per its FR-001.
type Resolver interface {
	// Lookup confirms a credential reference exists without resolving
	// its value. Used at pre-flight (FR-012) so misconfiguration
	// surfaces before any fetch.
	Lookup(ref string) (bool, error)

	// Resolve returns the secret value for ref. Callers MUST NOT log
	// the return value; pass it directly into the channel adapter and
	// drop the reference as soon as the request is built.
	Resolve(ctx context.Context, ref string) (string, error)
}

// NoopResolver is a stub Resolver that reports every reference as
// missing. Useful for tests that exercise the local_path channel,
// which never resolves credentials.
type NoopResolver struct{}

// Lookup always returns (false, nil).
func (NoopResolver) Lookup(_ string) (bool, error) { return false, nil }

// Resolve always returns an empty string and nil error.
func (NoopResolver) Resolve(_ context.Context, _ string) (string, error) { return "", nil }
