package chat

import (
	"context"
	"errors"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/compactionpolicy"
	"github.com/kameas-ai/kenaz-harness/core/llm/tokenizer"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// session_compaction.go is where the five-tier compaction dial lives
// after agentgraph-total-convergence-01PMGX01 WP08.
//
// WHAT MOVED, AND WHY THE MOVE IS THE POINT. Until WP08 this logic ran
// a pre-kernel pass on the ChatRunner, invoked from
// StartStream between persisting the user turn and building the Env.
// That made the harness ship two compaction systems — this one, which
// rewrites *persisted* session history through core/agentgraph/compaction's
// Engine, and FR-041's Compactor pipeline, which shrinks *in-memory*
// message slices inside the kernel. The two could not see each other,
// so the only way to stop them double-compacting a turn was a
// suppression flag on the Env, which this WP also deletes. Neither
// symbol is named here: spec §6 I4 grep-forbids both, and a comment is
// a non-test .go line like any other.
//
// The persisted-vs-in-memory distinction was real, and the resolution
// is the one spec §4.4 names: make history loading a graph concern.
// `history_read` already loads the conversation as a node. Put a
// `compact` node immediately after it and both compactions become the
// same operation on the same representation — the messages that node
// was handed. The dial is now an ordinary FR-041 strategy
// (StrategySessionRewrite) reached from an ordinary node, and there is
// exactly one compaction entry point on the chat path.
//
// WHAT DID NOT MOVE. The tier semantics are carried over decision for
// decision: the same trigger arithmetic, the same summarize fractions,
// the same rolling-summary path for `maximal`, the same graceful
// degrade to the aggressive numerics when the compaction model is too
// small, the same swallow-and-continue on a transient engine error, and
// the same compaction.ErrSessionFull on the two paths where the user is
// honestly out of room. compaction_dial_golden_test.go (WP07) pins all
// of it byte-for-byte and is driven through this strategy.
//
// ONE ARITHMETIC CHANGE, DELIBERATE. The old pre-send pass counted
// `loadHistory() + userMessage`, but StartStream had already persisted
// the user turn before calling it — so the new user message was counted
// twice, and the trigger fired marginally early. The node is handed the
// history slice that `history_read` produced, which contains the user
// turn exactly once, and this strategy counts what it is given. One
// message's worth of tokens, in the correct direction.

// sessionRewriteStrategy implements compaction.Compactor for
// compaction.StrategySessionRewrite: it rewrites the persisted history of
// one session using core/agentgraph/compaction's SessionEngine.
//
// It is bound per run (Pipeline.Bind) rather than registered once,
// because it needs run identity — which session, and which model to
// spend the summarisation call on. Everything else about it is an
// ordinary strategy: the pipeline resolves the config, decides the site
// is enabled, and dispatches.
type sessionRewriteStrategy struct {
	deps *CompactionDeps
	// history re-reads the session after a rewrite so the node's
	// downstream ports carry the post-compaction transcript. nil means
	// the caller cannot re-read, and the input slice is returned
	// unchanged (the engine still did its work; the kernel's own
	// env.History re-read picks it up on the next node that asks).
	history SessionMessageReader
	// profileID + modelOverride are the run's active chat model. They
	// are the fallback compaction model and the key the capability
	// lookup answers the context-window question for — the same two
	// values StartStream used to hand the pre-send pass.
	profileID     string
	modelOverride string
}

// Strategy reports the strategy name.
func (s *sessionRewriteStrategy) Strategy() compaction.Strategy {
	return compaction.StrategySessionRewrite
}

// Compact runs one dial pass over the session's persisted history.
//
// Returns the input slice unchanged (never an error) on every path
// where compaction is unavailable or not indicated; returns
// compaction.ErrSessionFull on the two paths where the user is out of
// context and nothing can be done about it. Any other engine error is
// logged and swallowed — a provider hiccup during compaction must not
// take down a chat turn that is not yet over cap.
func (s *sessionRewriteStrategy) Compact(ctx context.Context, in compaction.ContextSlice, _ compaction.CompactOpts) (compaction.CompactedContext, error) {
	passthrough := func() compaction.CompactedContext {
		return compaction.CompactedContext{
			Messages:    append([]coreag.Message(nil), in.Messages...),
			TokensAfter: in.CurrentTokens,
			Strategy:    s.Strategy(),
		}
	}

	deps := s.deps
	if deps == nil || deps.Engine == nil {
		// Compaction not wired — the test-fixture path, and the boot
		// path on a chassis where the engine failed to construct.
		return passthrough(), nil
	}
	// HARNESS_COMPACTION=off short-circuits the dial entirely so the
	// harness can be A/B tested without a restart.
	if compactionDisabledByEnv() {
		return passthrough(), nil
	}
	if deps.Aggressiveness == nil {
		// Defensive: a chassis that wired the engine but not the
		// settings reader cannot make a tier decision.
		return passthrough(), nil
	}
	sessionID := in.SessionID
	if sessionID == "" {
		// A session-scoped strategy with no session. Skip rather than
		// guess: there is no history to rewrite.
		return passthrough(), nil
	}

	tier := compactionpolicy.Tier(deps.Aggressiveness())

	// current is the size of the transcript the node was handed. The
	// node sits directly downstream of history_read, so this is the
	// persisted conversation including the turn the user just sent.
	current := tokenizeMessages(in.Messages)

	// reload re-reads the session after a rewrite so the compacted form
	// flows out of the node rather than only being visible to whatever
	// asks env.History later.
	reload := func() compaction.CompactedContext {
		if s.history == nil {
			return passthrough()
		}
		msgs, herr := s.history.History(ctx, sessionID, 0)
		if herr != nil {
			logging.L().Warn("chat.compaction.history_reload_failed",
				"session_id", sessionID, "err", herr.Error())
			return passthrough()
		}
		return compaction.CompactedContext{
			Messages:    msgs,
			TokensAfter: tokenizeMessages(msgs),
			Strategy:    s.Strategy(),
			BytesSaved:  bytesOfMessages(in.Messages) - bytesOfMessages(msgs),
		}
	}

	// pickModel: the configured compaction model wins; otherwise the
	// summarisation runs against the same model the chat is using.
	pickModel := func() compaction.ProviderProfileRef {
		if deps.CompactionModel != nil {
			if ref, ok := deps.CompactionModel(); ok && (ref.ProviderID != "" || ref.ModelID != "") {
				return ref
			}
		}
		return compaction.ProviderProfileRef{ProviderID: s.profileID, ModelID: s.modelOverride}
	}
	activeModel := compaction.ProviderProfileRef{ProviderID: s.profileID, ModelID: s.modelOverride}

	switch tier.Mode {
	case compactionpolicy.ModeNone:
		// Off tier: honest "session full" if we are already over cap,
		// otherwise proceed untouched. The user chose not to compact;
		// silently truncating would be worse than saying so.
		if deps.MaxContextTokens == nil {
			return passthrough(), nil
		}
		if capTokens, ok := deps.MaxContextTokens(activeModel); ok && capTokens > 0 && current >= capTokens {
			logging.L().Warn("chat.compaction.session_full_off",
				"session_id", sessionID, "tokens", current, "cap", capTokens)
			return passthrough(), compaction.ErrSessionFull
		}
		return passthrough(), nil

	case compactionpolicy.ModeThreshold:
		if deps.MaxContextTokens == nil {
			return passthrough(), nil
		}
		capTokens, ok := deps.MaxContextTokens(activeModel)
		if !ok || capTokens <= 0 {
			// Unknown model cap — skip the trigger check; the
			// provider's own gate handles any over-cap span.
			return passthrough(), nil
		}
		if float64(current)/float64(capTokens) < tier.TriggerPct {
			return passthrough(), nil
		}
		// Trigger. One synchronous Compact pass.
		if _, cerr := deps.Engine.Compact(ctx, sessionID, pickModel(), tier.SummarizePct); cerr != nil {
			var tooSmall *compaction.ErrCompactionModelTooSmall
			if errors.As(cerr, &tooSmall) {
				// The compaction model cannot hold the span we need to
				// summarise. The user is out of runway; say so.
				logging.L().Warn("chat.compaction.threshold_model_too_small",
					"session_id", sessionID,
					"needs_tokens", tooSmall.NeedsTokens,
					"model_max_tokens", tooSmall.ModelMaxTokens)
				return passthrough(), compaction.ErrSessionFull
			}
			// Anything else: log and proceed uncompacted. Partial state
			// is safe because Compact is transactional — either the
			// summary row exists or the originals were never touched.
			logging.L().Warn("chat.compaction.threshold_failed",
				"session_id", sessionID, "err", cerr.Error())
			return passthrough(), nil
		}
		return reload(), nil

	case compactionpolicy.ModeRolling:
		// Maximal tier: roll every turn, unconditionally. There is no
		// trigger check here and there never was — the tier's meaning
		// is "keep only the recent window plus a rolling summary".
		recentWindow := 4
		if deps.RecentWindow != nil {
			recentWindow = deps.RecentWindow()
		}
		_, cerr := deps.Engine.RollingSummarize(ctx, sessionID, pickModel(), recentWindow)
		if cerr == nil {
			return reload(), nil
		}
		var tooSmall *compaction.ErrCompactionModelTooSmall
		if errors.As(cerr, &tooSmall) {
			// Graceful degrade: treat this turn as the aggressive tier.
			// The audit breadcrumb was already emitted inside
			// RollingSummarize, so dashboards can see the fallback.
			logging.L().Warn("chat.compaction.maximal_too_small_fallback_aggressive",
				"session_id", sessionID,
				"needs_tokens", tooSmall.NeedsTokens,
				"model_max_tokens", tooSmall.ModelMaxTokens)
			fallback := compactionpolicy.Tier(compactionpolicy.AggressivenessAggressive)
			if _, fcerr := deps.Engine.Compact(ctx, sessionID, pickModel(), fallback.SummarizePct); fcerr != nil {
				var fts *compaction.ErrCompactionModelTooSmall
				if errors.As(fcerr, &fts) {
					return passthrough(), compaction.ErrSessionFull
				}
				logging.L().Warn("chat.compaction.maximal_fallback_failed",
					"session_id", sessionID, "err", fcerr.Error())
				return passthrough(), nil
			}
			return reload(), nil
		}
		// Other rolling errors: log and proceed uncompacted. A provider
		// hiccup should not block a chat that is not yet over cap.
		logging.L().Warn("chat.compaction.rolling_failed",
			"session_id", sessionID, "err", cerr.Error())
		return passthrough(), nil
	}
	return passthrough(), nil
}

// tokenizeMessages counts a message slice the way the pre-send pass
// counted its (history, userMessage) pair: through the shared
// tokenizer, with an empty system-prompt slot. The framing overhead the
// tokenizer adds covers the slot regardless, and the chat runner does
// not know what system prompt the kernel will inject — that is a
// graph-side concern.
func tokenizeMessages(msgs []coreag.Message) int {
	out := make([]tokenizer.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, tokenizer.Message{Role: m.Role, Content: m.Content})
	}
	return tokenizer.CountRequestTokens("", out)
}

