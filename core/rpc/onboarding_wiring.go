// onboarding_wiring.go wires the onboarding view (core/rpc/views/onboarding)
// with concrete adapters over the production managers.
//
// Mission: harness-self-mcp-onboarding-01KQ8TDU WP08 (tail polish, v0.5.4).
// Extended: harness-onboarding-01NHON01 WP01/WP03/WP04/WP07.
// Extended: fleet-welcome-01NWEL01 — live fleet integration (WP04/WP07).
//
// The adapter types here satisfy the narrow interfaces declared in
// core/rpc/views/onboarding/impl.go so the onboarding view can be
// constructed without cyclic imports into core/rpc/api.go.
//
// Completion state is now persisted via Settings.FirstRunOnboardingCompleted
// (added in WP01). The AccountStepAvailableChecker gracefully degrades to
// "unavailable" when fleet is disabled or not wired.
//
// Fleet integration (01NWEL01):
//
//   - onboardingProgressSyncerAdapter: PATCH /api/v1/me/onboarding when a
//     milestone is recorded and the user is signed in (WP07).
//   - onboardingFleetStateReaderAdapter: GET /api/v1/me/onboarding on Begin
//     to warm Path A (WP04).
//
// Both adapters are best-effort and graceful-on-disabled. The fleet client is
// injected at construction time from api.go (the chassis layer).
package rpc

