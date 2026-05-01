package kind

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrInvalid is returned when a kind name fails well-formedness rules.
// (Mirrored as core/event.ErrInvalidKind for the public surface; this
// package keeps a local sentinel to avoid an import cycle.)
var ErrInvalid = errors.New("kind: invalid")

// Kind is the registry-internal representation. core/event.Kind aliases
// it via string conversion at the call site.
type Kind string

// Built-in self-event kinds. Consumers register additional kinds under
// their own namespace prefix.
const (
	KindRetentionStarted    Kind = "event-log.retention.started"
	KindRetentionCompleted  Kind = "event-log.retention.completed"
	KindSaltRotated         Kind = "event-log.redaction.salt-rotated"
	KindSessionBranched     Kind = "event-log.session.branched"
	KindRawReplayOpened     Kind = "event-log.replay.raw-opened"
	KindRedactionSupersede  Kind = "event-log.redaction.supersede"
	KindChainRebased        Kind = "event-log.chain.rebased"
	KindCancellation        Kind = "event-log.cancellation"
	KindError               Kind = "event-log.error"
	KindTimeout             Kind = "event-log.timeout"
	KindDatabaseOpened      Kind = "event-log.database.opened"

	// KindShortcutOverridden is emitted on every successful keyboard
	// shortcut binding write (set or reset). Payload JSON:
	//   {"shortcut_id":"chat.send","default_binding":"Cmd+Enter","new_binding":"Cmd+Shift+Enter"}
	// Reset emits new_binding: "".
	// (keyboard-shortcuts-settings-01KQ8TDR plan §2.9)
	KindShortcutOverridden Kind = "settings.shortcut.overridden"
)

var builtIn = []Kind{
	KindRetentionStarted, KindRetentionCompleted, KindSaltRotated,
	KindSessionBranched, KindRawReplayOpened, KindRedactionSupersede,
	KindChainRebased, KindCancellation, KindError, KindTimeout,
	KindDatabaseOpened,
	KindShortcutOverridden,
}

var (
	mu       sync.RWMutex
	registry = func() map[Kind]struct{} {
		m := make(map[Kind]struct{}, len(builtIn))
		for _, k := range builtIn {
			m[k] = struct{}{}
		}
		return m
	}()
)

// Register adds k to the process-local registry. Idempotent. Returns
// ErrInvalid if k is malformed.
func Register(k Kind) error {
	if err := Validate(k); err != nil {
		return err
	}
	mu.Lock()
	registry[k] = struct{}{}
	mu.Unlock()
	return nil
}

// IsRegistered reports whether k is in the process-local registry.
// (Unknown but well-formed kinds are still accepted by Validate.)
func IsRegistered(k Kind) bool {
	mu.RLock()
	_, ok := registry[k]
	mu.RUnlock()
	return ok
}

// All returns every registered kind. Order is undefined.
func All() []Kind {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Kind, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// Validate checks the grammar of k:
//
//	<namespace>.<dotted.path>
//
// where namespace is one or more lowercase ASCII letters / digits /
// hyphens (matching the emitter-id namespace prefixes minus the slash
// boundary), and the dotted path is one or more dot-separated segments
// of the same character class. At least one dot must separate the
// namespace from a path segment.
//
// Forward-compat: well-formed kinds outside the registry are NOT
// rejected here; they are accepted as opaque (NFR-006). Callers
// distinguish "registered" via IsRegistered.
func Validate(k Kind) error {
	s := string(k)
	if s == "" {
		return fmt.Errorf("%w: empty", ErrInvalid)
	}
	if strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return fmt.Errorf("%w: leading/trailing dot in %q", ErrInvalid, s)
	}
	if strings.Contains(s, "..") {
		return fmt.Errorf("%w: empty segment in %q", ErrInvalid, s)
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return fmt.Errorf("%w: missing dotted path in %q", ErrInvalid, s)
	}
	for _, p := range parts {
		if p == "" {
			return fmt.Errorf("%w: empty segment in %q", ErrInvalid, s)
		}
		for _, r := range p {
			ok := (r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') ||
				r == '-' || r == '_'
			if !ok {
				return fmt.Errorf("%w: invalid char %q in segment %q of %q", ErrInvalid, r, p, s)
			}
		}
	}
	return nil
}
