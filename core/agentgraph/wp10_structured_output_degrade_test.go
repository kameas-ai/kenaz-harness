package agentgraph

// wp10_structured_output_degrade_test.go —
// structured-output-is-reachable-01PMZE14 WP10: the review gate and the
// router ask for a real schema on LLMRequest.ResponseSchema (WP02's
// field) instead of only asking nicely in prose, and degrade — never
// fail — when the resolved model's capability row says
// structured_output: false. AC-011(a)/(b) and the router's equivalent.

import (
	"context"
	"encoding/json"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestReview_RequestsStructuredOutputSchema is AC-011(a)'s agentgraph-
// layer half: the review call's LLMRequest carries a ResponseSchema
// naming exactly the fields reviewPrompt already demands in prose
// ("verdict", "reason"). The wire-level assertion (does the resulting
// GenerationRequest.ResponseFormat actually reach the provider body) is
// core/llm/registry's job — TestRegistry_StructuredOutputAudit_* and the
// pre-existing TestRegistry_StreamCapabilityRejection_ResponseFormat
// already cover that seam; this test pins the boundary immediately
// above it.
func TestReview_RequestsStructuredOutputSchema(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: `{"verdict":"pass","reason":"looks right"}`}}}
	env := reviewEnv(t, llm)

	_, err := reviewExecutor{}.Execute(context.Background(), env, reviewNode(ReviewAttrs{
		UpstreamNode: "agent_loop", MaxIterations: 2,
	}), PortValues{"draft": "the answer"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	llm.mu.Lock()
	defer llm.mu.Unlock()
	if len(llm.calls) != 1 {
		t.Fatalf("Generate calls = %d, want 1", len(llm.calls))
	}
	schema := llm.calls[0].ResponseSchema
	if len(schema) == 0 {
		t.Fatal("review call carries no ResponseSchema — the review gate is still only asking in prose")
	}
	var decoded map[string]any
	if err := json.Unmarshal(schema, &decoded); err != nil {
		t.Fatalf("ResponseSchema is not valid JSON: %v", err)
	}
	required, _ := decoded["required"].([]any)
	got := map[string]bool{}
	for _, r := range required {
		if s, ok := r.(string); ok {
			got[s] = true
		}
	}
	if !got["verdict"] || !got["reason"] {
		t.Errorf("ResponseSchema.required = %v, want both %q and %q", required, "verdict", "reason")
	}
}

// TestReview_DegradesWhenStructuredOutputUnsupported is AC-011(b): a
// *llm.ErrCapabilityUnsupported naming CapStructuredOutput on the first
// (schema-carrying) call must not fail the run — the review gate must
// drop the schema and re-issue, and the tolerant parser (parseReviewVerdict)
// must still run on the second call's plain-text reply. D-9: "Must go
// red if (b) errors" — this is the single most likely way the mission
// causes a regression, per tasks.md.
func TestReview_DegradesWhenStructuredOutputUnsupported(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{
		failOn:  1,
		failErr: &corellm.ErrCapabilityUnsupported{Provider: "nostruct", Model: "m", Capabilities: []corellm.Capability{corellm.CapStructuredOutput}},
		responses: []LLMResponse{
			{Content: "Sure! PASS — the work looks complete."},
		},
	}
	env := reviewEnv(t, llm)

	res, err := reviewExecutor{}.Execute(context.Background(), env, reviewNode(ReviewAttrs{
		UpstreamNode: "agent_loop", MaxIterations: 2,
	}), PortValues{"draft": "the answer"})
	if err != nil {
		t.Fatalf("Execute must degrade, not fail, on ErrCapabilityUnsupported(structured_output): %v", err)
	}

	llm.mu.Lock()
	defer llm.mu.Unlock()
	if len(llm.calls) != 2 {
		t.Fatalf("Generate calls = %d, want 2 (schema attempt + degraded retry)", len(llm.calls))
	}
	if len(llm.calls[0].ResponseSchema) == 0 {
		t.Error("first call should have carried a ResponseSchema")
	}
	if len(llm.calls[1].ResponseSchema) != 0 {
		t.Error("second (degraded) call must NOT carry a ResponseSchema")
	}
	verdict, _ := res.Outputs["verdict"].(map[string]any)
	if verdict["verdict"] != "pass" {
		t.Errorf("verdict = %v, want pass (tolerant parser must still run on the degraded reply)", verdict)
	}
}

// TestReview_OtherErrorsAreNotTreatedAsDegradePath verifies that an
// error unrelated to structured-output capability (e.g. a transient
// provider failure) is NOT caught by the degrade branch — only
// ErrCapabilityUnsupported naming CapStructuredOutput specifically may
// trigger a silent retry; everything else must propagate as a real
// node error, or the executor would swallow failures it has no
// business swallowing.
func TestReview_OtherErrorsAreNotTreatedAsDegradePath(t *testing.T) {
	t.Parallel()
	wantErr := &corellm.ErrTransient{Status: 503, Message: "blip"}
	llm := &stubLLM{
		failOn:  1,
		failErr: wantErr,
	}
	env := reviewEnv(t, llm)

	_, err := reviewExecutor{}.Execute(context.Background(), env, reviewNode(ReviewAttrs{
		UpstreamNode: "agent_loop", MaxIterations: 2,
	}), PortValues{"draft": "the answer"})
	if err == nil {
		t.Fatal("expected the non-capability error to propagate, got nil")
	}
	llm.mu.Lock()
	defer llm.mu.Unlock()
	if len(llm.calls) != 1 {
		t.Fatalf("Generate calls = %d, want exactly 1 — a non-capability error must not be retried", len(llm.calls))
	}
}

// TestRouterAskModel_RequestsStructuredOutputSchema is the router's
// equivalent of TestReview_RequestsStructuredOutputSchema: standalone
// mode's routing call carries a ResponseSchema naming the choice field
// (defaultRouterChoiceField) constrained to the menu's ids.
func TestRouterAskModel_RequestsStructuredOutputSchema(t *testing.T) {
	t.Parallel()
	llm := &countingLLM{reply: "done"}
	env := routerEnv(t, llm)
	node := routerNode("route", RouterAttrs{
		Mode: routerModeStandalone, Choices: routerMenu(), DefaultChoice: "done",
	})
	if _, err := (routerExecutor{}).Execute(context.Background(), env, node, PortValues{"in": "p"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	reqs := llm.snapshotRequests()
	if len(reqs) != 1 {
		t.Fatalf("Generate calls = %d, want 1", len(reqs))
	}
	schema := reqs[0].ResponseSchema
	if len(schema) == 0 {
		t.Fatal("router standalone call carries no ResponseSchema")
	}
	var decoded map[string]any
	if err := json.Unmarshal(schema, &decoded); err != nil {
		t.Fatalf("ResponseSchema is not valid JSON: %v", err)
	}
	props, _ := decoded["properties"].(map[string]any)
	field, _ := props[defaultRouterChoiceField].(map[string]any)
	if field == nil {
		t.Fatalf("ResponseSchema.properties missing %q: %v", defaultRouterChoiceField, decoded)
	}
	enumRaw, _ := field["enum"].([]any)
	gotIDs := map[string]bool{}
	for _, v := range enumRaw {
		if s, ok := v.(string); ok {
			gotIDs[s] = true
		}
	}
	for id := range routerMenu() {
		if !gotIDs[id] {
			t.Errorf("ResponseSchema enum missing choice id %q, got %v", id, enumRaw)
		}
	}
}

// TestRouterAskModel_DegradesWhenStructuredOutputUnsupported mirrors the
// review gate's degrade test for the router's standalone call.
func TestRouterAskModel_DegradesWhenStructuredOutputUnsupported(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{
		failOn:  1,
		failErr: &corellm.ErrCapabilityUnsupported{Provider: "nostruct", Model: "m", Capabilities: []corellm.Capability{corellm.CapStructuredOutput}},
		responses: []LLMResponse{
			{Content: "research"},
		},
	}
	env := reviewEnv(t, llm) // reviewEnv builds a plain Env{LLM: llm}; router doesn't need anything review-specific.
	node := routerNode("route", RouterAttrs{
		Mode: routerModeStandalone, Choices: routerMenu(), DefaultChoice: "done",
	})
	res, err := routerExecutor{}.Execute(context.Background(), env, node, PortValues{"in": "p"})
	if err != nil {
		t.Fatalf("Execute must degrade, not fail: %v", err)
	}
	llm.mu.Lock()
	calls := append([]LLMRequest(nil), llm.calls...)
	llm.mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("Generate calls = %d, want 2 (schema attempt + degraded retry)", len(calls))
	}
	if len(calls[0].ResponseSchema) == 0 {
		t.Error("first call should have carried a ResponseSchema")
	}
	if len(calls[1].ResponseSchema) != 0 {
		t.Error("second (degraded) call must NOT carry a ResponseSchema")
	}
	if got, _ := res.Outputs.GetString("next"); got != "research" {
		t.Errorf("next = %q, want %q", got, "research")
	}
}
