// onboarding_wiring.go wires the onboarding view (core/rpc/views/onboarding)
// with concrete adapters over the production managers.
//
// Mission: harness-self-mcp-onboarding-01KQ8TDU WP08 (tail polish, v0.5.4).
// Extended: harness-onboarding-01NHON01 WP01/WP03/WP04/WP07.
//
// The adapter types here satisfy the narrow interfaces declared in
// core/rpc/views/onboarding/impl.go so the onboarding view can be
// constructed without cyclic imports into core/rpc/api.go.
//
// Completion state is now persisted via Settings.FirstRunOnboardingCompleted
// (added in WP01). The AccountStepAvailableChecker gracefully degrades to
// "unavailable" when fleet is disabled or not wired.
package rpc

import (
	"context"
	"errors"
	"os"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
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
// DEFERRED FLEET INTEGRATION (WP03): when the fleet sign-in endpoint ships
// (01NWEL01), update this adapter to also check whether the fleet client
// is reachable (a single HEAD probe at startup). Until then the env-var
// gate is sufficient — the account step is always skippable so a false
// positive just shows a CTA that silently degrades in the FSM.
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

// ErrOnboardingNotWired is returned when the session manager is unavailable.
var ErrOnboardingNotWired = errors.New("onboarding: session manager not wired (nil chassis)")
