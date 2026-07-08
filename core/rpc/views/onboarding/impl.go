// Concrete OnboardingAPI implementation. The view holds a stateful FSM
// context per process (the dialog is single-instance so we don't need
// per-tab state) plus thin adapters over the host's settings / session
// managers via narrow callbacks.
//
// All host dependencies are interface-typed so the view can be
// constructed in tests without dragging in the full core stack.
//
// Fleet integration seams (01NWEL01):
//
//   - ProgressSyncer  — mirrors named progress milestones to fleet PATCH
//     /api/v1/me/onboarding when the user is signed in (WP07).
//   - FleetStateReader — reads fleet GET /api/v1/me/onboarding on Begin
//     to warm Path A (pre-fill / skip already-completed steps) (WP04).
//
// Both interfaces are OSS-first: defined here, implemented in
// core/rpc/onboarding_wiring.go which is allowed to import core/fleet/.
package onboarding

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	coreonboarding "github.com/kameas-ai/kenaz-harness/core/onboarding"
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

// ProgressSyncer mirrors a named onboarding milestone to the Fleet shared
// checklist (PATCH /api/v1/me/onboarding). Implemented in
// core/rpc/onboarding_wiring.go by onboardingProgressSyncerAdapter.
//
// OSS-first invariant: this interface must NOT be implemented here —
// the implementation lives in the wiring layer that is permitted to
// import core/fleet/.
//
// The call is always best-effort and non-blocking from the caller's
// perspective. When fleet is disabled/not signed-in, SyncProgress is a
// no-op (returns nil immediately).
type ProgressSyncer interface {
	// SyncProgress pushes the given step (as a partial PATCH) to fleet.
	// Must return quickly — the implementation spawns a goroutine for
	// the actual network call. Never returns an error that would surface
	// to the user.
	SyncProgress(ctx context.Context, step ProgressStep, signedIn bool) error
}

// FleetStateReader reads the fleet-side onboarding state (GET
// /api/v1/me/onboarding). Used by Begin to warm Path A: if the user is
// already signed in and fleet shows previously completed steps, the FSM
// can skip them. Implemented in core/rpc/onboarding_wiring.go.
//
// OSS-first invariant: same as ProgressSyncer — defined here, wired from
// the allowed bridge file.
//
// ReadFleetState returns a FleetOnboardingHint. When fleet is disabled,
// not signed-in, or the call fails, the zero value is returned (no pre-fill).
// Errors are never surfaced to the onboarding dialog.
type FleetStateReader interface {
	ReadFleetState(ctx context.Context) (FleetOnboardingHint, error)
}

// BootstrapRunner kicks a context-bootstrap run for the onboarding bootstrap
// step (context-bootstrap-harness-integration WP06). Implemented in
// core/rpc/onboarding_wiring.go by onboardingBootstrapRunnerAdapter, which
// delegates to the context-bootstrap orchestration API.
//
// OSS-first invariant: this interface is defined here (fleet-free); the
// implementation lives in the bridge file that may import the engine + fleet.
// A nil BootstrapRunner makes RunBootstrap a no-op that still records the
// progress step locally (so the FSM does not stall on an OSS build).
type BootstrapRunner interface {
	// RunBootstrap dispatches a bootstrap run over the given connector ids and
	// blocks until it finishes. Returns the run id on success.
	RunBootstrap(ctx context.Context, consentedSources []string) (string, error)
}

