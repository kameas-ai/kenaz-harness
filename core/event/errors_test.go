package event

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorClassificationHelpers(t *testing.T) {
	cases := []struct {
		name string
		err  error
		fn   func(error) bool
		want bool
	}{
		{"chain broken positive", ErrChainBroken, IsChainBroken, true},
		{"chain broken wrapped", fmt.Errorf("wrap: %w", ErrChainBroken), IsChainBroken, true},
		{"chain broken negative", ErrInvalidKind, IsChainBroken, false},
		{"redaction bypassed positive", ErrRedactionBypassed, IsRedactionBypassed, true},
		{"unknown emitter positive", ErrUnknownEmitter, IsUnknownEmitter, true},
		{"invalid kind positive", ErrInvalidKind, IsInvalidKind, true},
		{"truncated positive", ErrTruncated, IsTruncated, true},
		{"append-only positive", ErrAppendOnly, IsAppendOnlyViolation, true},
		{"policy violation positive", ErrPolicyViolation, IsPolicyViolation, true},
		{"session not found positive", ErrSessionNotFound, IsSessionNotFound, true},
		{"already archived positive", ErrAlreadyArchived, IsAlreadyArchived, true},
		{"nil err", nil, IsChainBroken, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.fn(tc.err); got != tc.want {
				t.Fatalf("classifier returned %v, want %v", got, tc.want)
			}
		})
	}
}

// Defense-in-depth: if anyone refactors a sentinel into a typed error
// later, the wrapping contract should still hold.
func TestSentinelsAreDistinct(t *testing.T) {
	all := []error{
		ErrChainBroken, ErrRedactionBypassed, ErrUnknownEmitter,
		ErrInvalidKind, ErrTruncated, ErrAppendOnly, ErrPolicyViolation,
		ErrSessionNotFound, ErrAlreadyArchived, ErrInvalidQuery,
		ErrInvalidArgument,
	}
	for i := range all {
		for j := range all {
			if i == j {
				continue
			}
			if errors.Is(all[i], all[j]) {
				t.Fatalf("sentinel %v should not Is %v", all[i], all[j])
			}
		}
	}
}
