package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type stubTier struct{ tier string }

func (s *stubTier) OrgTier() string { return s.tier }

func TestTelemetryConsent_DefaultIsNone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c, err := NewTelemetryConsent(dir, &stubTier{"pro"})
	if err != nil {
		t.Fatalf("NewTelemetryConsent: %v", err)
	}
	if c.Level() != ConsentNone {
		t.Errorf("want default %s, got %s", ConsentNone, c.Level())
	}
}

func TestTelemetryConsent_Persistence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c, _ := NewTelemetryConsent(dir, &stubTier{"pro"})
	if err := c.SetLevel(ConsentAggregate); err != nil {
		t.Fatalf("SetLevel: %v", err)
	}

	// Reload from disk.
	c2, err := NewTelemetryConsent(dir, &stubTier{"pro"})
	if err != nil {
		t.Fatalf("second NewTelemetryConsent: %v", err)
	}
	if c2.Level() != ConsentAggregate {
		t.Errorf("want persisted %s, got %s", ConsentAggregate, c2.Level())
	}
}

func TestTelemetryConsent_AggregatRequiresProTier(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c, _ := NewTelemetryConsent(dir, &stubTier{"free"})
	err := c.SetLevel(ConsentAggregate)
	if err == nil {
		t.Fatal("want error for free tier requesting aggregate, got nil")
	}
	var tierErr *ErrTierInsufficient
	if !errors.As(err, &tierErr) {
		t.Errorf("want ErrTierInsufficient, got %T: %v", err, err)
	}
}

func TestTelemetryConsent_FullRequiresTeamTier(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Pro tier should NOT allow full.
	c, _ := NewTelemetryConsent(dir, &stubTier{"pro"})
	if err := c.SetLevel(ConsentFull); err == nil {
		t.Error("want error for pro tier requesting full, got nil")
	}

	// Team tier should allow full.
	dir2 := t.TempDir()
	c2, _ := NewTelemetryConsent(dir2, &stubTier{"team"})
	if err := c2.SetLevel(ConsentFull); err != nil {
		t.Errorf("team tier should allow full: %v", err)
	}
}

func TestTelemetryConsent_NoneAlwaysAllowed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c, _ := NewTelemetryConsent(dir, &stubTier{"free"})
	if err := c.SetLevel(ConsentNone); err != nil {
		t.Errorf("none should always be allowed: %v", err)
	}
}

func TestTelemetryConsent_EffectiveLevelClampsOnDowngrade(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Set aggregate level with pro tier.
	c, _ := NewTelemetryConsent(dir, &stubTier{"pro"})
	_ = c.SetLevel(ConsentAggregate)

	// Simulate account downgrade: reload with free tier.
	c2, _ := NewTelemetryConsent(dir, &stubTier{"free"})
	// Stored level is aggregate, but effective level must clamp to none.
	if eff := c2.EffectiveLevel(); eff != ConsentNone {
		t.Errorf("want effective %s after downgrade, got %s", ConsentNone, eff)
	}
}

func TestTelemetryConsent_FileHas0600Mode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c, _ := NewTelemetryConsent(dir, &stubTier{"pro"})
	_ = c.SetLevel(ConsentAggregate)

	consentPath := filepath.Join(dir, "fleet", consentFile)
	fi, err := os.Stat(consentPath)
	if err != nil {
		t.Fatalf("stat consent file: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("consent file mode want 0600, got %v", fi.Mode().Perm())
	}
}

func TestTelemetryConsent_ConsentLevelImplementsInterface(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c, _ := NewTelemetryConsent(dir, nil)
	// The ConsentLevel() string shape is part of the consent contract even
	// though no interface names it any more (the FleetExporter that consumed
	// ConsentReader was removed as dead code).
	type consentLevelReader interface{ ConsentLevel() string }
	var _ consentLevelReader = c
	// Default when no tier: none.
	if got := c.ConsentLevel(); got != "none" {
		t.Errorf("want none, got %q", got)
	}
}
