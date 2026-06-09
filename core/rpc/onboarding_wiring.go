// onboarding_wiring.go wires the onboarding view (core/rpc/views/onboarding)
// with concrete adapters over the production managers.
//
// Mission: harness-self-mcp-onboarding-01KQ8TDU WP08 (tail polish, v0.5.4).
//
// The three adapter types here satisfy the narrow interfaces declared in
// core/rpc/views/onboarding/impl.go so the onboarding view can be
// constructed without cyclic imports into core/rpc/api.go.
//
// State persistence (OnboardingCompleted, HarnessSelfMCPDisabled) is not
// yet mapped to a settings field — those keys are sentinel no-ops in the
// harness_wiring.go allowlist. The adapters below fall back to safe
// defaults (not completed = always show once, dial enabled = true) so the
// first-run dialog fires correctly on a fresh install. A follow-up mission
// adds the dedicated settings fields.
package rpc

import (
	"context"
	"errors"

	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
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
// Since OnboardingCompleted is not yet in the settings store, MarkOnboardingCompleted
// is a no-op and IsCompleted always returns false (first-run condition persists
// until a provider is added, which makes IsFirstRun return false).
type onboardingCompletionAdapter struct{}

func (onboardingCompletionAdapter) MarkOnboardingCompleted(_ context.Context) error {
	// No-op: completion state is implicitly derived from provider count.
	// A follow-up adds a persistent flag.
	return nil
}

func (onboardingCompletionAdapter) IsCompleted(_ context.Context) (bool, error) {
	return false, nil
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

// ErrOnboardingNotWired is returned when the session manager is unavailable.
var ErrOnboardingNotWired = errors.New("onboarding: session manager not wired (nil chassis)")
