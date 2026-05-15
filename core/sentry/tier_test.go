package sentry_test

import (
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/sentry"
)

func TestResolveTier(t *testing.T) {
	tests := []struct {
		name          string
		stored        string
		fleetLoggedIn bool
		want          sentry.Tier
	}{
		{"off by default", "", false, sentry.TierOff},
		{"off explicit", "off", false, sentry.TierOff},
		{"anonymous no fleet", "anonymous", false, sentry.TierAnonymous},
		{"anonymous with fleet", "anonymous", true, sentry.TierAnonymous},
		{"identified with fleet", "identified", true, sentry.TierIdentified},
		{"identified without fleet downgrades", "identified", false, sentry.TierAnonymous},
		{"unknown value is off", "broken", false, sentry.TierOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sentry.ResolveTier(tt.stored, tt.fleetLoggedIn)
			if got != tt.want {
				t.Errorf("ResolveTier(%q, %v) = %q, want %q", tt.stored, tt.fleetLoggedIn, got, tt.want)
			}
		})
	}
}
