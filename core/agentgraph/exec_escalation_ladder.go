package agentgraph

import (
	"context"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/elicitation"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// ---- EscalationLadderNode (autonomy-recovery-runtime-01PMDL03 WP04) ----
//
// Before this node kind, the four recovery rungs the architecture doc
// describes — retry(same model) -> escalate(stronger model) ->
// replan -> ask(human) — existed only as separate, independently-wired
// node kinds (RetryNode, EscalateNode, PlannerNode, AskNode). Nothing
// chained them; a graph author had to hand-wire the progression
// node-by-node, and the one dynamic hand-off (ReviewNode's
// should_retry/retry_target) was inert until WP01 gave the kernel a
// real backtrack primitive.
//
// escalationLadderExecutor is the runtime-orchestrated chain: a graph
// wires a single EscalationLadder node downstream of a
// ReviewFail/ReflectFail/doom_loop_detected signal, and each time it
// fires (once per fresh failure of a.UpstreamNode) it advances exactly
// one rung of the ladder:
//
//  1. retry   — request a kernel backtrack to a.UpstreamNode, up to
//     a.MaxRetries times (default 1). Same model, same approach, just
//     re-fired with the accumulated FailureAnnotation context (WP01).
//  2. escalate — re-run the failing draft through a.TargetModel (a
//     stronger model). Gated by a single **run-global** escalation
//     flag (generalising EscalateAttrs.OneEscalationOnly from
//     per-node to per-run): once any EscalationLadder node in this run
//     has escalated, every ladder skips straight past this rung.
//  3. replan  — ask a.PlannerModel (or the run default) for a revised
//     plan that explicitly avoids the rejected approach.
//  4. ask     — pose a.AskQuestion (or a generic default) to a human
//     via the AskBus, pausing the run until answered.
//
// Rung transitions persist in RunState under a per-node key so the
// ladder resumes at the right rung across kernel re-fires; the
// escalate-used flag is a single shared key so it is genuinely global
// across every EscalationLadder node in the run. Every rung — and its
// sibling side-effect event (EventBacktrackFired via the kernel,
// EventEscalateTriggered, EventPlanCreated, EventAskPending/
// EventAskAnswered) — also gets an EventLadderRung entry so the
// EventLog carries a single audit trail of "which rung, which attempt"
// that ties the sibling events together.
type escalationLadderExecutor struct{}

func (escalationLadderExecutor) Kind() NodeKind { return NodeKindEscalationLadder }

// ladderStateKey is the per-node RunState key tracking which rung an
// EscalationLadder node is on and how many same-model retries it has
// spent. Keyed by node ID so multiple ladder nodes in one graph track
// independent rung progress.
func ladderStateKey(nodeID string) string { return "__ladder__" + nodeID }

// ladderGlobalEscalatedKey is intentionally NOT keyed by node ID: the
// spec calls for "one global escalation counter" generalising
// EscalateAttrs.OneEscalationOnly from a per-node flag to a per-run
// one. Every EscalationLadder node in a run shares this single key.
const ladderGlobalEscalatedKey = "__ladder_escalated_global__"

// Ladder rung indices, persisted as plain ints in RunState outputs
// (PortValues values must round-trip through the same loose `any`
// carrier every other control executor's iteration state uses).
const (
	ladderRungRetry = iota
	ladderRungEscalate
	ladderRungReplan
	ladderRungAsk
	ladderRungExhausted
)

func (escalationLadderExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(EscalationLadderAttrs)
	if !ok {
		return res, fmt.Errorf("escalation_ladder: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	if a.TargetModel == "" {
		return res, fmt.Errorf("escalation_ladder: node %q: target_model is required", node.ID)
	}
	if a.UpstreamNode == "" {
		return res, fmt.Errorf("escalation_ladder: node %q: upstream_node is required", node.ID)
	}
	maxRetries := a.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 1
	}

	stateKey := ladderStateKey(node.ID)
	st := env.State.Outputs(stateKey)
	rung, _ := st["rung"].(int)
	retryAttempts, _ := st["retry_attempts"].(int)

	draft := ladderDraftText(env, inputs, a.UpstreamNode)

	if rung == ladderRungRetry {
		retryAttempts++
		_ = res.Events.AppendKind(env.RunID, node.ID, EventLadderRung, map[string]any{
			"rung": "retry", "attempt": retryAttempts, "max_attempts": maxRetries,
		})
		if retryAttempts <= maxRetries {
			env.State.SetOutputs(stateKey, PortValues{"rung": ladderRungRetry, "retry_attempts": retryAttempts})
			res.Backtrack = &BacktrackRequest{
				TargetNode:       a.UpstreamNode,
				Reason:           fmt.Sprintf("escalation ladder: retry %d/%d", retryAttempts, maxRetries),
				RejectedApproach: draft,
			}
			return res, nil
		}
		// Retries exhausted this fire — advance straight into the
		// escalate rung rather than waiting for another external
		// re-fire; the escalation ladder owns exactly one action per
		// upstream failure, and "retries exhausted" is itself the
		// trigger for the next rung.
		rung = ladderRungEscalate
		env.State.SetOutputs(stateKey, PortValues{"rung": rung, "retry_attempts": retryAttempts})
	}

	if rung == ladderRungEscalate {
		// Claim the run's single shared escalation slot atomically
		// (RunState.CompareAndSetOnce), *before* doing the slow,
		// unlocked env.LLM.Generate call below. The previous
		// read-then-Generate-then-write sequence had a TOCTOU window
		// in which two concurrent EscalationLadder nodes could both
		// observe "not yet escalated" and both fire a real, costed
		// escalation call — this collapses the check-and-claim into
		// one atomic, mutex-guarded step.
		claimed := env.State.CompareAndSetOnce(ladderGlobalEscalatedKey)
		_ = res.Events.AppendKind(env.RunID, node.ID, EventLadderRung, map[string]any{
			"rung": "escalate", "already_used": !claimed,
		})
		if claimed {
			// Ground the rung (wiring-integrity-01PMAG04 WP00). The
			// ladder fires only after a failure, so graphBaseOf's
			// TaskState + FailureAnnotations block is populated and is
			// precisely the context this call needs. EscalationLadder
			// extends the *control* archetype, so there is no per-node
			// system_prompt attr to compose in — the graph base is the
			// whole grounding here.
			resp, err := env.LLM.Generate(ctx, LLMRequest{
				Model:        a.TargetModel,
				MaxTokens:    1024,
				SystemPrompt: composePrompt(resolvePromptTemplate(env, "", a.TargetModel), graphBaseOf(env)),
				Messages: []Message{
					{Role: "user", Content: "Re-do this with higher quality:\n\n" + draft},
				},
				FallbackChainId: a.FallbackChainId,
			})
			if err != nil {
				// The claim was spent on an attempt that never
				// actually escalated anything — release it so a
				// retry (by this node or another ladder node in the
				// run) can still use the run's one shared slot.
				env.State.ReleaseOnce(ladderGlobalEscalatedKey)
				return res, fmt.Errorf("escalation_ladder: node %q: escalate: %w", node.ID, err)
			}
			if env.Counters != nil {
				env.Counters.AddLLM(resp.TokensUsed)
				env.Counters.AddCost(resp.CostUSD)
			}
			env.State.SetOutputs(stateKey, PortValues{"rung": ladderRungReplan, "retry_attempts": retryAttempts})
			res.Outputs["result"] = []Message{{Role: "assistant", Content: resp.Content}}
			res.Outputs["rung"] = "escalate"
			// model_used dropped (wiring-integrity-01PMAG04 WP03, docs/wiring-audit.md
			// item 3c): the value is already carried on EventEscalateTriggered's
			// target_model field below, and the Outputs port had no reader.
			_ = res.Events.AppendKind(env.RunID, node.ID, EventEscalateTriggered, map[string]any{
				"target_model": a.TargetModel,
				"upstream":     a.UpstreamNode,
				"tokens":       resp.TokensUsed,
			})
			return res, nil
		}
		// This run's one global escalation is already spent — skip
		// straight to replan.
		rung = ladderRungReplan
		env.State.SetOutputs(stateKey, PortValues{"rung": rung, "retry_attempts": retryAttempts})
	}

	if rung == ladderRungReplan {
		plannerModel := a.PlannerModel
		if plannerModel == "" {
			plannerModel = "default"
		}
		prompt := "The previous approach failed and repeated retry/escalation did not resolve it:\n\n" +
			draft + "\n\nProduce a revised numbered-step plan that explicitly avoids the rejected approach."
		// Ground the replan rung (wiring-integrity-01PMAG04 WP00). The
		// prompt tells the model to "avoid the rejected approach" — the
		// record of what *was* rejected lives in graphBaseOf's
		// FailureAnnotations block, so without this the instruction
		// referenced context the model could not see.
		resp, err := env.LLM.Generate(ctx, LLMRequest{
			Model:        plannerModel,
			MaxTokens:    1024,
			SystemPrompt: composePrompt(resolvePromptTemplate(env, "", plannerModel), graphBaseOf(env)),
			Messages:     []Message{{Role: "user", Content: prompt}},
		})
		if err != nil {
			return res, fmt.Errorf("escalation_ladder: node %q: replan: %w", node.ID, err)
		}
		if env.Counters != nil {
			env.Counters.AddLLM(resp.TokensUsed)
			env.Counters.AddCost(resp.CostUSD)
		}
		env.State.SetOutputs(stateKey, PortValues{"rung": ladderRungAsk, "retry_attempts": retryAttempts})
		res.Outputs["result"] = []Message{{Role: "assistant", Content: resp.Content}}
		res.Outputs["rung"] = "replan"
		_ = res.Events.AppendKind(env.RunID, node.ID, EventLadderRung, map[string]any{
			"rung": "replan", "tokens": resp.TokensUsed,
		})
		_ = res.Events.AppendKind(env.RunID, node.ID, EventPlanCreated, map[string]any{
			"verbosity": "standard", "model": plannerModel, "tokens": resp.TokensUsed,
		})
		return res, nil
	}

	if rung == ladderRungAsk {
		// F7 (agentgraph-total-convergence-01PMGX01 WP11c; autonomy-knobs
		// review finding). The terminal rung asks a human, and at
		// askOnAmbiguity proceed/never the user has said they do not
		// want to be asked — the chat surface already withholds the ask
		// tool from the model on those settings. A ladder that pauses
		// the run with a question anyway honours the dial for the model
		// and ignores it for the runtime, which is the same dial
		// meaning two things.
		//
		// Under Withhold the ladder is EXHAUSTED instead: retry,
		// escalate and replan have all run, and what is left is a
		// question the user pre-declined. Recorded as a rung so the
		// EventLog shows the ladder reached its end rather than
		// silently stopping one rung short.
		if env.AskPolicy == AskPolicyWithhold {
			env.State.SetOutputs(stateKey, PortValues{"rung": ladderRungExhausted, "retry_attempts": retryAttempts})
			_ = res.Events.AppendKind(env.RunID, node.ID, EventLadderRung, map[string]any{
				"rung": "ask", "skipped": true, "reason": "ask_withheld",
			})
			logging.L().Info("agentgraph.escalation_ladder.ask_withheld",
				"run_id", env.RunID, "node_id", node.ID,
				"detail", "askOnAmbiguity withholds questions; the ladder ends at replan rather than pausing the run")
			res.Outputs["result"] = []Message{{Role: "assistant", Content: draft}}
			res.Outputs["rung"] = "exhausted"
			return res, nil
		}
		question := a.AskQuestion
		if question == "" {
			question = "Automatic recovery (retry, escalate, replan) did not resolve the failure at " +
				a.UpstreamNode + ". How should the run proceed?"
		}
		if ans, ok := env.Ask.LookupAnswer(ctx, env.RunID, node.ID); ok {
			env.State.SetOutputs(stateKey, PortValues{"rung": ladderRungExhausted, "retry_attempts": retryAttempts})
			text := ans.String()
			res.Outputs["result"] = []Message{{Role: "user", Content: text}}
			res.Outputs["rung"] = "ask"
			_ = res.Events.AppendKind(env.RunID, node.ID, EventAskAnswered, map[string]any{
				"answer_len": len(text),
			})
			_ = res.Events.AppendKind(env.RunID, node.ID, EventLadderRung, map[string]any{"rung": "ask"})
			return res, nil
		}
		if err := env.Ask.Pending(ctx, env.RunID, node.ID, elicitation.Question{Text: question}); err != nil {
			return res, fmt.Errorf("escalation_ladder: node %q: ask pending: %w", node.ID, err)
		}
		_ = res.Events.AppendKind(env.RunID, node.ID, EventAskPending, map[string]any{"question": question})
		_ = res.Events.AppendKind(env.RunID, node.ID, EventLadderRung, map[string]any{"rung": "ask"})
		res.Pause = true
		res.PauseReason = "escalation_ladder: " + question
		return res, nil
	}

	return res, fmt.Errorf("escalation_ladder: node %q: all rungs exhausted (retry, escalate, replan, ask)", node.ID)
}

// ladderDraftText resolves the failing content the ladder re-runs at
// the escalate/replan rungs: prefer the upstream node's last recorded
// assistant message (mirrors escalateExecutor's own lookup), then fall
// back to whatever landed on the `trigger` input port.
func ladderDraftText(env *Env, inputs PortValues, upstreamNode string) string {
	if upstreamNode != "" {
		out := env.State.Outputs(upstreamNode)
		if v, ok := out["assistant"]; ok {
			if m, ok := v.(Message); ok && m.Content != "" {
				return m.Content
			}
		}
	}
	if v, ok := inputs.GetString("trigger"); ok && v != "" {
		return v
	}
	if v, ok := inputs.Get("trigger"); ok {
		if msgs, ok := v.([]Message); ok && len(msgs) > 0 {
			return msgs[len(msgs)-1].Content
		}
	}
	return ""
}
