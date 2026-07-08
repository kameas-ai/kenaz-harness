// Package onboarding is the view-scoped RPC surface for the harness
// onboarding flow.
//
// The frontend's OnboardingDialog binds to OnboardingAPI to:
//
//   - Read OnboardingState on app boot to decide whether to mount the
//     dialog (first-run condition: zero providers + dialog never
//     dismissed).
//   - Drive the Phase-1 FSM via Begin / Step (welcome → pick provider
//     → enter API key → test connection → account step → guided action
//     → done).
//   - Dismiss the dialog (sets OnboardingCompleted = true so it never
//     auto-shows again).
//   - RestartPhase2 from Settings → "Reconfigure with assistant" — opens
//     a fresh kind=onboarding session with the harness-self MCP enabled
//     and a "tell me what you want to change" system prompt.
//
// Extended in harness-onboarding-01NHON01: adds the account step (WP03),
// first guided action (WP06), fleet handoff intake seam (WP04), context
// bootstrap seam (WP05), and progress sync seam (WP07).
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
	// Completed mirrors Settings.FirstRunOnboardingCompleted; true means the
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

	// ── Extended state (harness-onboarding-01NHON01 WP01/WP03) ─────────────

	// SignedIn is true when the user completed the account step with a
	// successful sign-in. Always false in the OSS-standalone path.
	SignedIn bool `json:"signedIn,omitempty"`
	// AccountStepAvailable is true when the fleet sign-in surface is wired
	// (i.e. HARNESS_FLEET_DISABLED != 1). When false the frontend may hide
	// the account CTA or render it as "not available in this build".
	AccountStepAvailable bool `json:"accountStepAvailable,omitempty"`
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
	State string              `json:"state"`
	Card  coreonboarding.Card `json:"card"`
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

// HandoffHint is the non-authenticating deep-link hint the Fleet welcome
// page may pass via a custom URL scheme to pre-fill the account step.
// Per spec §9 Q1 (RESOLVED): auth is always the real owned-login; this
// hint only pre-fills the email field. It carries no access grant.
//
// SEAM: WP04 — fleet handoff intake. Populated when the harness detects
// a kameas:// deep-link or an --onboarding-token CLI flag on startup.
// When absent, the account step starts empty (no pre-fill).
//
// DEFERRED FLEET INTEGRATION: the Fleet welcome flow (01NWEL01) will
// call the harness via deep-link on "install the harness". This seam
// accepts the hint when it arrives; the fleet side is not yet shipped.
type HandoffHint struct {
	// EmailHint is the email address to pre-fill in the sign-in form.
	// Never used as authentication — it is display-only.
	EmailHint string `json:"emailHint,omitempty"`
	// Source identifies where the hint came from: "deep_link" | "cli_arg".
	Source string `json:"source,omitempty"`
}

// ProgressStep identifies a named onboarding step for progress sync (WP07).
type ProgressStep string

const (
	ProgressStepProviderConfigured ProgressStep = "provider_configured"
	ProgressStepAccountConnected   ProgressStep = "account_connected"
	ProgressStepBootstrapRun       ProgressStep = "bootstrap_run"
	ProgressStepGuidedActionShown  ProgressStep = "guided_action_shown"
)

// OnboardingAPI is the view-scoped accessor.
type OnboardingAPI interface {
	// State returns the boot-time OnboardingState.
	State(ctx context.Context) (OnboardingState, error)
	// Begin returns the initial FSM card without consuming an event.
	// The frontend calls this when the dialog mounts.
	Begin(ctx context.Context) (StepResponse, error)
	// Step advances the FSM by one event.
	Step(ctx context.Context, req StepRequest) (StepResponse, error)
	// Dismiss flips Settings.FirstRunOnboardingCompleted to true so the dialog
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

	// ── WP04: Fleet handoff intake seam ────────────────────────────────────
	//
	// AcceptHandoffHint stores a non-authenticating deep-link hint from the
	// Fleet welcome page (01NWEL01). The hint pre-fills the email in the
	// account step but does not grant any access.
	//
	// DEFERRED FLEET INTEGRATION: the Fleet welcome page is not yet deployed.
	// This seam accepts hints now so a future fleet update can call it
	// without a harness release. Returns nil when fleet is disabled or the
	// hint is malformed — never returns an error that would block onboarding.
	AcceptHandoffHint(ctx context.Context, hint HandoffHint) error

	// GetHandoffHint returns the most recently stored HandoffHint, or a
	// zero value when none is present.
	GetHandoffHint(ctx context.Context) (HandoffHint, error)

	// ── WP07: Progress sync seam ────────────────────────────────────────────
	//
	// RecordProgress marks a named step as complete locally and (when
	// signed in) attempts to mirror the update to the Fleet shared checklist
	// (01NWEL01). The call is best-effort and non-blocking.
	//
	// DEFERRED FLEET INTEGRATION: the Fleet progress-mirror endpoint is not
	// yet deployed. The local recording always succeeds; the fleet push is a
	// no-op until the fleet side ships.
	RecordProgress(ctx context.Context, step ProgressStep) error

	// ── WP06: Bootstrap trigger seam ────────────────────────────────────────
	//
	// RunBootstrap kicks a context-bootstrap run over the consented connector
	// ids, blocks until it finishes, then records the bootstrap_run milestone.
	// Returns the run id. Degrades to a local no-op (records the step, empty
	// run id) when no engine is wired (OSS build).
	// (context-bootstrap-harness-integration WP06)
	RunBootstrap(ctx context.Context, consentedSources []string) (string, error)
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