import (
	"context"
	"errors"
	"os"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	onboardingview "github.com/kameas-ai/kenaz-harness/core/rpc/views/onboarding"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// ---- FirstRunChecker adapter ------------------------------------------------

// onboardingFirstRunAdapter implements onboardingview.FirstRunChecker.
// IsFirstRun returns true when the provider count is zero.
// OnboardingCompleted is not yet persisted, so we treat it as always false
// (i.e. the dialog may appear on every cold start until a provider is
// configured). A follow-up adds a real completion flag.
type onboardingFirstRunAdapter struct {
	llmAPI llmview.LLMConnectorAPI
}

func (a onboardingFirstRunAdapter) IsFirstRun(ctx context.Context) (bool, error) {
	if a.llmAPI == nil {
		return false, nil
	}
	providers, err := a.llmAPI.ListProviders(ctx)
	if err != nil {
		return false, err
	}
	return len(providers) == 0, nil
}

// ---- CompletionMarker adapter -----------------------------------------------

// onboardingCompletionAdapter implements onboardingview.CompletionMarker.
// Persists and reads Settings.FirstRunOnboardingCompleted via the settings
// store (harness-onboarding-01NHON01 WP01).
//
// When store is nil (test/nil-core path), MarkOnboardingCompleted is a no-op
// and IsCompleted returns false — the dialog will show on every cold start
// until a provider is added, which is the correct first-run behaviour for
// a bare test environment.
type onboardingCompletionAdapter struct {
	store settings.SettingsStore
}

func (a onboardingCompletionAdapter) MarkOnboardingCompleted(_ context.Context) error {
	if a.store == nil {
		return nil
	}
	return a.store.SaveFirstRunOnboardingCompleted(true)
}

func (a onboardingCompletionAdapter) IsCompleted(_ context.Context) (bool, error) {
	if a.store == nil {
		return false, nil
	}
	return a.store.LoadFirstRunOnboardingCompleted()
}

// ---- AccountStepAvailableChecker adapter ------------------------------------

// onboardingAccountStepAdapter implements onboardingview.AccountStepAvailableChecker.
//
// The account step is considered available when fleet is NOT explicitly
// disabled via the HARNESS_FLEET_DISABLED environment variable. This is
// intentionally env-var-gated rather than reading the fleet client to avoid
// importing core/fleet/ into this package (the OSS-first invariant).
//
// WP03 (01NWEL01): the fleet sign-in endpoint is now live. This adapter
// continues to use the env-var gate only — a false positive just shows a CTA
// that silently degrades in the FSM, and the env-var is the canonical gate.
type onboardingAccountStepAdapter struct{}

func (onboardingAccountStepAdapter) IsAccountStepAvailable(_ context.Context) (bool, error) {
	// HARNESS_FLEET_DISABLED=1 opts the build out of the fleet surface entirely.
	// Any other value (including absent) leaves the CTA visible.
	if os.Getenv("HARNESS_FLEET_DISABLED") == "1" {
		return false, nil
	}
	return true, nil
}

// ---- SessionStarter adapter --------------------------------------------------

// onboardingSessionStarterAdapter implements onboardingview.SessionStarter.
// It spawns a kind=onboarding session with the chosen starter's system prompt.
type onboardingSessionStarterAdapter struct {
	sessionMgr *session.Manager
	dataDir    string
}

func (a onboardingSessionStarterAdapter) StartOnboardingSession(
	ctx context.Context,
	starter harnessmcp.Starter,
) (string, error) {
	if a.sessionMgr == nil {
		return "", ErrOnboardingNotWired
	}
	name := starter.Title
	if name == "" {
		name = "Onboarding"
	}
	rec, err := a.sessionMgr.CreateWithKind(
		ctx,
		name,
		nil,
		session.SessionKindOnboarding,
	)
	if err != nil {
		return "", err
	}
	return rec.ID, nil
}

// ---- SettingsDialReader adapter ---------------------------------------------

// onboardingSettingsDialAdapter implements onboardingview.SettingsDialReader.
// HarnessSelfMCPDisabled is not yet in the settings store, so we always
// return false (= MCP enabled). A follow-up adds the persistent dial.
type onboardingSettingsDialAdapter struct{}

func (onboardingSettingsDialAdapter) IsHarnessSelfMCPDisabled(_ context.Context) (bool, error) {
	return false, nil
}

// ---- AccountSigner adapter --------------------------------------------------

// onboardingAccountSignerAdapter implements coreonboarding.AccountSigner by
// delegating to the fleet owned-login sign-in flow already shipped in
// core/rpc/views/settings.
//
// INVARIANT: when Fleet is disabled or the settings API is nil, SignIn returns
// a user-readable error (never a nil email) so the FSM can surface a clear
// "sign-in unavailable" message to the user in the account step.
// EventSkipAccount is always accepted regardless — the OSS-standalone path
// must never be blocked.
//
// This adapter lives in core/rpc/onboarding_wiring.go because that package is
// allowed to import core/fleet (it is NOT flagged by check-no-fleet-imports.sh,
// which excludes core/rpc/onboarding_wiring.go explicitly as an allowlisted
// bridge file). The core/onboarding and core/rpc/views/onboarding packages
// remain fleet-free.
type onboardingAccountSignerAdapter struct {
	settingsAPI settings.SettingsAPI
}

// SignIn opens the fleet owned-login device-code flow and blocks until the
// user completes or cancels authentication. Satisfies coreonboarding.AccountSigner.
func (a onboardingAccountSignerAdapter) SignIn(ctx context.Context) (string, error) {
	if a.settingsAPI == nil {
		return "", errors.New("account sign-in unavailable: settings not initialised")
	}
	if fleet.Disabled() {
		return "", errors.New("account sign-in unavailable: Fleet is disabled in this build (HARNESS_FLEET_DISABLED=1)")
	}
	id, err := a.settingsAPI.FleetSignIn(ctx)
	if err != nil {
		return "", err
	}
	return id.Email, nil
}

// ---- ProgressSyncer adapter (WP07, 01NWEL01) --------------------------------

// onboardingProgressSyncerAdapter implements onboardingview.ProgressSyncer.
// On each milestone it fires a non-blocking best-effort PATCH to the fleet
// onboarding state endpoint — but ONLY when the user is signed in.
//
// Graceful-on-disabled contract:
//   - nil fleet client → log debug + return nil
//   - fleet.Disabled() → return nil
//   - network error → log warn + return nil (never surface to caller)
//   - not signed in → return nil
//
// OSS-first: this type lives in core/rpc/ (allowed to import core/fleet).
// core/rpc/views/onboarding/ only holds the ProgressSyncer interface.
type onboardingProgressSyncerAdapter struct {
	client *fleet.Client
}

// SyncProgress implements onboardingview.ProgressSyncer.
// Spawns a goroutine so the caller returns immediately. The goroutine is
// best-effort and never panics.
func (a *onboardingProgressSyncerAdapter) SyncProgress(ctx context.Context, step onboardingview.ProgressStep, signedIn bool) error {
	if a.client == nil || a.client.IsNop() {
		logging.L().Debug("onboarding.progress_sync.skipped", "reason", "fleet_disabled_or_nil")
		return nil
	}
	if !signedIn {
		logging.L().Debug("onboarding.progress_sync.skipped", "reason", "not_signed_in", "step", string(step))
		return nil
	}

	// Build the partial update (only set the field matching the step).
	update := progressStepToWire(step)
	if update == (fleet.OnboardingStateWire{}) {
		// Unknown step — no-op.
		return nil
	}
	update.Schema = 1

	// Non-blocking: network call goes off in a goroutine.
	go func() {
		if err := a.client.PatchOnboardingState(context.Background(), update); err != nil {
			// ErrNotSignedIn + ErrFleetDisabled are expected in degraded paths.
			logging.L().Warn("onboarding.progress_sync.failed",
				"step", string(step),
				"err", err.Error(),
			)
		} else {
			logging.L().Info("onboarding.progress_sync.ok", "step", string(step))
		}
	}()
	return nil
}

// progressStepToWire converts a ProgressStep to the matching fleet wire
// partial-update. Returns zero value for unknown steps.
func progressStepToWire(step onboardingview.ProgressStep) fleet.OnboardingStateWire {
	switch step {
	case onboardingview.ProgressStepProviderConfigured:
		return fleet.OnboardingStateWire{ProviderConfigured: true}
	case onboardingview.ProgressStepAccountConnected:
		return fleet.OnboardingStateWire{AccountConnected: true}
	case onboardingview.ProgressStepBootstrapRun:
		return fleet.OnboardingStateWire{BootstrapRun: true}
	case onboardingview.ProgressStepGuidedActionShown:
		return fleet.OnboardingStateWire{GuidedActionShown: true}
	default:
		return fleet.OnboardingStateWire{}
	}
}

// ---- FleetStateReader adapter (WP04, 01NWEL01) ------------------------------

// onboardingFleetStateReaderAdapter implements onboardingview.FleetStateReader.
// Reads GET /api/v1/me/onboarding to warm Path A on Begin.
//
// Graceful-on-disabled contract:
//   - nil/nop client → return zero hint, nil
//   - fleet.Disabled() → return zero hint, nil
//   - network error or not signed in → return zero hint + error (caller logs + ignores)
//
// OSS-first: same bridge-file rule as ProgressSyncerAdapter.
type onboardingFleetStateReaderAdapter struct {
	client *fleet.Client
}

// ReadFleetState implements onboardingview.FleetStateReader.
func (a *onboardingFleetStateReaderAdapter) ReadFleetState(ctx context.Context) (onboardingview.FleetOnboardingHint, error) {
	if a.client == nil || a.client.IsNop() {
		return onboardingview.FleetOnboardingHint{}, nil
	}
	state, err := a.client.GetOnboardingState(ctx)
	if err != nil {
		// Return the error so the caller (Begin) can decide to log and ignore.
		return onboardingview.FleetOnboardingHint{}, err
	}
	return onboardingview.FleetOnboardingHint{
		AlreadyConnected:        state.AccountConnected,
		AlreadyHarnessInstalled: state.HarnessInstalled,
	}, nil
}

// ---- BootstrapRunner adapter (WP06, context-bootstrap) ----------------------

// onboardingBootstrapRunnerAdapter implements onboardingview.BootstrapRunner by
// delegating to the context-bootstrap orchestration API. It lives here (in
// core/rpc) because it bridges the fleet-free onboarding view to the engine +
// fleet-backed adapters. A nil api makes RunBootstrap a no-op error-free call.
//
// OSS-first: core/rpc/views/onboarding only holds the BootstrapRunner interface.
type onboardingBootstrapRunnerAdapter struct {
	api ContextBootstrapAPI
}

// RunBootstrap implements onboardingview.BootstrapRunner. It starts a run over
// the consented sources and blocks until StartRun returns (StartRun runs the
// engine synchronously and finalises the fleet run).
func (a onboardingBootstrapRunnerAdapter) RunBootstrap(ctx context.Context, consentedSources []string) (string, error) {
	if a.api == nil {
		return "", nil
	}
	res, err := a.api.StartRun(ctx, StartBootstrapRunRequest{ConsentedSources: consentedSources})
	if err != nil {
		return "", err
	}
	return res.RunID, nil
}

// ErrOnboardingNotWired is returned when the session manager is unavailable.
var ErrOnboardingNotWired = errors.New("onboarding: session manager not wired (nil chassis)")
