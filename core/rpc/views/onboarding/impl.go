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

	harnessmcp "github.com/sigil-tech/kaneaz-harness/core/mcp/builtin/harness"
	coreonboarding "github.com/sigil-tech/kaneaz-harness/core/onboarding"
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

// Config bundles the host wiring an API needs. All fields are nil-safe;
// when nil the matching method returns a typed error or a sensible
// default, so the chassis-only test fixture compiles.
type Config struct {
	FSM            *coreonboarding.FSM
	FirstRun       FirstRunChecker
	Completion     CompletionMarker
	SessionStarter SessionStarter
	SettingsDial   SettingsDialReader
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
}

// New constructs an API. cfg may be the zero value for tests; production
// passes the full set.
func New(cfg Config) *API {
	if cfg.FSM == nil {
		cfg.FSM = coreonboarding.New(nil)
	}
	return &API{
		cfg:     cfg,
		current: coreonboarding.StateWelcome,
		fsmCtx:  coreonboarding.NewFSMContext(),
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
	a.mu.Lock()
	out.CurrentState = string(a.current)
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
	a.mu.Unlock()

	// On terminal Done state with a successful provider configuration,
	// flip the completed flag so the dialog never re-shows automatically.
	if r.State == coreonboarding.StateDone && a.cfg.Completion != nil {
		_ = a.cfg.Completion.MarkOnboardingCompleted(ctx)
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

// Compile-time witness.
var _ OnboardingAPI = (*API)(nil)
