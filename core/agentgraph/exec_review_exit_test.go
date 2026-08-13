package agentgraph

import (
	"context"
	"strings"
	"testing"
)

// exec_review_exit_test.go — the `review` hardening that turns the kind
// from scaffolding into a verified exit
// (agentgraph-total-convergence-01PMGX01 WP11b; design in
// agentic-turn-routing-01PMAG01 §3.4, tasks WP06).

func reviewEnv(t *testing.T, llm LLMProvider) *Env {
	t.Helper()
	env := &Env{RunID: "run-review", SessionID: "s", LLM: llm, Graph: &Graph{SystemPrompt: "BASE"}}
	applyEnvDefaults(env)
	env.LLM = llm
	return env
}

func reviewNode(attrs ReviewAttrs) *Node {
	return &Node{ID: "exit_gate", Kind: NodeKindReview, Attrs: attrs}
}

// TestReview_PromptCarriesGoalAndCompletedSteps is the §3.4 item-1 pin.
// A reviewer handed only the draft is rating prose in a vacuum — it
// will happily approve a well-written answer to a question nobody
// asked. The verdict has to be against the user's actual request.
func TestReview_PromptCarriesGoalAndCompletedSteps(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: `{"verdict":"pass","reason":"looks right"}`}}}
	env := reviewEnv(t, llm)
	env.TaskState.SetGoal("Summarise the Q3 revenue report")
	env.TaskState.AddCompletedStep("read the report")
	env.TaskState.AddCompletedStep("extracted the revenue table")

	_, err := reviewExecutor{}.Execute(context.Background(), env, reviewNode(ReviewAttrs{
		UpstreamNode: "agent_loop", MaxIterations: 2,
	}), PortValues{"draft": "Q3 revenue was up 12%."})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	llm.mu.Lock()
	defer llm.mu.Unlock()
	if len(llm.calls) != 1 {
		t.Fatalf("Generate calls = %d, want 1", len(llm.calls))
	}
	prompt := llm.calls[0].Messages[len(llm.calls[0].Messages)-1].Content
	for _, want := range []string{
		"Summarise the Q3 revenue report",
		"read the report",
		"extracted the revenue table",
		"Q3 revenue was up 12%.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("review prompt is missing %q:\n%s", want, prompt)
		}
	}
}

