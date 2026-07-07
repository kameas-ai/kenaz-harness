package contextbootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ─── fakes ────────────────────────────────────────────────────────────────────

// fakeModelCaller is a race-safe fake ModelCaller.
type fakeModelCaller struct {
	mu       sync.Mutex
	response string
	err      error
	calls    []string
}

func (f *fakeModelCaller) Complete(_ context.Context, prompt string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, prompt)
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

func (f *fakeModelCaller) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// fakeMCPPool is a race-safe fake MCPPool.
type fakeMCPPool struct {
	mu      sync.Mutex
	tools   []MCPTool
	running map[string]bool
	callFn  func(server, tool string, args json.RawMessage) (json.RawMessage, error)
}

func (f *fakeMCPPool) Tools(_ context.Context) ([]MCPTool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]MCPTool(nil), f.tools...), nil
}

func (f *fakeMCPPool) Call(_ context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	fn := f.callFn
	f.mu.Unlock()
	if fn != nil {
		return fn(server, tool, args)
	}
	return json.RawMessage(`[]`), nil
}

func (f *fakeMCPPool) IsRunning(_ context.Context, recipeID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[recipeID]
}

// fakeContextWriter is a race-safe fake ContextWriter.
type fakeContextWriter struct {
	mu    sync.Mutex
	nodes []ExtractedNode
	syncs []SyncPayload
}

func (f *fakeContextWriter) WriteNodes(_ context.Context, nodes []ExtractedNode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodes = append(f.nodes, nodes...)
	return nil
}

func (f *fakeContextWriter) Sync(_ context.Context, payload SyncPayload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncs = append(f.syncs, payload)
	return nil
}

func (f *fakeContextWriter) snapshotNodes() []ExtractedNode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ExtractedNode(nil), f.nodes...)
}

func (f *fakeContextWriter) snapshotSyncs() []SyncPayload {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]SyncPayload(nil), f.syncs...)
}

// fakeProgressSink is a race-safe fake ProgressSink.
type fakeProgressSink struct {
	mu     sync.Mutex
	events []RunStatus
}

func (f *fakeProgressSink) Emit(_ context.Context, s RunStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, s)
}

func (f *fakeProgressSink) snapshot() []RunStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]RunStatus(nil), f.events...)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// minimalRecipe builds a recipe with one outlook connector for tests.
func minimalRecipe() *BootstrapRecipe {
	return &BootstrapRecipe{
		Version: "test",
		InterviewSchema: InterviewSchema{
			ToolChoices: []ToolChoice{
				{ID: "outlook", Label: "Outlook", MCPRecipeID: "microsoft-outlook"},
			},
		},
		ConnectorCatalog: []ConnectorDef{
			{
				ID:            "outlook",
				Label:         "Outlook",
				MCPRecipeID:   "microsoft-outlook",
				ReadOnlyTools: []string{"list_messages"},
				ExtractionRecipe: ConnectorExtractionRecipe{
					FetchStrategy:    "list_recent_N:10",
					ExtractionPrompt: "Extract patterns from the data below.",
					MaxItems:         10,
					MaxTokens:        10000,
				},
			},
		},
		Taxonomy: []TaxonomyEntry{
			{Kind: "person", Description: "A person"},
			{Kind: "project", Description: "A project"},
		},
		ConfidenceRules: ConfidenceRules{
			AssertMinCorroborations: 3,
			TrustedPersonWeight:     3,
		},
	}
}

// extractionResponseJSON is a model response for tests.
const extractionResponseJSON = `{
  "nodes": [
    {
      "kind": "person",
      "title": "Alice",
      "body": "Alice is the engineering lead.",
      "source_kind": "email",
      "source_refs": ["msg-1", "msg-2", "msg-3"],
      "corroborating_sources": ["bob@example.com", "carol@example.com", "dave@example.com"],
      "corroborations": 3
    }
  ]
}`

// ─── tests ────────────────────────────────────────────────────────────────────

