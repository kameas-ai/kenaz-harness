package credstore

import (
	"context"
	"runtime"
	"strings"

	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// Redacted is the display-safe view returned by Peek.
// It carries no raw credential bytes.
type Redacted struct {
	// Display is the human-readable redacted form of the credential.
	// Rules per plan §2.6:
	//   bytes > 12  →  4-char prefix + "…" + 4-char suffix  (e.g. "sk-a…d4f9")
	//   bytes ≤ 12  →  8 bullet characters                  ("••••••••")
	Display string
	// Kind is the ref.RefKind string of the resolved credential.
	Kind string
	// Locator is the redaction-safe ID (ref.CredentialReference.ID),
	// NOT the raw locator.
	Locator string
}

// Peek resolves ref to bytes, builds a Redacted view, zeroes the
// buffer, and returns. No audit event is emitted (Peek is
// display-only).
//
// Errors:
//   - ErrEmptyCredential when ref.Locator is empty.
//   - resolver errors propagated directly.
func (s *store) Peek(ctx context.Context, ref secrets.CredentialReference) (Redacted, error) {
	if ref.Locator == "" {
		return Redacted{}, ErrEmptyCredential
	}

	sec, err := s.resolver.Resolve(ctx, ref, "credstore:peek")
	if err != nil {
		return Redacted{}, err
	}
	defer sec.Destroy()

	var display string
	extractErr := sec.Use(func(raw []byte) error {
		// Copy to a local buf we can zero before returning.
		buf := make([]byte, len(raw))
		copy(buf, raw)

		defer func() {
			for i := range buf {
				buf[i] = 0
			}
			runtime.KeepAlive(buf)
		}()

		display = redactBytes(buf)
		return nil
	})
	if extractErr != nil {
		return Redacted{}, extractErr
	}

	return Redacted{
		Display: display,
		Kind:    ref.Kind.String(),
		Locator: ref.ID(),
	}, nil
}

// redactBytes applies the plan §2.6 display rules to raw bytes:
//   - len > 12  →  first 4 chars + "…" + last 4 chars of the UTF-8 string
//   - len ≤ 12  →  8 bullet characters
func redactBytes(b []byte) string {
	if len(b) > 12 {
		s := string(b)
		runes := []rune(s)
		if len(runes) > 12 {
			prefix := string(runes[:4])
			suffix := string(runes[len(runes)-4:])
			return prefix + "…" + suffix
		}
	}
	return strings.Repeat("•", 8)
}