// TestReview_EmptyTaskStateDegradesToTheDraftOnlyPrompt: a review node
// in a hand-built graph with no history seam is legitimate, so an empty
// TaskState must degrade rather than error or emit an empty "Goal:"
// stanza that reads as "the goal is nothing".
func TestReview_EmptyTaskStateDegradesToTheDraftOnlyPrompt(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "PASS"}}}
	env := reviewEnv(t, llm)

	if _, err := (reviewExecutor{}).Execute(context.Background(), env, reviewNode(ReviewAttrs{
		UpstreamNode: "up", MaxIterations: 2,
	}), PortValues{"draft": "the answer"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	llm.mu.Lock()
	defer llm.mu.Unlock()
	prompt := llm.calls[0].Messages[0].Content
	if strings.Contains(prompt, "The user's goal") {
		t.Errorf("empty TaskState still rendered a goal stanza:\n%s", prompt)
	}
	if !strings.Contains(prompt, "the answer") {
		t.Errorf("draft missing from the prompt:\n%s", prompt)
	}
}

// TestParseReviewVerdict is the §3.4 item-2 pin. The old rule was
// HasPrefix(upper(trim(resp)), "PASS"), so "Sure! PASS" scored a FAIL —
// a gate that punishes the model for being polite, burning retry budget
// on work that was already correct.
func TestParseReviewVerdict(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, want string
	}{
		{"structured pass", `{"verdict": "pass", "reason": "meets the goal"}`, "pass"},
		{"structured fail", `{"verdict": "fail", "reason": "half done"}`, "fail"},
		{"structured in a fenced block", "```json\n{\"verdict\":\"pass\"}\n```", "pass"},
		{"structured with preamble", `Here is my verdict: {"verdict":"pass","reason":"ok"}`, "pass"},
		{"bare token", `verdict: pass`, "pass"},
		{"legacy bare PASS", "PASS", "pass"},
		{"legacy bare FAIL", "FAIL — missing the summary", "fail"},
		{"polite preamble (the old bug)", "Sure! PASS — this satisfies the goal.", "pass"},
		{"heading preamble (the old bug)", "## Review\n\nPASS, looks complete", "pass"},
		{"both words, rejection wins", "This is not a PASS. FAIL: the table is missing.", "fail"},
		{"PASSAGE must not match PASS", "The PASSAGE is unclear, so I cannot approve.", "fail"},
		{"unreadable defaults to fail", "¯\\_(ツ)_/¯", "fail"},
		{"empty defaults to fail", "", "fail"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _ := parseReviewVerdict(tc.in)
			if got != tc.want {
				t.Errorf("parseReviewVerdict(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestReview_FailWithBudgetRewindsIntoTheLoop pins the rewind that
// makes the exit verified rather than advisory: FAIL emits
// should_retry/retry_target, which the kernel's resolveBacktrack
// already honors — no new kernel primitive (01PMAG01 §3.4).
func TestReview_FailWithBudgetRewindsIntoTheLoop(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: `{"verdict":"fail","reason":"only half the table"}`}}}
	env := reviewEnv(t, llm)
	res, err := reviewExecutor{}.Execute(context.Background(), env, reviewNode(ReviewAttrs{
		UpstreamNode: "agent_loop", MaxIterations: 3,
	}), PortValues{"draft": "partial"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if retry, _ := res.Outputs["should_retry"].(bool); !retry {
		t.Error("should_retry = false on a FAIL with budget remaining; the turn would return half-done work")
	}
	if got, _ := res.Outputs.GetString("retry_target"); got != "agent_loop" {
		t.Errorf("retry_target = %q, want %q", got, "agent_loop")
	}
	v, _ := res.Outputs["verdict"].(map[string]any)
	if v["reason"] != "only half the table" {
		t.Errorf("verdict.reason = %v, want the structured reason", v["reason"])
	}
}

// TestReview_CapHitUnderAskWithholdReturnsTheDraft is the autonomy
// finding F7, landed here.
//
// At askOnAmbiguity proceed/never the chat surface already withholds
// kenaz__ask_user_question from the model. Without this, the exit gate
// re-opened that door from a path the user never saw: on_cap_hit
// escalate leads to the ladder, and the ladder's terminal rung asks a
// human. Under Withhold the cap hit returns the best draft — the
// verdict is still FAIL and still on the EventLog, but the run ends
// with the work rather than with a question.
func TestReview_CapHitUnderAskWithholdReturnsTheDraft(t *testing.T) {
	t.Parallel()
	for _, onCapHit := range []string{"escalate", "halt"} {
		onCapHit := onCapHit
		t.Run("on_cap_hit="+onCapHit, func(t *testing.T) {
			t.Parallel()
			llm := &stubLLM{responses: []LLMResponse{{Content: `{"verdict":"fail","reason":"still partial"}`}}}
			env := reviewEnv(t, llm)
			env.AskPolicy = AskPolicyWithhold

			res, err := reviewExecutor{}.Execute(context.Background(), env, reviewNode(ReviewAttrs{
				UpstreamNode: "agent_loop", MaxIterations: 1, OnCapHit: onCapHit,
			}), PortValues{"draft": "the best I have"})
			if err != nil {
				t.Fatalf("Execute: want no error under AskPolicyWithhold, got %v", err)
			}
			if retry, _ := res.Outputs["should_retry"].(bool); retry {
				t.Error("should_retry = true at the cap; the loop would never terminate")
			}
			if got, _ := res.Outputs.GetString("approved"); got != "the best I have" {
				t.Errorf("approved = %q, want the draft returned to the user", got)
			}
			if _, escalated := res.Outputs["escalated"]; escalated {
				t.Error("escalated at a cap hit while questions are withheld — F7 is exactly this")
			}
			var sawProceeded, sawUnrecov bool
			for _, e := range res.Events.Events {
				switch e.Kind {
				case EventReviewCapProceeded:
					sawProceeded = true
				case EventReviewUnrecov:
					sawUnrecov = true
				case EventEscalateTriggered:
					t.Error("escalate_triggered emitted while questions are withheld")
				}
			}
			if !sawProceeded {
				t.Error("no review_cap_proceeded event; the suppression must be auditable")
			}
			if !sawUnrecov {
				t.Error("review_failed_unrecoverable was suppressed too; the verdict was still a failure and the log must say so")
			}
		})
	}
}

// TestReview_CapHitUnderAskAllowKeepsTodaysBehaviour is the other half
// of F7: the default posture is untouched. `escalate` still escalates,
// `halt` still errors.
func TestReview_CapHitUnderAskAllowKeepsTodaysBehaviour(t *testing.T) {
	t.Parallel()
	t.Run("escalate", func(t *testing.T) {
		t.Parallel()
		llm := &stubLLM{responses: []LLMResponse{{Content: "FAIL"}}}
		env := reviewEnv(t, llm)
		res, err := reviewExecutor{}.Execute(context.Background(), env, reviewNode(ReviewAttrs{
			UpstreamNode: "u", MaxIterations: 1, OnCapHit: "escalate",
		}), PortValues{"draft": "d"})
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if esc, _ := res.Outputs["escalated"].(bool); !esc {
			t.Error("escalated flag missing under the default ask policy")
		}
	})
	t.Run("halt", func(t *testing.T) {
		t.Parallel()
		llm := &stubLLM{responses: []LLMResponse{{Content: "FAIL"}}}
		env := reviewEnv(t, llm)
		_, err := reviewExecutor{}.Execute(context.Background(), env, reviewNode(ReviewAttrs{
			UpstreamNode: "u", MaxIterations: 1,
		}), PortValues{"draft": "d"})
		if err == nil {
			t.Error("want a cap-hit error under the default ask policy, got nil")
		}
	})
}

// TestReview_PolitePassNoLongerBurnsTheRetryBudget is the end-to-end
// statement of the parser fix at the executor level: the reply that
// used to score FAIL now passes, so the turn returns instead of
// rewinding.
func TestReview_PolitePassNoLongerBurnsTheRetryBudget(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "Sure! PASS — this fully answers the question."}}}
	env := reviewEnv(t, llm)
	res, err := reviewExecutor{}.Execute(context.Background(), env, reviewNode(ReviewAttrs{
		UpstreamNode: "agent_loop", MaxIterations: 3,
	}), PortValues{"draft": "the answer"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if retry, _ := res.Outputs["should_retry"].(bool); retry {
		t.Error("a polite PASS still rewound the turn — this is the defect the structured verdict exists to fix")
	}
	var sawPass bool
	for _, e := range res.Events.Events {
		if e.Kind == EventReviewPass {
			sawPass = true
		}
	}
	if !sawPass {
		t.Error("no review_pass event for a passing verdict")
	}
}
