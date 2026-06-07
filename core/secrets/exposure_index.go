// Package secrets provides the model-side exposure index for secret
// references. The ExposureIndex tracks which keychain entries are
// currently exposed to the model for reference resolution.
//
// This file implements ExposureIndex (WP06 of model-secret-references-01KW7M5A).
// The index is intentionally in-memory for now; a SQL-backed implementation
// will follow when the migration is slotted.
package secrets

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
)

// ErrLocatorNotExposed is returned by ExposureIndex.Use when the locator
// is not in the exposed set.
var ErrLocatorNotExposed = errors.New("secrets: locator not exposed to model")

// Scope describes the visibility scope of an exposed secret entry.
type Scope string

const (
	// ScopeSession means the entry is visible for the lifetime of the
	// active session (a one-shot grant from /secret add).
	ScopeSession Scope = "session"
	// ScopeProject means the entry is visible to all sessions under the
	// named project.
	ScopeProject Scope = "project"
	// ScopeAgent means the entry is visible to a specific agent.
	ScopeAgent Scope = "agent"
	// ScopeWorkflow means the entry was granted by a workflow YAML
	// secrets: block and is visible for the run only.
	ScopeWorkflow Scope = "workflow"
)

// KindHint is a human-readable hint for how to use the secret.
type KindHint string

const (
	KindHintBearer     KindHint = "bearer"
	KindHintBasic      KindHint = "basic"
	KindHintHeader     KindHint = "header"
	KindHintRaw        KindHint = "raw"
	KindHintAWSCred    KindHint = "aws_credential"
)

// ExposedEntry describes one entry in the exposure index.
type ExposedEntry struct {
	// Locator is the @secret: locator (e.g. "user:example-api-token").
	Locator string
	// Description is the human-readable label the user assigned.
	Description string
	// Scope is the visibility scope.
	Scope Scope
	// ScopeValue is the project/agent/workflow id when Scope != ScopeSession.
	ScopeValue string
	// KindHint is the kind hint for the model ("bearer", "basic", etc.).
	KindHint KindHint
	// CreatedAt is when the entry was added.
	CreatedAt time.Time
	// UpdatedAt is when the entry was last modified.
	UpdatedAt time.Time

	// secretPlaintext is the actual secret value, kept in-memory only.
	// It is zeroed on Remove. MUST NOT appear in any exported field or
	// be included in any JSON serialization.
	secretPlaintext []byte
}

// ExposureIndex tracks which keychain locators are currently exposed to
// the model. It implements refs.SecretLookup so it can be passed directly
// to refs.Resolver. Thread-safe.
type ExposureIndex struct {
	mu      sync.RWMutex
	entries map[string]*ExposedEntry // locator → entry
}

// NewExposureIndex returns an empty ExposureIndex.
func NewExposureIndex() *ExposureIndex {
	return &ExposureIndex{
		entries: make(map[string]*ExposedEntry),
	}
}

// Add exposes a locator to the model. The plaintext is the resolved
// secret value that the resolver will inject. The caller is responsible
// for zeroing the plaintext slice after Add returns.
//
// If the locator is already exposed, the entry is updated (description,
// kind hint, scope). The plaintext is updated if non-nil.
func (idx *ExposureIndex) Add(entry ExposedEntry, plaintext []byte) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	existing, ok := idx.entries[entry.Locator]
	if ok {
		// Update metadata.
		existing.Description = entry.Description
		existing.KindHint = entry.KindHint
		existing.Scope = entry.Scope
		existing.ScopeValue = entry.ScopeValue
		existing.UpdatedAt = time.Now().UTC()
		if len(plaintext) > 0 {
			// Zero the old plaintext before replacing.
			for i := range existing.secretPlaintext {
				existing.secretPlaintext[i] = 0
			}
			buf := make([]byte, len(plaintext))
			copy(buf, plaintext)
			existing.secretPlaintext = buf
		}
		return
	}
	e := entry
	e.CreatedAt = time.Now().UTC()
	e.UpdatedAt = e.CreatedAt
	if len(plaintext) > 0 {
		buf := make([]byte, len(plaintext))
		copy(buf, plaintext)
		e.secretPlaintext = buf
	}
	idx.entries[entry.Locator] = &e
}

// Remove removes a locator from the exposure index and zeroes its
// plaintext. Sessions that already resolved the locator continue to
// work until end-of-session.
func (idx *ExposureIndex) Remove(locator string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	e, ok := idx.entries[locator]
	if !ok {
		return
	}
	for i := range e.secretPlaintext {
		e.secretPlaintext[i] = 0
	}
	delete(idx.entries, locator)
}

// List returns a snapshot of all exposed entries matching the given scope.
// An empty scope returns all entries. The returned entries do NOT contain
// plaintext (secretPlaintext field is not exported).
func (idx *ExposureIndex) List(ctx context.Context, scope Scope) []ExposedEntry {
	_ = ctx
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]ExposedEntry, 0, len(idx.entries))
	for _, e := range idx.entries {
		if scope != "" && e.Scope != scope {
			continue
		}
		// Return a copy without the plaintext.
		out = append(out, ExposedEntry{
			Locator:     e.Locator,
			Description: e.Description,
			Scope:       e.Scope,
			ScopeValue:  e.ScopeValue,
			KindHint:    e.KindHint,
			CreatedAt:   e.CreatedAt,
			UpdatedAt:   e.UpdatedAt,
		})
	}
	return out
}

// Contains reports whether locator is in the exposure index.
func (idx *ExposureIndex) Contains(locator string) bool {
	idx.mu.RLock()
	_, ok := idx.entries[locator]
	idx.mu.RUnlock()
	return ok
}

// Use implements refs.SecretLookup. It calls op with the plaintext bytes
// for the given locator. Returns ErrLocatorNotExposed when the locator
// is not in the index, or refs.ErrUnknownLocator when the locator is
// absent (mapped to the same error for the resolver).
func (idx *ExposureIndex) Use(_ context.Context, locator string, op func([]byte) error) error {
	idx.mu.RLock()
	e, ok := idx.entries[locator]
	idx.mu.RUnlock()
	if !ok {
		return refs.ErrUnknownLocator
	}
	if len(e.secretPlaintext) == 0 {
		return refs.ErrUnknownLocator
	}
	// Copy the plaintext into a temporary buffer so we can zero it.
	buf := make([]byte, len(e.secretPlaintext))
	idx.mu.RLock()
	copy(buf, e.secretPlaintext)
	idx.mu.RUnlock()
	defer func() {
		for i := range buf {
			buf[i] = 0
		}
	}()
	return op(buf)
}

// Compile-time: ExposureIndex must satisfy refs.SecretLookup.
var _ refs.SecretLookup = (*ExposureIndex)(nil)
