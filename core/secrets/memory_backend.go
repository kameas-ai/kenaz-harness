package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/registry"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/secret"
	"github.com/zalando/go-keyring"
)

// keyringService is the namespace under which the harness writes
// keychain credentials. zalando/go-keyring routes to the per-OS
// secure store: macOS Keychain, Windows Credential Manager, libsecret
// on Linux. A single namespace makes auditing + manual cleanup easy.
const keyringService = "kaneaz-harness"

// MemoryBackend is a single-process Backend that resolves env-var refs
// from os.Getenv and keychain/file refs from an in-memory map. It is
// the test-friendly default used by consumers (llm/credref,
// acp/peers) until the per-platform backends from secrets-keychain
// land. The behaviour mirrors the real backends closely enough to
// exercise the resolver pipeline:
//
//   - RefEnv: returns os.Getenv(locator); empty value is "missing".
//   - RefKeychain / RefFile: returns map[locator] when present;
//     a missing key is a typed error.
//
// Construction is intentionally trivial; use SetEntry / SetEntries to
// stage values for tests.
type MemoryBackend struct {
	mu      sync.RWMutex
	entries map[string][]byte // key = "<kind>|<locator>"
}

// NewMemoryBackend returns an empty in-memory backend. RefEnv lookups
// always read os.Getenv; other kinds need explicit SetEntry calls.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{entries: map[string][]byte{}}
}

// SetEntry stages an in-memory value for kind+locator. Used in tests.
func (b *MemoryBackend) SetEntry(kind ref.RefKind, locator string, value []byte) {
	key := kind.String() + "|" + locator
	dup := append([]byte(nil), value...)
	b.mu.Lock()
	b.entries[key] = dup
	b.mu.Unlock()
}

// SetEntries stages multiple values; map keys are "<kind>|<locator>".
func (b *MemoryBackend) SetEntries(values map[string][]byte) {
	b.mu.Lock()
	for k, v := range values {
		b.entries[k] = append([]byte(nil), v...)
	}
	b.mu.Unlock()
}

// ClearEntry removes the staged in-memory value for kind+locator.
// Used by the rpc tools view's ForgetRecipeKey so a follow-up
// Resolve falls through to the OS keychain (or fails when both are
// empty). Safe under concurrent access; no-op when the entry is
// absent.
func (b *MemoryBackend) ClearEntry(kind ref.RefKind, locator string) {
	key := kind.String() + "|" + locator
	b.mu.Lock()
	delete(b.entries, key)
	b.mu.Unlock()
}

// Kind returns the canonical backend name.
func (b *MemoryBackend) Kind() registry.BackendKind { return "memory" }

// SupportedRefKinds reports the kinds this backend can serve.
func (b *MemoryBackend) SupportedRefKinds() []ref.RefKind {
	return []ref.RefKind{ref.RefEnv, ref.RefKeychain, ref.RefFile, ref.RefAWSProfile}
}

// errNotFound is returned when no in-memory entry matches the locator.
var errNotFound = errors.New("memory: not found")

// Resolve fetches the credential bytes for r and returns a Secret.
func (b *MemoryBackend) Resolve(ctx context.Context, r ref.CredentialReference) (secret.Secret, error) {
	switch r.Kind {
	case ref.RefEnv:
		v := os.Getenv(r.Locator)
		if v == "" {
			return nil, fmt.Errorf("%w: env var %q unset or empty", errNotFound, r.Locator)
		}
		buf := []byte(v)
		return secret.NewStdlibSecret(buf, r.ID(), r.ConsumerID), nil
	case ref.RefKeychain:
		// Prefer the OS keychain so credentials survive process
		// restarts. Fall back to the in-memory map for tests +
		// for keys that have only been staged this session.
		if v, err := keyring.Get(keyringService, r.Locator); err == nil {
			return secret.NewStdlibSecret([]byte(v), r.ID(), r.ConsumerID), nil
		}
		key := r.Kind.String() + "|" + r.Locator
		b.mu.RLock()
		v, ok := b.entries[key]
		b.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("%w: %s", errNotFound, key)
		}
		dup := append([]byte(nil), v...)
		return secret.NewStdlibSecret(dup, r.ID(), r.ConsumerID), nil
	case ref.RefFile:
		key := r.Kind.String() + "|" + r.Locator
		b.mu.RLock()
		v, ok := b.entries[key]
		b.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("%w: %s", errNotFound, key)
		}
		dup := append([]byte(nil), v...)
		return secret.NewStdlibSecret(dup, r.ID(), r.ConsumerID), nil
	case ref.RefAWSProfile:
		// The "credential" for an aws_profile reference is the profile
		// name itself — the AWS SDK reads ~/.aws/credentials at use
		// time. We surface the locator (profile name) verbatim so the
		// adapter has something to pass to config.LoadDefaultConfig.
		profile := r.Locator
		if profile == "" {
			profile = "default"
		}
		return secret.NewStdlibSecret([]byte(profile), r.ID(), r.ConsumerID), nil
	default:
		return nil, fmt.Errorf("memory: unsupported ref kind %s", r.Kind)
	}
}

// Health reports the backend's status; the in-memory backend is always
// healthy.
func (b *MemoryBackend) Health(context.Context) registry.BackendHealth {
	return registry.BackendHealth{
		Status:      registry.HealthOK,
		Message:     "memory backend",
		LastChecked: time.Now(),
	}
}

// Compile-time assertion that MemoryBackend satisfies Backend.
var _ Backend = (*MemoryBackend)(nil)