// FleetOnboardingHint carries the relevant fields from the fleet
// OnboardingState that the harness cares about for Path A warm-start.
// The struct is intentionally narrow — only pre-fill / skip signals,
// never credentials or sensitive data.
type FleetOnboardingHint struct {
	// AlreadyConnected is true when fleet reports account_connected=true.
	// The FSM may skip the account step when this is true and the user is
	// already signed in to the same account.
	AlreadyConnected bool
	// AlreadyHarnessInstalled is true when fleet reports harness_installed=true.
	// Informational; no current FSM effect (future: fast-path skip of install step).
	AlreadyHarnessInstalled bool
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

	// ── 01NWEL01 fleet integration seams ──────────────────────────────────

	// ProgressSyncer mirrors named milestones to fleet (WP07).
	// When nil, RecordProgress only records locally (deferred-integration
	// posture preserved for OSS builds / tests).
	ProgressSyncer ProgressSyncer

	// FleetStateReader reads fleet onboarding state on Begin (WP04).
	// When nil, Begin does not attempt fleet state read (zero hint).
	FleetStateReader FleetStateReader

	// BootstrapRunner kicks the context-bootstrap run for the bootstrap step
	// (context-bootstrap-harness-integration WP06). When nil, RunBootstrap
	// only records the step locally (OSS build / test posture).
	BootstrapRunner BootstrapRunner
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
//
// WP04 fleet state warm-start: when a FleetStateReader is wired and the
// user is signed in, Begin fetches fleet onboarding state and stores the
// hint for use by the account step. This is best-effort; failures are
// logged and ignored — the account step always starts empty on error.
func (a *API) Begin(ctx context.Context) (StepResponse, error) {
	r := coreonboarding.InitialCard()
	a.mu.Lock()
	a.current = r.State
	a.fsmCtx = coreonboarding.NewFSMContext()
	a.mu.Unlock()

	// WP04: read fleet state for Path A warm-start. Non-blocking: errors
	// are logged and swallowed so a fleet outage never blocks onboarding.
	if a.cfg.FleetStateReader != nil {
		hint, err := a.cfg.FleetStateReader.ReadFleetState(ctx)
		if err != nil {
			// Log at debug level — fleet may not be available (offline / OSS).
			logging.L().Debug("onboarding.fleet_state.read_failed",
				"err", err.Error(),
			)
		} else if hint.AlreadyConnected {
			// Store a synthetic handoff hint so the account step can note
			// the user is already enrolled. The hint carries no auth grant.
			a.handoffHintMu.Lock()
			// Only set if there is no existing deep-link hint (deep-link wins).
			if a.handoffHint.EmailHint == "" {
				a.handoffHint = HandoffHint{
					Source: "fleet_state",
				}
			}
			a.handoffHintMu.Unlock()
			logging.L().Info("onboarding.fleet_state.warm_start",
				"already_connected", hint.AlreadyConnected,
				"harness_installed", hint.AlreadyHarnessInstalled,
			)
		}
	}

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
// Stores a non-authenticating deep-link hint from the Fleet welcome page
// (01NWEL01). The hint pre-fills the email in the account step but does not
// grant any access.
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
//
// WP04 full: returns the stored HandoffHint. When a fleet state warm-start
// set a synthetic hint (source="fleet_state") and the user later receives a
// real deep-link hint, the deep-link wins (set in AcceptHandoffHint).
func (a *API) GetHandoffHint(_ context.Context) (HandoffHint, error) {
	a.handoffHintMu.Lock()
	defer a.handoffHintMu.Unlock()
	return a.handoffHint, nil
}

// RecordProgress implements OnboardingAPI (WP07 progress sync seam).
//
// Records the step locally and — when signed in and a ProgressSyncer is
// wired — fires a non-blocking best-effort mirror to Fleet (01NWEL01).
//
// The fleet push is:
//   - non-blocking (goroutine launched by ProgressSyncer.SyncProgress)
//   - best-effort (errors are logged, never returned to the caller)
//   - gated on fsmCtx.SignedIn (only mirror when signed in)
//   - OSS-first (fleet import lives in the adapter, not here)
func (a *API) RecordProgress(ctx context.Context, step ProgressStep) error {
	a.progressMu.Lock()
	if a.progress == nil {
		a.progress = make(map[ProgressStep]bool)
	}
	a.progress[step] = true
	a.progressMu.Unlock()
	logging.L().Info("onboarding.progress.recorded", "step", string(step))

	// WP07: mirror to fleet when signed in and syncer is wired.
	if a.cfg.ProgressSyncer != nil {
		a.mu.Lock()
		signedIn := a.fsmCtx.SignedIn
		a.mu.Unlock()
		// SyncProgress is expected to be non-blocking (goroutine inside the adapter).
		// We still discard any error here (best-effort contract).
		_ = a.cfg.ProgressSyncer.SyncProgress(ctx, step, signedIn)
	}
	return nil
}

// RunBootstrap implements OnboardingAPI (WP06 bootstrap trigger seam).
//
// It kicks a context-bootstrap run over the consented connector ids via the
// wired BootstrapRunner, blocks until the run finishes, and then records the
// ProgressStepBootstrapRun milestone (which also mirrors bootstrap_run=true to
// fleet via the ProgressSyncer).
//
// Graceful degradation:
//   - nil BootstrapRunner (OSS build / test) → record the step locally and
//     return an empty run id, so the FSM never stalls.
//   - runner error → surface it to the caller (the frontend shows a retry)
//     WITHOUT marking the step complete.
func (a *API) RunBootstrap(ctx context.Context, consentedSources []string) (string, error) {
	if a.cfg.BootstrapRunner == nil {
		// OSS build / no engine: mark the step done so onboarding can proceed.
		_ = a.RecordProgress(ctx, ProgressStepBootstrapRun)
		logging.L().Info("onboarding.bootstrap.skipped", "reason", "no_runner")
		return "", nil
	}
	runID, err := a.cfg.BootstrapRunner.RunBootstrap(ctx, consentedSources)
	if err != nil {
		logging.L().Warn("onboarding.bootstrap.failed", "err", err.Error())
		return "", err
	}
	// Run finished — mark the step complete (mirrors bootstrap_run to fleet).
	_ = a.RecordProgress(ctx, ProgressStepBootstrapRun)
	logging.L().Info("onboarding.bootstrap.done", "run_id", runID)
	return runID, nil
}

// Compile-time witness.
var _ OnboardingAPI = (*API)(nil)
