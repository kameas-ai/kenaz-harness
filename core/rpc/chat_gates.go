package rpc

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/rpc/middleware"
	sessionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
)

// chat_gates.go holds the pre-dispatch gating that MUST be identical on
// every transport that can start, stop, or feed a chat turn.
//
// The harness has two such transports:
//
//   - the desktop Wails bindings (core/rpc/bindings.go), and
//   - the served-mode HTTP/WS server (core/serve), which a browser inside
//     a Kenaz workbench talks to.
//
// Before these helpers existed the gating lived inline in the Wails
// binding bodies, so a second transport had to re-implement it — and a
// transport that forgot a gate would happily dispatch a turn the desktop
// app refuses. Both transports now call the same function, so the two can
// only drift by deleting a call site rather than by simply forgetting one.
//
// These are free functions over [HarnessAPI] rather than methods on *API
// so they can be used against the interface (which the binding layer and
// its test fakes hold) without widening it.

// StartLLMStream opens a streaming completion for sessionID against
// profileID, applying the same pre-dispatch gates the desktop
// LLM_StartStream binding applies:
//
//   - fleet emergency lockdown (fleet-emergency-lockdown-01NDFSEX12 WP04)
//
// modelOverride selects a non-default model from the profile's authorised
// set; empty means "use the profile default".
//
// The returned subscription id correlates the `llm:stream-chunk` and
// `llm:stream-closed` events the caller must already be subscribed to.
func StartLLMStream(ctx context.Context, api HarnessAPI, profileID, sessionID, modelOverride string) (string, error) {
	if err := middleware.CheckLockdown(); err != nil {
		return "", err
	}
	return api.LLMConnector().StartStream(ctx, profileID, sessionID, modelOverride)
}

// StopLLMStream terminates the subscription opened by [StartLLMStream].
//
// Deliberately ungated: stopping is always allowed, including during a
// lockdown — a user must never be prevented from cancelling a turn that
// is already in flight.
func StopLLMStream(ctx context.Context, api HarnessAPI, subID string) error {
	return api.LLMConnector().StopStream(ctx, subID)
}

// SendMessageBlocks persists a multimodal user turn, applying the same
// lockdown gate the desktop Sessions_SendMessageWithBlocks binding applies.
func SendMessageBlocks(ctx context.Context, api HarnessAPI, sessionID string, blocks []sessionsview.ContentBlock) (sessionsview.Message, error) {
	if err := middleware.CheckLockdown(); err != nil {
		return sessionsview.Message{}, err
	}
	return api.Sessions().SendMessageWithBlocks(ctx, sessionID, blocks)
}
