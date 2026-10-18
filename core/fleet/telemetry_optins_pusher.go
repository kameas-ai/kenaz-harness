// Package fleet — telemetry_optins_pusher.go
//
// TelemetryOptInPusher pushes the per-class opt-in vector a consent tier
// implies (TierOptInUpdates, telemetry_tier_optins.go) to the fleet store,
// with local retry-durability across restarts.
package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// confirmedOptInPushFile is the filename within DataDir/fleet/ that records
// which consent tier's implied opt-in vector was last CONFIRMED pushed to
// the fleet store (i.e. the PUT succeeded).
const confirmedOptInPushFile = "telemetry_optins_confirmed.json"

// confirmedPushEnvelope is the on-disk shape.
type confirmedPushEnvelope struct {
	// ConfirmedLevel is the consent tier whose TierOptInUpdates vector was
	// last successfully PUT to the fleet store. Empty means never confirmed
	// (fresh install, or every attempt so far has failed/been skipped).
	ConfirmedLevel ConsentLevel `json:"confirmed_level"`
}

// TelemetryOptInPusher pushes the per-class opt-in vector implied by a
// consent tier (TierOptInUpdates) to the fleet store, with local
// retry-durability across restarts.
//
// # Failure semantics (owner ruling 2026-09-16)
//
// The local consent tier (TelemetryConsent.SetLevel) always persists first,
// independent of this pusher — this is an offline-first app and the local
// gate must never depend on a network round trip succeeding. A failed or
// skipped push does NOT roll back the local tier.
//
// Push failure is surfaced to the RPC caller as a returned error (see
// core/rpc/views/fleet/impl.go SetTelemetryConsent), not swallowed: a
// silently-dropped push under a different name is exactly the bug class
// this type exists to end.
//
// Retry is durable across process restarts because "confirmed" state is
// disk-backed, not in-memory: Push records the LAST CONFIRMED level on
// success and leaves it untouched on failure, so a mismatch between what
// TelemetryConsent currently says and what was last confirmed survives a
// restart. Two triggers close the loop:
//
//   - "next tier change": any subsequent Push call (same or different
//     level) attempts fresh — a real user action is never skipped just
//     because a previous attempt is still outstanding.
//   - "next app start": Reconcile, called from the settings package's
//     fleetEnroll (the existing post-login/app-resume reconciliation point
//     — see its sibling call to refreshTelemetryOptIns in
//     core/rpc/views/settings/fleet.go), compares the live consent level
//     against what is confirmed and retries on any mismatch, including one
//     left by an attempt that never ran at all (e.g. the fleet client was
//     nil at the time — see Push).
//
// Thread-safe.
type TelemetryOptInPusher struct {
	mu         sync.Mutex
	path       string
	clientFunc func() *Client
	onPushed   func([]TelemetryOptInItem)
}

// NewTelemetryOptInPusher constructs a pusher backed by
// <dataDir>/fleet/telemetry_optins_confirmed.json. dataDir may be empty
// (test chassis / no profile dir); persistence is then a no-op and every
// process start behaves like a fresh install (no durable retry across
// restarts, but Push/Reconcile still function within the process).
//
// clientFunc is called lazily on every Push/Reconcile so callers can supply
// a live accessor — the fleet client may not exist yet at construction
// time, or may change across sign-in/out — rather than a snapshot. A nil
// clientFunc, or one returning nil, makes Push a no-op that returns
// ErrFleetDisabled, matching the contract sibling fleet methods already use
// (e.g. FleetSetTelemetryOptIn).
func NewTelemetryOptInPusher(dataDir string, clientFunc func() *Client) *TelemetryOptInPusher {
	p := &TelemetryOptInPusher{clientFunc: clientFunc}
	if dataDir != "" {
		p.path = filepath.Join(dataDir, "fleet", confirmedOptInPushFile)
	}
	return p
}

// SetOnPushed registers a callback invoked with the full pushed vector after
// EVERY successful push (a fresh Push or one resolved by Reconcile). Used to
// feed the OTLP log lane's narrowing snapshot and the settings-layer read
// cache (settings.API.AdoptTelemetryOptIns) without a redundant GET round
// trip. Safe to leave unset; safe to call concurrently with Push.
func (p *TelemetryOptInPusher) SetOnPushed(fn func([]TelemetryOptInItem)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onPushed = fn
}

