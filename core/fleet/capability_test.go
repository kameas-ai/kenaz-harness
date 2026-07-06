package fleet

import (
	"errors"
	"testing"
	"time"
)

func TestAllCapabilitiesCount(t *testing.T) {
	all := AllCapabilities()
	if len(all) != 26 {
		t.Errorf("AllCapabilities() = %d entries, want 26", len(all))
	}
}

func TestCapSitesHostingInAllCapabilities(t *testing.T) {
	// WP01: sites-foundation-01NSITE04 — CapSitesHosting must appear exactly once.
	found := false
	for _, c := range AllCapabilities() {
		if c == CapSitesHosting {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("AllCapabilities() does not contain CapSitesHosting (%q)", CapSitesHosting)
	}
	if string(CapSitesHosting) != "sites_hosting" {
		t.Errorf("CapSitesHosting = %q, want \"sites_hosting\"", CapSitesHosting)
	}
}

func TestAllCapabilitiesNoDuplicates(t *testing.T) {
	all := AllCapabilities()
	seen := map[Capability]int{}
	for _, c := range all {
		seen[c]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("duplicate capability %q (%d times)", k, n)
		}
	}
}

func TestCapabilities_Has(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-1 * time.Hour)     // 1h ago — within TTL
	stale := now.Add(-25 * time.Hour)    // 25h ago — beyond TTL

	tests := []struct {
		name      string
		caps      *Capabilities
		key       Capability
		wantHas   bool
	}{
		{
			name:    "nil capabilities returns false",
			caps:    nil,
			key:     CapLauncherUpdates,
			wantHas: false,
		},
		{
			name:    "enabled key within TTL returns true",
			caps:    &Capabilities{Tier: "pro", Enabled: map[Capability]bool{CapLauncherUpdates: true}, FetchedAt: fresh},
			key:     CapLauncherUpdates,
			wantHas: true,
		},
		{
			name:    "disabled key within TTL returns false",
			caps:    &Capabilities{Tier: "pro", Enabled: map[Capability]bool{CapLauncherUpdates: false}, FetchedAt: fresh},
			key:     CapLauncherUpdates,
			wantHas: false,
		},
		{
			name:    "enabled key but stale (>24h) returns false",
			caps:    &Capabilities{Tier: "pro", Enabled: map[Capability]bool{CapLauncherUpdates: true}, FetchedAt: stale},
			key:     CapLauncherUpdates,
			wantHas: false,
		},
		{
			name:    "unknown key returns false without panic",
			caps:    &Capabilities{Tier: "pro", Enabled: map[Capability]bool{}, FetchedAt: fresh},
			key:     Capability("unknown_cap_xyz"),
			wantHas: false,
		},
		{
			name:    "nil Enabled map returns false",
			caps:    &Capabilities{Tier: "pro", Enabled: nil, FetchedAt: fresh},
			key:     CapLauncherUpdates,
			wantHas: false,
		},
		{
			name:    "zero FetchedAt (never fetched) returns false",
			caps:    &Capabilities{Tier: "pro", Enabled: map[Capability]bool{CapLauncherUpdates: true}, FetchedAt: time.Time{}},
			key:     CapLauncherUpdates,
			wantHas: false,
		},
		{
			name:    "exactly 24h boundary is stale",
			caps:    &Capabilities{Tier: "pro", Enabled: map[Capability]bool{CapLauncherUpdates: true}, FetchedAt: now.Add(-24 * time.Hour)},
			key:     CapLauncherUpdates,
			wantHas: false,
		},
		{
			name:    "just under 24h is fresh",
			caps:    &Capabilities{Tier: "pro", Enabled: map[Capability]bool{CapLauncherUpdates: true}, FetchedAt: now.Add(-23*time.Hour - 59*time.Minute)},
			key:     CapLauncherUpdates,
			wantHas: true,
		},
		{
			name:    "all keys in AllCapabilities covered",
			caps:    buildFullEnabled(fresh),
			key:     CapQuarterlyAttestationReports,
			wantHas: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.caps.Has(tc.key)
			if got != tc.wantHas {
				t.Errorf("Has(%q) = %v, want %v", tc.key, got, tc.wantHas)
			}
		})
	}
}

func TestCapabilities_Require(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-1 * time.Hour)

	t.Run("returns nil when capability present", func(t *testing.T) {
		caps := &Capabilities{
			Tier:      "pro",
			Enabled:   map[Capability]bool{CapLauncherUpdates: true},
			FetchedAt: fresh,
		}
		if err := caps.Require(CapLauncherUpdates); err != nil {
			t.Errorf("Require(enabled) = %v, want nil", err)
		}
	})

	t.Run("wraps ErrCapabilityNotInTier on missing", func(t *testing.T) {
		caps := &Capabilities{
			Tier:      "pro",
			Enabled:   map[Capability]bool{CapLauncherUpdates: false},
			FetchedAt: fresh,
		}
		err := caps.Require(CapLauncherUpdates)
		if err == nil {
			t.Fatal("Require(disabled) = nil, want error")
		}
		if !errors.Is(err, ErrCapabilityNotInTier) {
			t.Errorf("Require error = %v, want to wrap ErrCapabilityNotInTier", err)
		}
	})

	t.Run("error message includes capability key", func(t *testing.T) {
		caps := &Capabilities{
			Tier:      "team",
			Enabled:   map[Capability]bool{},
			FetchedAt: fresh,
		}
		err := caps.Require(CapSharedTeamGraph)
		if err == nil {
			t.Fatal("want error for disabled capability")
		}
		msg := err.Error()
		if msg == "" {
			t.Error("error message is empty")
		}
		// The key name should appear in the error.
		const want = "shared_team_graph"
		if len(msg) < len(want) {
			t.Errorf("error message %q does not contain %q", msg, want)
		}
	})

	t.Run("nil caps Require returns ErrCapabilityNotInTier", func(t *testing.T) {
		var caps *Capabilities
		err := caps.Require(CapLauncherUpdates)
		if !errors.Is(err, ErrCapabilityNotInTier) {
			t.Errorf("nil.Require = %v, want ErrCapabilityNotInTier", err)
		}
	})
}

func TestDefaultDenyCapabilities(t *testing.T) {
	d := DefaultDenyCapabilities()
	if d.Source != "default-deny" {
		t.Errorf("Source = %q, want 'default-deny'", d.Source)
	}
	if d.Has(CapLauncherUpdates) {
		t.Error("default-deny Has(CapLauncherUpdates) = true, want false")
	}
	for _, c := range AllCapabilities() {
		if d.Has(c) {
			t.Errorf("default-deny Has(%q) = true, want false", c)
		}
	}
}

// buildFullEnabled returns a Capabilities where every known capability
// is set to true.
func buildFullEnabled(fetchedAt time.Time) *Capabilities {
	m := make(map[Capability]bool, len(AllCapabilities()))
	for _, c := range AllCapabilities() {
		m[c] = true
	}
	return &Capabilities{Tier: "enterprise", Enabled: m, FetchedAt: fetchedAt}
}
