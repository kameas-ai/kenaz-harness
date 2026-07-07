// Package fleet — onboarding.go
//
// Fleet-side onboarding state API: GET/PATCH /api/v1/me/onboarding.
//
// Contract: Fleet 01NWEL01 v1 (schema=1). Confirmed shipped on fleet prod.
//
// Wire shape (OnboardingStateWire) mirrors the fleet ContextOnboarding record.
// The harness updates fleet as onboarding milestones complete. All calls are
// best-effort: a nil/nop client returns ErrFleetDisabled; network errors are
// returned to the caller (who must log + swallow them in a non-blocking goroutine).
//
// OSS-first: this file lives in core/fleet/ (the fleet-touching layer). The
// onboarding view (core/rpc/views/onboarding/) remains fleet-free by using the
// ProgressSyncer interface wired from core/rpc/onboarding_wiring.go.
//
// Mission: fleet-welcome-01NWEL01 (harness integration seams WP04/WP07).
package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// OnboardingStateWire is the fleet wire shape for GET/PATCH /api/v1/me/onboarding.
// Schema version 1. Fields are omitempty so a PATCH body only carries changed fields.
//
// Invariant: no provider/tool credentials are ever included. Only named boolean
// progress milestones and the schema version tag.
type OnboardingStateWire struct {
	// Schema is the schema version. Always 1 for the v1 contract.
	Schema int `json:"schema,omitempty"`

	// ProviderConfigured is true when the harness has a working LLM provider.
	ProviderConfigured bool `json:"provider_configured,omitempty"`

	// AccountConnected is true when the user completed the sign-in step.
	AccountConnected bool `json:"account_connected,omitempty"`

	// BootstrapRun is true when the context-bootstrap ran at least once.
	BootstrapRun bool `json:"bootstrap_run,omitempty"`

	// GuidedActionShown is true when the first-guided-action step was shown.
	GuidedActionShown bool `json:"guided_action_shown,omitempty"`

	// ContextSynced is true when the harness performed its first successful
	// context push. Fleet also auto-derives this from context_nodes > 0 so
	// this field is best-effort / advisory.
	ContextSynced bool `json:"context_synced,omitempty"`

	// HarnessInstalled is derived server-side from node_registrations; the
	// harness never sets this — it is read-only in GET responses.
	HarnessInstalled bool `json:"harness_installed,omitempty"`
}

// GetOnboardingState fetches the caller's fleet onboarding state.
// Returns (zero, ErrFleetDisabled) on nop clients.
// Returns (zero, ErrNotSignedIn) when no valid token is in the keychain.
// All other errors are network/server errors.
func (c *Client) GetOnboardingState(ctx context.Context) (OnboardingStateWire, error) {
	if c == nil || c.isNop {
		return OnboardingStateWire{}, ErrFleetDisabled
	}
	resp, err := c.Get(ctx, "/api/v1/me/onboarding")
	if err != nil {
		return OnboardingStateWire{}, fmt.Errorf("fleet/onboarding: get: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized {
		return OnboardingStateWire{}, ErrNotSignedIn
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return OnboardingStateWire{}, fmt.Errorf("fleet/onboarding: get status %d: %s", resp.StatusCode, body)
	}

	var state OnboardingStateWire
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return OnboardingStateWire{}, fmt.Errorf("fleet/onboarding: get decode: %w", err)
	}
	return state, nil
}

// PatchOnboardingState applies a partial update to the caller's fleet onboarding
// state via PATCH /api/v1/me/onboarding. Only non-zero fields in update are sent.
//
// Returns ErrFleetDisabled on nop clients.
// Returns ErrNotSignedIn when no valid token is in the keychain.
// All other errors are network/server errors — callers should log and discard.
func (c *Client) PatchOnboardingState(ctx context.Context, update OnboardingStateWire) error {
	if c == nil || c.isNop {
		return ErrFleetDisabled
	}
	resp, err := c.PatchJSON(ctx, "/api/v1/me/onboarding", update)
	if err != nil {
		return fmt.Errorf("fleet/onboarding: patch: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrNotSignedIn
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("fleet/onboarding: patch status %d: %s", resp.StatusCode, body)
	}
	return nil
}
