package fleet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ConsentLevel is the three-tier opt-in for fleet telemetry.
type ConsentLevel string

const (
	// ConsentNone disables the FleetExporter entirely. No telemetry is
	// posted. This is the default.
	ConsentNone ConsentLevel = "none"

	// ConsentAggregate ships counts + durations only: no string payloads
	// beyond span names, no log records. Requires Pro+ subscription.
	ConsentAggregate ConsentLevel = "aggregate"

	// ConsentFull ships all redactor-cleaned telemetry (spans + metrics +
	// log records). Errors still have credentials scrubbed. Requires Team+
	// subscription.
	ConsentFull ConsentLevel = "full"
)

// IsValid reports whether c is one of the three valid consent levels.
func (c ConsentLevel) IsValid() bool {
	return c == ConsentNone || c == ConsentAggregate || c == ConsentFull
}

// OrgTierReader reads the current organization/billing tier for capability
// gating. Returns one of: "free", "pro", "team", "enterprise". Empty string
// is treated as "free" (most restrictive fallback).
//
// The interface is intentionally minimal: the orgs-tiers-billing mission wires
// the real implementation; tests inject a stub.
type OrgTierReader interface {
	OrgTier() string
}

// StaticTierReader is a no-op OrgTierReader that always returns "free".
// Used as the default wiring until the orgs-tiers-billing mission wires the
// real implementation.
type StaticTierReader struct{}

func (StaticTierReader) OrgTier() string { return "free" }

// consentFile is the filename within DataDir/fleet/ where the consent state
// is persisted.
const consentFile = "telemetry_consent.json"

// consentEnvelope is the on-disk JSON shape.
type consentEnvelope struct {
	// Level is the persisted ConsentLevel string.
	Level ConsentLevel `json:"level"`
}

// TelemetryConsent manages the three-tier opt-in state for fleet telemetry.
// It persists the chosen level to <DataDir>/fleet/telemetry_consent.json and
// enforces capability gating: only Pro+ accounts may use ConsentAggregate,
// only Team+ accounts may use ConsentFull.
//
// Thread-safe.
type TelemetryConsent struct {
	mu      sync.RWMutex
	level   ConsentLevel
	path    string
	orgTier OrgTierReader
}

// NewTelemetryConsent loads the persisted consent from
// <dataDir>/fleet/telemetry_consent.json (creating the file with default
// ConsentNone on first run). orgTier may be nil, in which case the tier is
// treated as "free" (most restrictive).
func NewTelemetryConsent(dataDir string, orgTier OrgTierReader) (*TelemetryConsent, error) {
	if dataDir == "" {
		return nil, errors.New("fleet/consent: NewTelemetryConsent: dataDir must not be empty")
	}
	dir := filepath.Join(dataDir, "fleet")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("fleet/consent: mkdir: %w", err)
	}
	path := filepath.Join(dir, consentFile)

	c := &TelemetryConsent{
		path:    path,
		orgTier: orgTier,
		level:   ConsentNone, // safe default
	}

	// Load existing value; ignore if not present.
	if err := c.load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("fleet/consent: load: %w", err)
	}
	return c, nil
}

// ConsentLevel implements ConsentReader. Returns the current persisted level.
func (c *TelemetryConsent) ConsentLevel() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return string(c.level)
}

// Level returns the current ConsentLevel typed value.
func (c *TelemetryConsent) Level() ConsentLevel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.level
}

// SetLevel persists a new consent level after verifying that the active
// org tier is permitted to use it.
//
// Capability requirements:
//   - ConsentNone: always permitted.
//   - ConsentAggregate: requires "pro", "team", or "enterprise" org tier.
//   - ConsentFull: requires "team" or "enterprise" org tier.
//
// Returns ErrTierInsufficient if the org tier is too low for the requested
// level.
func (c *TelemetryConsent) SetLevel(level ConsentLevel) error {
	if !level.IsValid() {
		return fmt.Errorf("fleet/consent: SetLevel: invalid level %q", level)
	}

	tier := c.currentTier()
	if err := checkCapability(level, tier); err != nil {
		return err
	}

	c.mu.Lock()
	c.level = level
	c.mu.Unlock()

	return c.persist(level)
}

// EffectiveLevel returns the effective consent level, clamped down to what the
// current org tier permits. This is the safe read for the FleetExporter: even
// if a stored level is too high for the current tier (e.g. after a downgrade),
// the exporter sees the clamped value.
func (c *TelemetryConsent) EffectiveLevel() ConsentLevel {
	tier := c.currentTier()
	c.mu.RLock()
	stored := c.level
	c.mu.RUnlock()

	switch stored {
	case ConsentFull:
		if !tierAtLeast(tier, "team") {
			return ConsentNone // downgraded account: fail-closed
		}
	case ConsentAggregate:
		if !tierAtLeast(tier, "pro") {
			return ConsentNone
		}
	}
	return stored
}

// ── ErrTierInsufficient ────────────────────────────────────────────────────

// ErrTierInsufficient is returned by SetLevel when the org tier is too low.
type ErrTierInsufficient struct {
	Requested ConsentLevel
	Required  string
	Current   string
}

func (e *ErrTierInsufficient) Error() string {
	return fmt.Sprintf(
		"fleet/consent: %q consent requires %s+ subscription (current: %s)",
		e.Requested, e.Required, e.Current,
	)
}

// ── internal helpers ────────────────────────────────────────────────────────

func (c *TelemetryConsent) currentTier() string {
	if c.orgTier == nil {
		return "free"
	}
	return c.orgTier.OrgTier()
}

// checkCapability returns ErrTierInsufficient when level requires a higher tier
// than currently available.
func checkCapability(level ConsentLevel, tier string) error {
	switch level {
	case ConsentAggregate:
		if !tierAtLeast(tier, "pro") {
			return &ErrTierInsufficient{
				Requested: level,
				Required:  "pro",
				Current:   tier,
			}
		}
	case ConsentFull:
		if !tierAtLeast(tier, "team") {
			return &ErrTierInsufficient{
				Requested: level,
				Required:  "team",
				Current:   tier,
			}
		}
	}
	return nil
}

// tierAtLeast reports whether tier is at least as high as required in the
// canonical ordering: free < pro < team < enterprise.
func tierAtLeast(tier, required string) bool {
	rank := map[string]int{
		"free":       0,
		"pro":        1,
		"team":       2,
		"enterprise": 3,
	}
	return rank[tier] >= rank[required]
}

func (c *TelemetryConsent) load() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return err
	}
	var env consentEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("unmarshal consent: %w", err)
	}
	if !env.Level.IsValid() {
		return nil // ignore unknown values; stay at default
	}
	c.mu.Lock()
	c.level = env.Level
	c.mu.Unlock()
	return nil
}

func (c *TelemetryConsent) persist(level ConsentLevel) error {
	env := consentEnvelope{Level: level}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0600)
}
