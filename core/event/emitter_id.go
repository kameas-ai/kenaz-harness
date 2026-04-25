package event

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// allowedEmitterPrefixes is the namespace allowlist documented in
// plan §5.6 (FR-017). The trailing "/" is part of the prefix, mirroring
// the spec's literal listing.
var allowedEmitterPrefixes = []string{
	"llm/",
	"mcp/client",
	"mcp/server",
	"a2a/",
	"scheduler/",
	"bundle/",
	"trust/",
	"context/",
	"session/",
	"event-log/",
	"storage/",
}

var (
	emitterMu       sync.RWMutex
	emitterPrefixes = func() map[string]struct{} {
		m := make(map[string]struct{}, len(allowedEmitterPrefixes))
		for _, p := range allowedEmitterPrefixes {
			m[p] = struct{}{}
		}
		return m
	}()
)

// RegisterEmitterPrefix adds prefix to the allowlist. Process-local;
// idempotent. Tests may use this to exercise SC-006 (a new emitter
// requires no change to other emitters).
func RegisterEmitterPrefix(prefix string) error {
	if prefix == "" {
		return fmt.Errorf("%w: empty prefix", ErrUnknownEmitter)
	}
	emitterMu.Lock()
	defer emitterMu.Unlock()
	emitterPrefixes[prefix] = struct{}{}
	return nil
}

// RegisteredEmitterPrefixes returns the current allowlist sorted.
func RegisteredEmitterPrefixes() []string {
	emitterMu.RLock()
	defer emitterMu.RUnlock()
	out := make([]string, 0, len(emitterPrefixes))
	for p := range emitterPrefixes {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ValidateEmitterID checks that eid carries an allowlisted prefix and
// is well-formed. Allowed shapes:
//
//   - "llm/" or "llm/<vendor>" — slash-terminated namespaces accept a
//     trailing component.
//   - "mcp/client" / "mcp/server" — exact matches; trailing
//     identifiers permitted via "mcp/client/<id>" if registered.
//
// Rejection cases:
//
//   - Empty.
//   - No allowlisted prefix.
//   - Internal whitespace or control characters.
func ValidateEmitterID(eid EmitterID) error {
	s := string(eid)
	if s == "" {
		return fmt.Errorf("%w: empty", ErrUnknownEmitter)
	}
	for _, r := range s {
		if r < 0x20 || r == ' ' {
			return fmt.Errorf("%w: contains whitespace or control char", ErrUnknownEmitter)
		}
	}
	emitterMu.RLock()
	defer emitterMu.RUnlock()
	for prefix := range emitterPrefixes {
		if s == prefix {
			return nil
		}
		// "llm/" accepts "llm/anthropic" but rejects "llmsomething".
		if strings.HasSuffix(prefix, "/") && strings.HasPrefix(s, prefix) {
			return nil
		}
		// "mcp/client" accepts "mcp/client" exactly and "mcp/client/<id>".
		if !strings.HasSuffix(prefix, "/") && (s == prefix || strings.HasPrefix(s, prefix+"/")) {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrUnknownEmitter, s)
}
