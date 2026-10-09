package chat

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/tokenizer"
)

// StreamClosedErrorKindRequestTooLarge marks a terminal close caused by a
// provider context-length rejection that the CONVERSATION did not cause:
// the session's history is empty or a small fraction of the model's
// window, so the overflow is the request the harness built around it —
// tool definitions plus the system prompt (dogfood 2026-10-08 round 2:
// a brand-new scheduled-chat session with one short user message hit a
// 131k model with ~220k tokens of tool schemas and was told "session has
// hit its context window", blaming a conversation that did not exist).
//
// Compaction cannot fix this — there is nothing to summarise — so the
// runner neither attempts overflow recovery nor reports
// StreamClosedErrorKindSessionFull for it.
const StreamClosedErrorKindRequestTooLarge = "request_too_large"

// requestShapedHistoryFraction is the history share of the model window
// below which an overflow is attributed to the request shape rather
// than to the conversation. A history under a quarter of the window
// cannot plausibly be what pushed a request over it; compacting it
// would free at most that quarter.
const requestShapedHistoryFraction = 0.25

// ErrRequestTooLarge is the typed form of the request-too-large verdict.
// Window is 0 when neither the provider's message nor the capability
// table named the model's context window.
type ErrRequestTooLarge struct {
	Model         string
	Window        int
	HistoryTokens int
}

func (e *ErrRequestTooLarge) Error() string {
	model := e.Model
	if model == "" {
		model = "this model"
	}
	if e.Window > 0 {
		return fmt.Sprintf("Request too large for %s (%d-token window): tool definitions and context alone exceed it — choose a larger model or disable tools", model, e.Window)
	}
	return fmt.Sprintf("Request too large for %s: tool definitions and context alone exceed its context window — choose a larger model or disable tools", model)
}

// providerWindowRe extracts the context window from the provider's own
// rejection text ("This endpoint's maximum context length is 131072
// tokens", "maximum context length is 128,000 tokens").
var providerWindowRe = regexp.MustCompile(`(?i)(?:maximum context length|context window|context length)[^0-9]{0,40}([0-9][0-9,]{2,})`)

// providerWindowFromError returns the window the provider named in a
// context-length rejection, or 0.
func providerWindowFromError(err error) int {
	if err == nil {
		return 0
	}
	m := providerWindowRe.FindStringSubmatch(err.Error())
	if len(m) < 2 {
		return 0
	}
	n, perr := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
	if perr != nil || n <= 0 {
		return 0
	}
	return n
}

// classifyRequestTooLarge decides whether a context-overflow error was
// caused by the request the harness built rather than by the session's
// history. It returns nil when the history is large enough that
// compaction is the right remedy (the ErrSessionFull path keeps that
// case), or when it cannot read the history at all (fail toward the
// pre-existing behaviour).
//
// model is the model the run actually used (the override, else the
// profile's default model); profileID/modelOverride key the capability
// lookup exactly as the compact node keys it.
func (r *ChatRunner) classifyRequestTooLarge(ctx context.Context, sessionID, profileID, modelOverride string, overflowErr error) *ErrRequestTooLarge {
	if r.cfg.History == nil {
		return nil
	}
	msgs, err := r.cfg.History.History(ctx, sessionID, 0)
	if err != nil {
		return nil
	}
	historyTokens := countHistoryTokens(msgs)

	model := r.resolveRunModel(profileID, modelOverride)
	window := providerWindowFromError(overflowErr)
	if window <= 0 && r.cfg.Compaction != nil && r.cfg.Compaction.MaxContextTokens != nil {
		if w, ok := r.cfg.Compaction.MaxContextTokens(compaction.ProviderProfileRef{ProviderID: profileID, ModelID: model}); ok && w > 0 {
			window = w
		}
	}

	shaped := false
	switch {
	case window > 0:
		shaped = float64(historyTokens) < float64(window)*requestShapedHistoryFraction
	default:
		// Unknown window: only the unambiguous case — nothing but the
		// turn's own user message (or nothing at all) in history.
		shaped = countNonSystem(msgs) <= 1
	}
	if !shaped {
		return nil
	}
	return &ErrRequestTooLarge{Model: model, Window: window, HistoryTokens: historyTokens}
}

// resolveRunModel names the model a run was dispatched with
// (corellm.ProviderProfile.DispatchModel — the registry's own rule).
func (r *ChatRunner) resolveRunModel(profileID, modelOverride string) string {
	if modelOverride != "" {
		return modelOverride
	}
	if r.cfg.Registry == nil || profileID == "" {
		return ""
	}
	prof, err := r.cfg.Registry.Profile(profileID)
	if err != nil {
		return ""
	}
	return prof.DispatchModel("")
}

func countHistoryTokens(msgs []coreag.Message) int {
	out := make([]tokenizer.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, tokenizer.Message{Role: m.Role, Content: m.Content})
	}
	return tokenizer.CountRequestTokens("", out)
}

func countNonSystem(msgs []coreag.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role != "system" {
			n++
		}
	}
	return n
}

// requestTooLargeFailure is the RunFailure recorded for the verdict.
func requestTooLargeFailure(providerKind string, e *ErrRequestTooLarge) corellm.RunFailure {
	return corellm.RunFailure{
		Class:    corellm.FailureUserActionable,
		Code:     StreamClosedErrorKindRequestTooLarge,
		Provider: providerKind,
		Summary:  "The request is larger than the model's context window",
		Message:  e.Error(),
	}
}
