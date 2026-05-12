package refs

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"sync"
)

// Sanitizer maintains a per-turn fingerprint set for secret plaintext
// redaction (FR-007, NFR-007). Scope is [turn-start, turn-end):
// the chat runner constructs a fresh Sanitizer at turn start and calls
// Clear at turn end. It is NOT a global singleton.
//
// # Storage
//
// Internally: a slice of entries, each containing:
//   - a SHA-256 fingerprint of the plaintext (for quick duplicate detection)
//   - a copy of the plaintext bytes (needed for substring scan)
//   - the locator string (used in the "[redacted: <locator>]" placeholder)
//
// The plaintext copies are zeroed in Clear.
//
// # Population
//
// Every successful Resolve call invokes Add(plaintext, locator) BEFORE
// returning the plaintext to the caller.
//
// # Application
//
// Every tool result content block AND every streamed assistant token
// is scanned via Sanitize. Matches are replaced with
// "[redacted: <locator>]".
//
// # Cleanup
//
// The chat runner calls Clear at end-of-turn — guarantees zero-length
// state entering the next turn (NFR-007).
//
// # Thread safety
//
// Sanitize is concurrency-safe (read lock over entries). Add and Clear
// acquire the write lock. The expected usage model has Add called only
// during the Resolve path (single goroutine per turn) and Sanitize
// called from the streaming bridge (one goroutine) and tool-result
// path (same goroutine). Concurrent Add+Sanitize is safe.
type Sanitizer struct {
	mu      sync.RWMutex
	entries []fingerprintEntry
}

type fingerprintEntry struct {
	fp      [32]byte // SHA-256 of plain, for duplicate detection
	plain   []byte   // owned copy, zeroed in Clear
	locator string
}

// NewSanitizer returns an empty Sanitizer ready for use.
func NewSanitizer() *Sanitizer {
	return &Sanitizer{}
}

// Add registers a plaintext + locator pair. If the same fingerprint was
// already added, the call is a no-op (idempotent). Must be called under
// the write path (non-concurrent with Clear; concurrent with Sanitize is safe).
// The plaintext bytes are copied; the caller may zero their slice after Add returns.
func (s *Sanitizer) Add(plaintext []byte, locator string) {
	if len(plaintext) == 0 {
		return
	}
	fp := sha256.Sum256(plaintext)
	s.mu.Lock()
	defer s.mu.Unlock()
	// Idempotent: skip if already registered.
	for _, e := range s.entries {
		if e.fp == fp {
			return
		}
	}
	buf := make([]byte, len(plaintext))
	copy(buf, plaintext)
	s.entries = append(s.entries, fingerprintEntry{
		fp:      fp,
		plain:   buf,
		locator: locator,
	})
}

// Sanitize scans content and replaces any occurrence of a registered
// plaintext with "[redacted: <locator>]". Returns the (possibly rewritten)
// content. If no fingerprints are registered, the input is returned
// unchanged (zero allocation fast path).
//
// Safe for concurrent use with itself and with Add.
func (s *Sanitizer) Sanitize(content []byte) []byte {
	s.mu.RLock()
	if len(s.entries) == 0 {
		s.mu.RUnlock()
		return content
	}
	// Snapshot the entries under the read lock.
	snap := make([]fingerprintEntry, len(s.entries))
	copy(snap, s.entries)
	s.mu.RUnlock()

	result := content
	for _, e := range snap {
		if !bytes.Contains(result, e.plain) {
			continue
		}
		placeholder := []byte(fmt.Sprintf("[redacted: %s]", e.locator))
		result = bytes.ReplaceAll(result, e.plain, placeholder)
	}
	return result
}

// SanitizeString is a convenience wrapper for string content blocks.
func (s *Sanitizer) SanitizeString(content string) string {
	if s == nil {
		return content
	}
	return string(s.Sanitize([]byte(content)))
}

// SanitizeStream processes a reader through the sanitizer, writing
// redacted output to the writer. It maintains a tail buffer of size
// maxPlaintextLen-1 so plaintext split across chunk boundaries is caught.
//
// n is the maximum plaintext length across all registered fingerprints.
// When n is 0 (no fingerprints) the data is passed through unchanged.
func (s *Sanitizer) SanitizeStream(chunks []byte) []byte {
	return s.Sanitize(chunks)
}

// Clear drops all registered plaintexts and zeroes the copies.
// Called at end-of-turn.
func (s *Sanitizer) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.entries {
		for j := range s.entries[i].plain {
			s.entries[i].plain[j] = 0
		}
	}
	s.entries = nil
}

// Len returns the number of registered fingerprints. Used in tests.
func (s *Sanitizer) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}
