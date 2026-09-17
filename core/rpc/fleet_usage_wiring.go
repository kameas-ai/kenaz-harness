package rpc

// fleet_usage_wiring.go — adapters from the runtime's usage seams to fleet's
// consent-gated ConversationTracker.
//
// The kernel (core/agentgraph) and the chat runner (core/rpc/views/agentgraph/
// chat) may not import core/fleet (OSS-first boundary,
// scripts/ci/check-no-fleet-imports.sh). They expose narrow observer
// interfaces instead; this package — which is allowed to import fleet —
// implements them.
//
// Every adapter resolves the tracker through settings.API.FleetUsageTracker on
// EACH call rather than capturing it. The fleet pipeline is wired late in
// rpc.New, after the graph and chat stacks these adapters are handed to, so a
// captured pointer would always be nil. A nil tracker (fleet disabled, OSS
// build, test chassis) makes every method a no-op.
//
// Nothing here decides whether anything is SENT. That is the tracker's
// emitter: effective consent, per-class opt-ins, the compiled ceiling, and an
// active account-attributed pipeline all have to agree first.

import (
	"context"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// turnUsageObserver converts a possibly-nil *fleetUsageObserver into the chat
// seam's interface without producing a typed-nil interface value (which would
// defeat the runner's `!= nil` guard and panic on first use).
func turnUsageObserver(o *fleetUsageObserver) chat.TurnUsageObserver {
	if o == nil {
		return nil
	}
	return o
}

// fleetUsageObserver implements agentgraph.ToolUsageObserver and
// chat.TurnUsageObserver over the settings-owned tracker.
type fleetUsageObserver struct {
	settings *settings.API
}

// newFleetUsageObserver returns nil when settingsImpl is nil so callers can
// assign the result to an interface field only after a nil check (never a
// typed-nil interface).
func newFleetUsageObserver(settingsImpl *settings.API) *fleetUsageObserver {
	if settingsImpl == nil {
		return nil
	}
	return &fleetUsageObserver{settings: settingsImpl}
}

func (o *fleetUsageObserver) tracker() *corefleet.ConversationTracker {
	if o == nil || o.settings == nil {
		return nil
	}
	return o.settings.FleetUsageTracker()
}

// ToolInvoked implements agentgraph.ToolUsageObserver.
func (o *fleetUsageObserver) ToolInvoked(ctx context.Context, sessionID, toolName string, latency time.Duration, success bool) {
	o.tracker().ToolInvoked(ctx, sessionID, toolName, latency, success)
}

// TurnStarted implements chat.TurnUsageObserver.
func (o *fleetUsageObserver) TurnStarted(ctx context.Context, sessionID, providerKind string) {
	o.tracker().TurnStarted(ctx, sessionID, providerKind)
}

// TurnFailed implements chat.TurnUsageObserver. failureKind is the chat
// runner's closed classifier output; ProjectErrorCategory maps it (and
// anything unexpected) onto fleet's closed enum.
func (o *fleetUsageObserver) TurnFailed(ctx context.Context, sessionID, failureKind string, recoverable bool) {
	o.tracker().TurnFailed(ctx, sessionID, corefleet.ProjectErrorCategory(failureKind), recoverable)
}

// LLMResponse feeds one model response's token usage and cost into the
// session's open conversation segment. Called from the chat UsageHook.
func (o *fleetUsageObserver) LLMResponse(ctx context.Context, sessionID string, tokenIn, tokenOut int, costUSD float64) {
	o.tracker().LLMResponse(ctx, sessionID, int64(tokenIn), int64(tokenOut), costUSD)
}

// AttributeTo rolls a sub-agent child session's usage into its parent's
// conversation. Session ids stay local; see ConversationTracker.AttributeTo.
func (o *fleetUsageObserver) AttributeTo(childSessionID, parentSessionID string) {
	o.tracker().AttributeTo(childSessionID, parentSessionID)
}
