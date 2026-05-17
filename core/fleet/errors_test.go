package fleet

import (
	"errors"
	"testing"
)

func TestErrorSentinels(t *testing.T) {
	// All sentinel errors must be non-nil and distinct.
	sentinels := []error{
		ErrFleetDisabled,
		ErrNotSignedIn,
		ErrTokenExpired,
		ErrCapabilityNotInTier,
		ErrProfileNotConfigured,
	}
	for i, a := range sentinels {
		if a == nil {
			t.Errorf("sentinel[%d] is nil", i)
		}
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinel[%d] (%v) wraps sentinel[%d] (%v)", i, a, j, b)
			}
		}
	}
}