// Push derives TierOptInUpdates(level) and PUTs it to the fleet store.
//
//   - client unavailable (nil accessor, or accessor returns nil/nop):
//     returns ErrFleetDisabled without touching confirmed-state. There is
//     nothing to push while fleet is unreachable, and leaving the confirmed
//     marker untouched means Reconcile will correctly see a mismatch and
//     retry once a client becomes available.
//   - PUT fails for any other reason (offline, 5xx, capability-gated, ...):
//     confirmed-state is left untouched (still mismatched, so Reconcile
//     retries later) and the error is returned so the caller can surface
//     it.
//   - PUT succeeds: level is persisted as confirmed and onPushed fires with
//     the pushed vector.
func (p *TelemetryOptInPusher) Push(ctx context.Context, level ConsentLevel) error {
	client := p.client()
	if client == nil {
		return ErrFleetDisabled
	}
	updates := TierOptInUpdates(level)
	if err := client.PutTelemetryOptIns(ctx, updates); err != nil {
		return err
	}
	if err := p.persistConfirmed(level); err != nil {
		// The push itself succeeded server-side; only the local durability
		// marker failed to write. Surface it rather than pretending
		// everything is fine — a future Reconcile would otherwise believe
		// (wrongly) that a retry is still owed, or (if it silently
		// swallowed this) that everything is fine when the marker is
		// actually stale/corrupt.
		return fmt.Errorf("fleet: telemetry opt-ins pushed but failed to persist confirmation marker: %w", err)
	}
	p.fireOnPushed(updates)
	return nil
}

// Reconcile compares wantLevel (the caller's current source of truth —
// typically TelemetryConsent.Level()) against the last confirmed level and,
// on any mismatch, calls Push(ctx, wantLevel). No-op (nil error, no network
// call) when they already match. This is the "next app start" half of the
// retry contract documented on TelemetryOptInPusher; call it from any
// post-login/app-resume reconciliation point.
func (p *TelemetryOptInPusher) Reconcile(ctx context.Context, wantLevel ConsentLevel) error {
	if p.confirmedLevel() == wantLevel {
		return nil
	}
	return p.Push(ctx, wantLevel)
}

// Pending reports whether the currently confirmed level differs from
// wantLevel — i.e. whether a Reconcile(ctx, wantLevel) call would attempt a
// push. Diagnostic / test use.
func (p *TelemetryOptInPusher) Pending(wantLevel ConsentLevel) bool {
	return p.confirmedLevel() != wantLevel
}

// ConfirmedLevel returns the last level whose implied opt-in vector was
// confirmed pushed to the fleet store. Empty means never confirmed.
func (p *TelemetryOptInPusher) ConfirmedLevel() ConsentLevel {
	return p.confirmedLevel()
}

func (p *TelemetryOptInPusher) client() *Client {
	p.mu.Lock()
	fn := p.clientFunc
	p.mu.Unlock()
	if fn == nil {
		return nil
	}
	c := fn()
	if c == nil || c.isNop {
		return nil
	}
	return c
}

func (p *TelemetryOptInPusher) fireOnPushed(items []TelemetryOptInItem) {
	p.mu.Lock()
	fn := p.onPushed
	p.mu.Unlock()
	if fn != nil {
		fn(items)
	}
}

func (p *TelemetryOptInPusher) confirmedLevel() ConsentLevel {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.load().ConfirmedLevel
}

func (p *TelemetryOptInPusher) persistConfirmed(level ConsentLevel) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.path == "" {
		return nil // no dataDir (test chassis) — best-effort, nothing to persist to
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(confirmedPushEnvelope{ConfirmedLevel: level}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.path, data, 0600)
}

// load reads the confirmed marker. Must be called with mu held. A missing
// or malformed file is treated as "never confirmed" (zero value) rather
// than an error — this is a best-effort local cache, not a source of
// truth, and a corrupt/absent file just means the next Reconcile retries,
// which is the safe direction to fail in.
func (p *TelemetryOptInPusher) load() confirmedPushEnvelope {
	if p.path == "" {
		return confirmedPushEnvelope{}
	}
	data, err := os.ReadFile(p.path)
	if err != nil {
		return confirmedPushEnvelope{}
	}
	var env confirmedPushEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return confirmedPushEnvelope{}
	}
	return env
}
