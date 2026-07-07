// Concrete OnboardingAPI implementation. The view holds a stateful FSM
// context per process (the dialog is single-instance so we don't need
// per-tab state) plus thin adapters over the host's settings / session
// managers via narrow callbacks.
//
// All host dependencies are interface-typed so the view can be
// constructed in tests without dragging in the full core stack.
package onboarding

import (
	"context"
	"errors"
	"fmt"
	"sync"

	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	coreonboarding "github.com/kameas-ai/kenaz-harness/core/onboarding"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// FirstRunChecker reports whether the harness is in its zero-config
// initial state. Wired by the host to count providers / read the
// OnboardingCompleted setting.
type FirstRunChecker interface {
	IsFirstRun(ctx context.Context) (bool, error)
}

// CompletionMarker flips Settings.OnboardingCompleted to true. Wired to
// the host's settings store.
type CompletionMarker interface {
	MarkOnboardingCompleted(ctx context.Context) error
	IsCompleted(ctx context.Context) (bool, error)
}

// SessionStarter spawns a kind=onboarding session with the chosen
// starter's system prompt and the harness-self MCP enabled. Returns the
// new session id. Wired to session.Manager.CreateWithKind + the MCP
// pool's per-session enable surface.
type SessionStarter interface {
	StartOnboardingSession(ctx context.Context, starter harnessmcp.Starter) (string, error)
}

// SettingsDialReader reads Settings.HarnessSelfMCPDisabled.
type SettingsDialReader interface {
	IsHarnessSelfMCPDisabled(ctx context.Context) (bool, error)
}

// AccountStepAvailableChecker reports whether the fleet sign-in surface is
// wired in this build. When false, the frontend hides or greys the sign-in
// CTA. Wired in production to a simple check of HARNESS_FLEET_DISABLED.
type AccountStepAvailableChecker interface {
	IsAccountStepAvailable(ctx context.Context) (bool, error)
}

// Config bundles the host wiring an API needs. All fields are nil-safe;
// when nil the matching method returns a typed error or a sensible
// default, so the chassis-only test fixture compiles.
type Config struct {
	FSM            *coreonboarding.FSM
	FirstRun       FirstRunChecker
	Completion     CompletionMarker
	SessionStarter SessionStarter
	SettingsDial   SettingsDialReader
	// AccountStepAvailable indicates whether the fleet sign-in surface is
	// wired. When nil, AccountStepAvailable defaults to false.
	AccountStepAvailable AccountStepAvailableChecker
	// Signer drives the optional owned-login sign-in step (WP03).
	// When nil (fleet absent / OSS build) EventSignIn gracefully degrades in
	// the FSM — guided_action is shown with a "sign-in unavailable" note and
	// EventSkipAccount always succeeds (OSS-standalone invariant).
	// Ignored when FSM is non-nil (callers that supply their own FSM are
	// responsible for passing a signer to it directly).
	Signer coreonboarding.AccountSigner
	// DataDir is forwarded to harnessmcp.LoadStarters so user-overridden
	// starter prompts are picked up.
	DataDir string
}

// API is the concrete OnboardingAPI implementation.
type API struct {
	cfg Config

	mu      sync.Mutex
	current coreonboarding.State
	fsmCtx  coreonboarding.FSMContext

	// handoffHintMu protects the stored handoff hint (WP04 seam).
	handoffHintMu sync.Mutex
	handoffHint   HandoffHint

	// progressMu protects the in-memory progress set (WP07 seam).
	progressMu sync.Mutex
	progress   map[ProgressStep]bool
}

// New constructs an API. cfg may be the zero value for tests; production
// passes the full set.
//
// When cfg.FSM is nil, New builds a default FSM:
//   - with no LLM tester (test-connection always passes — callers that need
//     a real tester supply their own FSM via cfg.FSM)
//   - with no session-kind transitioner (skipped on terminal state)
//   - with cfg.Signer when non-nil, so EventSignIn triggers the owned-login
//     flow; when nil EventSignIn degrades gracefully (OSS-standalone invariant).
func New(cfg Config) *API {
	if cfg.FSM == nil {
		cfg.FSM = coreonboarding.NewFull(nil, nil, cfg.Signer)
	}
	return &API{
		cfg:      cfg,
		current:  coreonboarding.StateWelcome,
		fsmCtx:   coreonboarding.NewFSMContext(),
		progress: make(map[ProgressStep]bool),
	}
}

// ErrNotConfigured is returned when a host-side dependency the caller
// asked for is nil. The frontend renders a graceful "feature not
// available in this build" path.
var ErrNotConfigured = errors.New("onboarding: host dependency not configured")

// State implements OnboardingAPI.
func (a *API) State(ctx context.Context) (OnboardingState, error) {
	out := OnboardingState{}
	if a.cfg.FirstRun != nil {
		fr, err := a.cfg.FirstRun.IsFirstRun(ctx)
		if err != nil {
			return out, err
		}
		out.FirstRun = fr
	}
	if a.cfg.Completion != nil {
		c, err := a.cfg.Completion.IsCompleted(ctx)
		if err != nil {
			return out, err
		}
		out.Completed = c
	}
	if a.cfg.SettingsDial != nil {
		dis, err := a.cfg.SettingsDial.IsHarnessSelfMCPDisabled(ctx)
		if err != nil {
			return out, err
		}
		out.HarnessSelfMCPDisabled = dis
	}
	if a.cfg.AccountStepAvailable != nil {
		avail, err := a.cfg.AccountStepAvailable.IsAccountStepAvailable(ctx)
		if err == nil {
			out.AccountStepAvailable = avail
		}
	}
	a.mu.Lock()
	out.CurrentState = string(a.current)
	out.SignedIn = a.fsmCtx.SignedIn
	a.mu.Unlock()
	if out.FirstRun || !out.Completed {
		out.Phase = "phase1"
	}
	return out, nil
}

// Begin implements OnboardingAPI.
func (a *API) Begin(_ context.Context) (StepResponse, error) {
	r := coreonboarding.InitialCard()
	a.mu.Lock()
	a.current = r.State
	a.fsmCtx = coreonboarding.NewFSMContext()
	a.mu.Unlock()
	return StepResponse{State: string(r.State), Card: r.Card}, nil
}

// Step implements OnboardingAPI.
func (a *API) Step(ctx context.Context, req StepRequest) (StepResponse, error) {
	a.mu.Lock()
	state := coreonboarding.State(req.State)
	if state == "" {
		state = a.current
	}
	fsm := a.cfg.FSM
	r, err := fsm.Step(ctx, state, coreonboarding.Event(req.Event), req.Payload, &a.fsmCtx)
	if err != nil {
		a.mu.Unlock()
		return StepResponse{}, err
	}
	a.current = r.State
	// Capture FSM context fields for progress recording (below the lock).
	signedIn := a.fsmCtx.SignedIn
	a.mu.Unlock()

	// Record progress milestones and flip the completion flag as the FSM
	// advances through key states (WP01/WP07).
	switch r.State {
	case coreonboarding.StateAccountStep:
		// Provider was successfully configured.
		_ = a.RecordProgress(ctx, ProgressStepProviderConfigured)
	case coreonboarding.StateGuidedAction:
		if signedIn {
			_ = a.RecordProgress(ctx, ProgressStepAccountConnected)
		}
	case coreonboarding.StateDone:
		// The full onboarding flow reached its terminal state. Flip the
		// completed flag so the dialog never re-shows automatically.
		_ = a.RecordProgress(ctx, ProgressStepGuidedActionShown)
		if a.cfg.Completion != nil {
			_ = a.cfg.Completion.MarkOnboardingCompleted(ctx)
		}
	}
	return StepResponse{State: string(r.State), Card: r.Card}, nil
}

// Dismiss implements OnboardingAPI.
func (a *API) Dismiss(ctx context.Context) error {
	if a.cfg.Completion == nil {
		return ErrNotConfigured
	}
	return a.cfg.Completion.MarkOnboardingCompleted(ctx)
}

// RestartPhase2 implements OnboardingAPI.
func (a *API) RestartPhase2(ctx context.Context, req RestartPhase2Request) (RestartPhase2Response, error) {
	if a.cfg.SessionStarter == nil {
		return RestartPhase2Response{}, ErrNotConfigured
	}
	starters, err := harnessmcp.LoadStarters(a.cfg.DataDir)
	if err != nil {
		return RestartPhase2Response{}, fmt.Errorf("onboarding: load starters: %w", err)
	}
	var chosen harnessmcp.Starter
	for _, s := range starters {
		if s.ID == req.StarterID {
			chosen = s
			break
		}
	}
	if chosen.ID == "" {
		return RestartPhase2Response{}, fmt.Errorf("onboarding: unknown starter %q", req.StarterID)
	}
	id, err := a.cfg.SessionStarter.StartOnboardingSession(ctx, chosen)
	if err != nil {
		return RestartPhase2Response{}, err
	}
	if a.cfg.Completion != nil {
		_ = a.cfg.Completion.MarkOnboardingCompleted(ctx)
	}
	return RestartPhase2Response{SessionID: id}, nil
}

// ListStarters implements OnboardingAPI.
func (a *API) ListStarters(_ context.Context) ([]StarterSummary, error) {
	starters, err := harnessmcp.LoadStarters(a.cfg.DataDir)
	if err != nil {
		return nil, err
	}
	out := make([]StarterSummary, 0, len(starters))
	for _, s := range starters {
		out = append(out, StarterSummary{
			ID:                  s.ID,
			Title:               s.Title,
			Description:         s.Description,
			RecommendedProvider: s.RecommendedProvider,
			RecommendedModel:    s.RecommendedModel,
			RecommendedRecipes:  s.RecommendedRecipes,
		})
	}
	return out, nil
}

// AcceptHandoffHint implements OnboardingAPI (WP04 fleet handoff intake seam).
//
// DEFERRED FLEET INTEGRATION: the Fleet welcome flow (01NWEL01) will call
// the harness via a kameas:// deep-link. This seam stores the hint in memory
// so the account step can pre-fill the email. No auth is performed here.
func (a *API) AcceptHandoffHint(_ context.Context, hint HandoffHint) error {
	// Validate: hints with no email are silently ignored rather than errored
	// so a malformed deep-link never blocks onboarding.
	if hint.EmailHint == "" && hint.Source == "" {
		return nil
	}
	a.handoffHintMu.Lock()
	a.handoffHint = hint
	a.handoffHintMu.Unlock()
	logging.L().Info("onboarding.handoff_hint.accepted",
		"source", hint.Source,
		"has_email", hint.EmailHint != "",
	)
	return nil
}

// GetHandoffHint implements OnboardingAPI (WP04 fleet handoff intake seam).
func (a *API) GetHandoffHint(_ context.Context) (HandoffHint, error) {
	a.handoffHintMu.Lock()
	defer a.handoffHintMu.Unlock()
	return a.handoffHint, nil
}

// RecordProgress implements OnboardingAPI (WP07 progress sync seam).
//
// Records the step locally and — when signed in — attempts to mirror the
// progress to the Fleet shared checklist (01NWEL01).
//
// DEFERRED FLEET INTEGRATION: the Fleet progress-mirror endpoint is not yet
// deployed. The local recording always succeeds; the fleet push is a no-op
// until the fleet side ships. The integration point is clearly marked in the
// code below.
func (a *API) RecordProgress(_ context.Context, step ProgressStep) error {
	a.progressMu.Lock()
	if a.progress == nil {
		a.progress = make(map[ProgressStep]bool)
	}
	a.progress[step] = true
	a.progressMu.Unlock()
	logging.L().Info("onboarding.progress.recorded", "step", string(step))

	// DEFERRED FLEET INTEGRATION (WP07): mirror progress to Fleet shared checklist.
	// When the Fleet progress-mirror RPC ships (01NWEL01), add a non-blocking
	// goroutine here that calls the fleet client. The call must be:
	//   - non-blocking (goroutine)
	//   - best-effort (errors are logged, never returned to the caller)
	//   - gated on a.fsmCtx.SignedIn (only mirror when signed in)
	//   - guarded by the OSS-first invariant (no fleet import in this package)
	// The fleet call belongs in the host adapter (core/rpc/onboarding_wiring.go).
	return nil
}

// Compile-time witness.
var _ OnboardingAPI = (*API)(nil)
