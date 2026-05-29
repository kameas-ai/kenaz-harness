package fleet

import (
	"context"
	"fmt"

	corefleet "github.com/sigil-tech/kaneaz-harness/core/fleet"
)

// Impl implements FleetAPI backed by core/fleet.TelemetryConsent.
type Impl struct {
	Consent *corefleet.TelemetryConsent
}

var _ FleetAPI = (*Impl)(nil)

// GetTelemetryConsent returns the effective consent level.
func (f *Impl) GetTelemetryConsent(_ context.Context) (string, error) {
	return string(f.Consent.EffectiveLevel()), nil
}

// SetTelemetryConsent validates the level string and delegates to
// TelemetryConsent.SetLevel, which enforces tier gating.
func (f *Impl) SetTelemetryConsent(_ context.Context, level string) error {
	cl := corefleet.ConsentLevel(level)
	switch cl {
	case corefleet.ConsentNone, corefleet.ConsentAggregate, corefleet.ConsentFull:
		// valid
	default:
		return fmt.Errorf("unknown consent level %q; must be one of none, aggregate, full", level)
	}
	return f.Consent.SetLevel(cl)
}