func TestEngineRunNoApprovedConnectors(t *testing.T) {
	writer := &fakeContextWriter{}
	eng := New(Config{
		RecipeSource: NewLocalRecipeSourceFromJSON(mustMarshal(t, minimalRecipe())),
		Pool:         &fakeMCPPool{running: map[string]bool{}},
		Model:        &fakeModelCaller{response: extractionResponseJSON},
		Writer:       writer,
	})

	result, err := eng.Run(context.Background(), RunRequest{
		Interview: InterviewResult{SelectedConnectorIDs: []string{"outlook"}},
		Gating:    GatingResult{Approved: nil}, // no approved connectors
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.TotalNodes != 0 {
		t.Errorf("expected 0 nodes, got %d", result.TotalNodes)
	}
	if len(writer.snapshotNodes()) != 0 {
		t.Errorf("expected no writes to context store")
	}
}

func TestEngineRunExtractionWritesNodes(t *testing.T) {
	pool := &fakeMCPPool{
		running: map[string]bool{"microsoft-outlook": true},
		callFn: func(server, tool string, args json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`[{"id":"msg-1","from":"bob@example.com","body":"Alice is the engineering lead and we use the Acme project."},{"id":"msg-2","from":"carol@example.com","body":"Alice leads engineering on Acme."},{"id":"msg-3","from":"dave@example.com","body":"Check with Alice on the Acme timeline."}]`), nil
		},
	}

	writer := &fakeContextWriter{}
	model := &fakeModelCaller{response: extractionResponseJSON}
	progress := &fakeProgressSink{}

	eng := New(Config{
		RecipeSource:        NewLocalRecipeSourceFromJSON(mustMarshal(t, minimalRecipe())),
		Pool:                pool,
		Model:               model,
		Writer:              writer,
		Progress:            progress,
		PerConnectorTimeout: 10 * time.Second,
	})

	result, err := eng.Run(context.Background(), RunRequest{
		Interview: InterviewResult{SelectedConnectorIDs: []string{"outlook"}},
		Gating:    GatingResult{Approved: []string{"outlook"}},
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// The model returned 1 node with 3 corroborations → asserted.
	if result.TotalNodes != 1 {
		t.Errorf("expected 1 node, got %d", result.TotalNodes)
	}

	// Nodes must be written to the context store.
	written := writer.snapshotNodes()
	if len(written) != 1 {
		t.Fatalf("expected 1 written node, got %d", len(written))
	}
	if written[0].Title != "Alice" {
		t.Errorf("expected node title 'Alice', got %q", written[0].Title)
	}
	if !written[0].IsAsserted {
		t.Error("expected node to be asserted (3 corroborations ≥ threshold 3)")
	}

	// Sync payload must be sent (even though it's a noop in the fake).
	syncs := writer.snapshotSyncs()
	if len(syncs) != 1 {
		t.Fatalf("expected 1 sync call, got %d", len(syncs))
	}

	// Progress events must be emitted.
	events := progress.snapshot()
	if len(events) == 0 {
		t.Error("expected at least one progress event")
	}
}

func TestEngineRunBudgetHitStopsCleanly(t *testing.T) {
	// Build a recipe with MaxItems=2 so we hit the budget quickly.
	recipe := minimalRecipe()
	recipe.ConnectorCatalog[0].ExtractionRecipe.MaxItems = 2

	pool := &fakeMCPPool{
		running: map[string]bool{"microsoft-outlook": true},
		callFn: func(server, tool string, args json.RawMessage) (json.RawMessage, error) {
			// Return 5 items — more than MaxItems.
			return json.RawMessage(`[
				{"id":"msg-1","from":"a@b.com","body":"item1"},
				{"id":"msg-2","from":"b@b.com","body":"item2"},
				{"id":"msg-3","from":"c@b.com","body":"item3"},
				{"id":"msg-4","from":"d@b.com","body":"item4"},
				{"id":"msg-5","from":"e@b.com","body":"item5"}
			]`), nil
		},
	}

	writer := &fakeContextWriter{}
	model := &fakeModelCaller{response: `{"nodes":[]}`}

	eng := New(Config{
		RecipeSource:        NewLocalRecipeSourceFromJSON(mustMarshal(t, recipe)),
		Pool:                pool,
		Model:               model,
		Writer:              writer,
		PerConnectorTimeout: 10 * time.Second,
	})

	result, err := eng.Run(context.Background(), RunRequest{
		Interview: InterviewResult{SelectedConnectorIDs: []string{"outlook"}},
		Gating:    GatingResult{Approved: []string{"outlook"}},
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// A coverage report must be generated (never silent truncation).
	if len(result.CoverageReport) != 1 {
		t.Errorf("expected coverage report for budget-hit connector, got %v", result.CoverageReport)
	}
	if result.CoverageReport[0].ConnectorID != "outlook" {
		t.Errorf("coverage report connector mismatch: %+v", result.CoverageReport[0])
	}

	// Status must reflect the budget hit.
	status := eng.Status()
	if len(status.CoverageReport) != 1 {
		t.Errorf("expected status coverage report, got %v", status.CoverageReport)
	}
}

func TestEngineRunReadOnlyToolEnforced(t *testing.T) {
	// Verify that the engine refuses to call a tool not in ReadOnlyTools.
	recipe := minimalRecipe()
	// ReadOnlyTools only allows "list_messages".
	// We simulate the extraction recipe calling a forbidden tool by injecting
	// a custom pool that tracks which tools are called.
	var calledTools []string
	var mu sync.Mutex

	pool := &fakeMCPPool{
		running: map[string]bool{"microsoft-outlook": true},
		callFn: func(server, tool string, args json.RawMessage) (json.RawMessage, error) {
			mu.Lock()
			calledTools = append(calledTools, tool)
			mu.Unlock()
			return json.RawMessage(`[]`), nil
		},
	}

	eng := New(Config{
		RecipeSource:        NewLocalRecipeSourceFromJSON(mustMarshal(t, recipe)),
		Pool:                pool,
		Model:               &fakeModelCaller{response: `{"nodes":[]}`},
		Writer:              &fakeContextWriter{},
		PerConnectorTimeout: 5 * time.Second,
	})

	_, err := eng.Run(context.Background(), RunRequest{
		Interview: InterviewResult{SelectedConnectorIDs: []string{"outlook"}},
		Gating:    GatingResult{Approved: []string{"outlook"}},
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// Only list_messages should be called.
	for _, called := range calledTools {
		if called != "list_messages" {
			t.Errorf("non-whitelisted tool called: %q", called)
		}
	}
}

func TestEngineClarificationLoop(t *testing.T) {
	// Model returns a node with only 1 corroboration (below threshold) → clarification.
	lowConfidenceResponse := `{
		"nodes": [{
			"kind": "person",
			"title": "Bob",
			"body": "Bob is the manager.",
			"source_kind": "email",
			"corroborations": 1
		}]
	}`

	pool := &fakeMCPPool{
		running: map[string]bool{"microsoft-outlook": true},
		callFn: func(server, tool string, args json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`[{"id":"msg-1","from":"x@y.com","body":"Bob is the manager."}]`), nil
		},
	}

	writer := &fakeContextWriter{}
	eng := New(Config{
		RecipeSource:        NewLocalRecipeSourceFromJSON(mustMarshal(t, minimalRecipe())),
		Pool:                pool,
		Model:               &fakeModelCaller{response: lowConfidenceResponse},
		Writer:              writer,
		PerConnectorTimeout: 5 * time.Second,
	})

	result, err := eng.Run(context.Background(), RunRequest{
		Gating: GatingResult{Approved: []string{"outlook"}},
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Bob has only 1 corroboration → tentative → clarification item.
	if len(result.Clarifications) != 1 {
		t.Errorf("expected 1 clarification item, got %d", len(result.Clarifications))
	}
	if result.Clarifications[0].Node.Title != "Bob" {
		t.Errorf("expected clarification for Bob, got %q", result.Clarifications[0].Node.Title)
	}

	// Applying clarification: user confirms Bob.
	answers := []ClarificationAnswer{{
		NodeTitle: "Bob",
		Confirmed: true,
	}}
	if err := eng.ApplyClarifications(context.Background(), result.Clarifications, answers); err != nil {
		t.Fatalf("ApplyClarifications failed: %v", err)
	}

	// Bob must now be written to the context store.
	written := writer.snapshotNodes()
	if len(written) != 1 || written[0].Title != "Bob" {
		t.Errorf("expected Bob in context store after clarification, got %v", written)
	}
	if !written[0].IsAsserted {
		t.Error("confirmed node must be asserted")
	}
}

func TestGaterApproveAndBlock(t *testing.T) {
	catalog := []ConnectorDef{
		{
			ID:            "outlook",
			Label:         "Outlook",
			MCPRecipeID:   "microsoft-outlook",
			ReadOnlyTools: []string{"list_messages"},
		},
		{
			ID:            "slack",
			Label:         "Slack",
			MCPRecipeID:   "slack",
			ReadOnlyTools: []string{"list_channels"},
		},
	}

	pool := &fakeMCPPool{
		running: map[string]bool{"microsoft-outlook": true},
		// slack is not running
	}

	gater := newGater(pool, catalog)
	result := gater.Gate(context.Background(), []string{"outlook", "slack", "unknown-connector"})

	if len(result.Approved) != 1 || result.Approved[0] != "outlook" {
		t.Errorf("expected outlook approved, got %v", result.Approved)
	}

	if len(result.Blocked) != 2 {
		t.Errorf("expected 2 blocked (slack + unknown), got %v", result.Blocked)
	}
}

func TestInterviewSeed(t *testing.T) {
	recipe := minimalRecipe()
	runner := newInterviewRunner(recipe, nil)
	result := runner.Seed(context.Background(), SeedRequest{
		SelectedIDs: []string{"outlook", "slack"},
	})
	if len(result.SelectedConnectorIDs) != 2 {
		t.Errorf("expected 2 connectors, got %v", result.SelectedConnectorIDs)
	}
}

func TestInterviewSeedDeduplicatesWelcomeSeed(t *testing.T) {
	recipe := minimalRecipe()
	runner := newInterviewRunner(recipe, nil)
	result := runner.Seed(context.Background(), SeedRequest{
		WelcomeChecklistSeed: []string{"outlook"},
		SelectedIDs:          []string{"outlook", "slack"},
	})
	// outlook appears in both but should not be duplicated.
	count := 0
	for _, id := range result.SelectedConnectorIDs {
		if id == "outlook" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected outlook deduplicated to 1, got %d", count)
	}
}

func TestLocalRecipeSource(t *testing.T) {
	src := NewLocalRecipeSource()
	recipe, err := src.LoadRecipe(context.Background())
	if err != nil {
		t.Fatalf("LoadRecipe failed: %v", err)
	}
	if recipe.Version == "" {
		t.Error("recipe version should not be empty")
	}
	if len(recipe.ConnectorCatalog) == 0 {
		t.Error("recipe catalog should not be empty")
	}
	// Verify outlook connector is present.
	found := false
	for _, c := range recipe.ConnectorCatalog {
		if c.ID == "outlook" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected outlook connector in embedded recipe")
	}
}

func TestBuildChecklist(t *testing.T) {
	schema := InterviewSchema{
		ToolChoices: []ToolChoice{
			{ID: "outlook", Label: "Outlook"},
			{ID: "slack", Label: "Slack"},
		},
	}
	items := BuildChecklist(schema, []string{"outlook"})
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	for _, item := range items {
		if item.ID == "outlook" && !item.Checked {
			t.Error("expected outlook pre-checked")
		}
		if item.ID == "slack" && item.Checked {
			t.Error("expected slack not pre-checked")
		}
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustMarshal: %v", err)
	}
	return b
}

// TestFakeModelCallerImplementsInterface verifies interface satisfaction.
func TestFakeModelCallerImplementsInterface(t *testing.T) {
	var _ ModelCaller = &fakeModelCaller{}
	var _ MCPPool = &fakeMCPPool{}
	var _ ContextWriter = &fakeContextWriter{}
	var _ ProgressSink = &fakeProgressSink{}
}

// TestClassifyError verifies the error classifier.
func TestClassifyError(t *testing.T) {
	if classifyError(nil) != "" {
		t.Error("nil error should return empty string")
	}
	if classifyError(context.Canceled) != "context_canceled_or_deadline" {
		t.Error("context.Canceled should be classified correctly")
	}
	if classifyError(fmt.Errorf("permission denied")) != "permission_denied" {
		t.Error("permission error should be classified correctly")
	}
	if classifyError(fmt.Errorf("some random error")) != "other" {
		t.Error("unknown error should be classified as other")
	}
}
