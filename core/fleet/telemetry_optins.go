// Package fleet — telemetry_optins.go
//
// Client for the per-class telemetry opt-in store
// (GET/PUT /api/v1/me/telemetry-opt-ins), the fleet-authoritative replacement
// for the local-only telemetry_consent.json
// (harness-fleet-sync-activation-01NSYNC01 gap #4).
//
// The fleet store is the source of truth for the seven telemetry classes
// (spec 007 §3.1). The harness fetches the opt-in set at enroll and pushes
// user toggles up. Posture is unchanged: every class defaults OFF (personal
// default) except harness.errors, and the OTLP export pipeline stays gated by
// TelemetryConsent.EffectiveLevel — opting in to a class never overrides the
// consent gate.
//
// Wire shapes mirror kenaz-fleet service/api_types.go exactly.
//
// Fork-removal recipe: delete this file; the harness falls back to the local
// telemetry_consent.json gate only.
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TelemetryOptInItem is a single telemetry class opt-in record. Mirrors
// fleet's TelemetryOptInItem.
type TelemetryOptInItem struct {
	Class   string     `json:"class"`
	OptedIn bool       `json:"opted_in"`
	OptedAt *time.Time `json:"opted_at,omitempty"`
	// Source is one of: user_self, org_policy, install_default.
	Source string `json:"source,omitempty"`
}

// listTelemetryOptInsResponse mirrors fleet's ListTelemetryOptInsResponse.
type listTelemetryOptInsResponse struct {
	OptIns []TelemetryOptInItem `json:"opt_ins"`
}

// updateTelemetryOptInsRequest mirrors fleet's UpdateTelemetryOptInsRequest.
// The server ignores OptedAt/Source in the request and forces
// source='user_self', opted_at=NOW().
type updateTelemetryOptInsRequest struct {
	Updates []TelemetryOptInItem `json:"updates"`
}

// KnownTelemetryClasses is the canonical ordered list of telemetry classes,
// mirroring kenaz-fleet service/lookups.go knownTelemetryClasses (spec 007
// §3.1). All default OFF except harness.errors.
var KnownTelemetryClasses = []string{
	"harness.usage_counts",
	"harness.tool_calls",
	"harness.errors",
	"harness.diagnostics",
	"sigil.heuristics",
	"sigil.predictions",
	"sigil.suggestions",
}

// GetTelemetryOptIns fetches the per-class opt-in set from the fleet store.
// Returns ErrFleetDisabled when the client is a nop. The response always
// carries all known classes (the server backfills install_default rows).
func (c *Client) GetTelemetryOptIns(ctx context.Context) ([]TelemetryOptInItem, error) {
	if c == nil || c.isNop {
		return nil, ErrFleetDisabled
	}
	resp, err := c.Get(ctx, "/api/v1/me/telemetry-opt-ins")
	if err != nil {
		return nil, fmt.Errorf("fleet: get telemetry opt-ins: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusForbidden {
		return nil, ErrCapabilityNotInTier
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fleet: get telemetry opt-ins: status %d: %s", resp.StatusCode, body)
	}
	var out listTelemetryOptInsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("fleet: get telemetry opt-ins: decode: %w", err)
	}
	return out.OptIns, nil
}

// PutTelemetryOptIns pushes user toggles to the fleet store. Each update needs
// Class + OptedIn; the server forces source='user_self' and opted_at=NOW().
// Returns ErrFleetDisabled when the client is a nop.
func (c *Client) PutTelemetryOptIns(ctx context.Context, updates []TelemetryOptInItem) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}
	body := updateTelemetryOptInsRequest{Updates: updates}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("fleet: put telemetry opt-ins: marshal: %w", err)
	}
	resp, err := c.Put(ctx, "/api/v1/me/telemetry-opt-ins", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("fleet: put telemetry opt-ins: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusForbidden {
		return ErrCapabilityNotInTier
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fleet: put telemetry opt-ins: status %d: %s", resp.StatusCode, b)
	}
	return nil
}