// bytesOfMessages totals UTF-8 content bytes for the strategy's
// BytesSaved telemetry field. Byte length, not rune count, is the right
// measure here: it is what the event payload reports and what the
// pipeline's own bytesOf uses, so the two agree.
func bytesOfMessages(msgs []coreag.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content) + len(m.Name)
	}
	return n
}

// newSessionRewriteStrategy binds the dial to one run.
func (r *ChatRunner) newSessionRewriteStrategy(profileID, modelOverride string) *sessionRewriteStrategy {
	return &sessionRewriteStrategy{
		deps:          r.cfg.Compaction,
		history:       r.cfg.History,
		profileID:     profileID,
		modelOverride: modelOverride,
	}
}

// bindCompactor produces the run's Compactor: the shared FR-041
// pipeline with this run's session-rewrite strategy bound onto it.
//
// Returns nil when no pipeline was supplied, which leaves env.Compactor
// nil and makes the `compact` node a documented no-op passthrough (see
// compactExecutor). That is the correct reading for a chassis with no
// compaction wired and for the many tests that construct a bare
// ChatRunner: nothing to compact with means nothing to compact.
func (r *ChatRunner) bindCompactor(profileID, modelOverride string) coreag.Compactor {
	if r.cfg.CompactionPipeline == nil {
		return nil
	}
	return r.cfg.CompactionPipeline.Bind(r.newSessionRewriteStrategy(profileID, modelOverride))
}
