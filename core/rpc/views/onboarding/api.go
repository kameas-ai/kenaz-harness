// Package onboarding is the view-scoped RPC surface for the harness
// onboarding flow (mission harness-self-mcp-onboarding-01KQ8TDU WP08).
//
// The frontend's OnboardingDialog binds to OnboardingAPI to:
//
//   - Read OnboardingState on app boot to decide whether to mount the
//     dialog (first-run condition: zero providers + dialog never
//     dismissed).
//   - Drive the Phase-1 FSM via Begin / Step (welcome → pick provider
//     → enter API key → test connection → done).
//   - Dismiss the dialog (sets OnboardingCompleted = true so it never
//     auto-shows again).
//   - RestartPhase2 from Settings → "Reconfigure with assistant" — opens
//     a fresh kind=onboarding session with the harness-self MCP enabled
//     and a "tell me what you want to change" system prompt.
//
// The package depends only on core/onboarding (FSM types) and the
// view-local helpers below. Concrete wiring of the FSM tester +
// settings + session.Manager lives in impl.go.
package onboarding

import (
	"context"

	coreonboarding "github.com/kameas-ai/kenaz-harness/core/onboarding"
)

// OnboardingState is the boot-time state the frontend reads to decide
// whether to mount the dialog.
type OnboardingState struct {
	// FirstRun is true when no provider is configured AND the user has
	// never dismissed the onboarding dialog. The frontend mounts the
	// dialog automatically when true.
	FirstRun bool `json:"firstRun"`
	// Completed mirrors Settings.OnboardingCompleted; true means the
	// dialog will not auto-mount but the user may still re-open it via
	// Settings → "Reconfigure with assistant".
	Completed bool `json:"completed"`
	// Phase is "phase1" while the deterministic FSM is running and
	// "phase2" once at least one provider is configured (the agent-
	// driven onboarding session has taken over). Empty when neither
	// phase is active.
	Phase string `json:"phase,omitempty"`
	// CurrentState is the active FSM state when Phase == "phase1".
	// Empty otherwise.
	CurrentState string `json:"currentState,omitempty"`
	// HarnessSelfMCPDisabled mirrors the Settings dial. When true the
	// frontend hides the "Reconfigure with assistant" button (the
	// agent-driven flow won't have its tool surface).
	HarnessSelfMCPDisabled bool `json:"harnessSelfMCPDisabled"`
}

// StepRequest carries the (state, event, payload) tuple the FSM
// needs to advance. Mirrors core/onboarding.FSM.Step parameters.
type StepRequest struct {
	State   string            `json:"state"`
	Event   string            `json:"event"`
	Payload map[string]string `json:"payload,omitempty"`
}

// StepResponse is the FSM's StepResult lifted to the wire shape.
// The caller renders Card directly.
type StepResponse struct {
	State string                  `json:"state"`
	Card  coreonboarding.Card     `json:"card"`
}

// RestartPhase2Request is the body the frontend sends when the user
// clicks "Reconfigure with assistant" (or picks a starter from the
// onboarding dialog). StarterID maps to a Starter.ID returned by
// LoadStarters in the harness-self builtin package.
type RestartPhase2Request struct {
	StarterID string `json:"starterId"`
}

// RestartPhase2Response carries the new session id the frontend should
// navigate to.
type RestartPhase2Response struct {
	SessionID string `json:"sessionId"`
}

// OnboardingAPI is the view-scoped accessor.
type OnboardingAPI interface {
	// State returns the boot-time OnboardingState.
	State(ctx context.Context) (OnboardingState, error)
	// Begin returns the initial FSM card without consuming an event.
	// The frontend calls this when the dialog mounts.
	Begin(ctx context.Context) (StepResponse, error)
	// Step advances the FSM by one event.
	Step(ctx context.Context, req StepRequest) (StepResponse, error)
	// Dismiss flips Settings.OnboardingCompleted to true so the dialog
	// will not auto-show again. Idempotent.
	Dismiss(ctx context.Context) error
	// RestartPhase2 spawns a kind=onboarding session with the
	// harness-self MCP enabled and the chosen starter's system prompt.
	// Returns the session id the frontend should navigate to.
	RestartPhase2(ctx context.Context, req RestartPhase2Request) (RestartPhase2Response, error)
	// ListStarters returns the curated starter prompts for the dialog
	// to render. Mirrors core/mcp/builtin/harness.LoadStarters but
	// uses a JSON-friendly subset (system prompt body trimmed).
	ListStarters(ctx context.Context) ([]StarterSummary, error)
}

// StarterSummary is the dialog-render shape — title + description plus
// metadata, but NOT the full system-prompt body (the frontend doesn't
// need it; the backend re-resolves the body when RestartPhase2 fires).
type StarterSummary struct {
	ID                  string   `json:"id"`
	Title               string   `json:"title"`
	Description         string   `json:"description"`
	RecommendedProvider string   `json:"recommendedProvider,omitempty"`
	RecommendedModel    string   `json:"recommendedModel,omitempty"`
	RecommendedRecipes  []string `json:"recommendedRecipes,omitempty"`
}
