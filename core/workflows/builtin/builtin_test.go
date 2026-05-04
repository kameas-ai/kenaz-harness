// Package builtin_test validates every YAML in core/workflows/builtin/ against
// the production loader. All tests are real — no skips, no mocks.
package builtin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robfig/cron/v3"
	workflows "github.com/sigil-tech/kaneaz-harness/core/workflows"
)

// loadBuiltinFile is a test helper that reads a YAML from the directory that
// contains this test file and parses it via the production loader.
func loadBuiltinFile(t *testing.T, name string) workflows.Workflow {
	t.Helper()
	path := filepath.Join(".", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q): %v", name, err)
	}
	w, err := workflows.LoadYAML(data)
	if err != nil {
		t.Fatalf("LoadYAML(%q): %v", name, err)
	}
	return w
}

// assertCronParses verifies that expr is a valid 5-field cron expression
// accepted by robfig/cron/v3.
func assertCronParses(t *testing.T, expr string) {
	t.Helper()
	p := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if _, err := p.Parse(expr); err != nil {
		t.Errorf("cron expression %q failed to parse: %v", expr, err)
	}
}

// countStepsByKind returns the number of steps with a given kind.
func countStepsByKind(w workflows.Workflow, kind workflows.StepKind) int {
	n := 0
	for _, s := range w.Steps {
		if s.Kind == kind {
			n++
		}
	}
	return n
}

// findStep returns the named step or fails the test.
func findStep(t *testing.T, w workflows.Workflow, name string) workflows.Step {
	t.Helper()
	for _, s := range w.Steps {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("step %q not found in workflow %q", name, w.ID)
	return workflows.Step{}
}

// ---------------------------------------------------------------------------
// TestBuiltinCatalog_Lists_All — top-level count guard
// ---------------------------------------------------------------------------

func TestBuiltinCatalog_Lists_All(t *testing.T) {
	wfs, errs := workflows.LoadBuiltins()
	for _, e := range errs {
		t.Errorf("LoadBuiltins error: %v", e)
	}
	const want = 6 // 5 new + plan_implement_review
	if len(wfs) < want {
		t.Errorf("LoadBuiltins returned %d workflows, want >= %d", len(wfs), want)
	}
}

// ---------------------------------------------------------------------------
// TestDailyEABriefing
// ---------------------------------------------------------------------------

func TestDailyEABriefing(t *testing.T) {
	w := loadBuiltinFile(t, "daily_ea_briefing.yaml")

	// Metadata.
	if w.ID != "daily_ea_briefing" {
		t.Errorf("id: got %q want %q", w.ID, "daily_ea_briefing")
	}
	if w.Name == "" {
		t.Error("name must not be empty")
	}
	if w.Description == "" {
		t.Error("description must not be empty")
	}

	// Schedule.
	if w.Schedule == "" {
		t.Error("schedule must not be empty")
	}
	assertCronParses(t, w.Schedule)
	if w.Timezone != "America/New_York" {
		t.Errorf("timezone: got %q want %q", w.Timezone, "America/New_York")
	}

	// Must have exactly 5 steps.
	if len(w.Steps) != 5 {
		t.Errorf("steps: got %d want 5", len(w.Steps))
	}

	// 3 mcp_call parents.
	if n := countStepsByKind(w, workflows.StepKindMCPCall); n != 3 {
		t.Errorf("mcp_call steps: got %d want 3", n)
	}

	// The model_turn step must fan in from all 3 mcp_call steps.
	briefing := findStep(t, w, "write_briefing")
	if briefing.Kind != workflows.StepKindModelTurn {
		t.Errorf("write_briefing kind: got %q want model_turn", briefing.Kind)
	}
	if len(briefing.InputsFrom) != 3 {
		t.Errorf("write_briefing.inputs_from: got %v want 3 parents", briefing.InputsFrom)
	}

	// MCP server references.
	servers := make(map[string]bool)
	for _, s := range w.Steps {
		if s.Kind == workflows.StepKindMCPCall {
			servers[s.Server] = true
		}
	}
	for _, want := range []string{"gmail", "slack", "google_calendar"} {
		if !servers[want] {
			t.Errorf("expected mcp_call.server %q not found", want)
		}
	}

	// notify step.
	notify := findStep(t, w, "notify")
	if notify.Kind != workflows.StepKindNotify {
		t.Errorf("notify step kind: got %q want notify", notify.Kind)
	}
}

// ---------------------------------------------------------------------------
// TestCodeReview
// ---------------------------------------------------------------------------

func TestCodeReview(t *testing.T) {
	w := loadBuiltinFile(t, "code_review.yaml")

	if w.ID != "code_review" {
		t.Errorf("id: got %q want %q", w.ID, "code_review")
	}

	// Inputs: pr_url (string, required).
	if len(w.Inputs) == 0 {
		t.Fatal("inputs must not be empty")
	}
	var prInput *workflows.Input
	for i := range w.Inputs {
		if w.Inputs[i].Name == "pr_url" {
			prInput = &w.Inputs[i]
		}
	}
	if prInput == nil {
		t.Fatal("input pr_url not found")
	}
	if prInput.Kind != "string" {
		t.Errorf("pr_url kind: got %q want string", prInput.Kind)
	}

	// Steps: fetch_diff, summarize_changes, find_issues, format_comment.
	expectedSteps := []string{"fetch_diff", "summarize_changes", "find_issues", "format_comment"}
	stepSet := make(map[string]bool)
	for _, s := range w.Steps {
		stepSet[s.Name] = true
	}
	for _, name := range expectedSteps {
		if !stepSet[name] {
			t.Errorf("expected step %q not found", name)
		}
	}

	// fetch_diff must be an http_request.
	fetchDiff := findStep(t, w, "fetch_diff")
	if fetchDiff.Kind != workflows.StepKindHTTPRequest {
		t.Errorf("fetch_diff kind: got %q want http_request", fetchDiff.Kind)
	}

	// format_comment must be a transform with inputs_from.
	fc := findStep(t, w, "format_comment")
	if fc.Kind != workflows.StepKindTransform {
		t.Errorf("format_comment kind: got %q want transform", fc.Kind)
	}
	if len(fc.InputsFrom) == 0 {
		t.Error("format_comment must declare inputs_from")
	}
}

// ---------------------------------------------------------------------------
// TestWebResearch
// ---------------------------------------------------------------------------

func TestWebResearch(t *testing.T) {
	w := loadBuiltinFile(t, "web_research.yaml")

	if w.ID != "web_research" {
		t.Errorf("id: got %q want %q", w.ID, "web_research")
	}

	// Inputs: query, top_n.
	inputNames := make(map[string]bool)
	for _, in := range w.Inputs {
		inputNames[in.Name] = true
	}
	for _, want := range []string{"query", "top_n"} {
		if !inputNames[want] {
			t.Errorf("expected input %q not found", want)
		}
	}

	// Must have at least: search, scrape_top, >=1 fetch step, aggregate_text, summarize.
	stepNames := make(map[string]bool)
	for _, s := range w.Steps {
		stepNames[s.Name] = true
	}
	required := []string{"search", "scrape_top", "aggregate_text", "summarize"}
	for _, name := range required {
		if !stepNames[name] {
			t.Errorf("expected step %q not found", name)
		}
	}

	// aggregate_text must use concat strategy.
	agg := findStep(t, w, "aggregate_text")
	if agg.Kind != workflows.StepKindAggregate {
		t.Errorf("aggregate_text kind: got %q want aggregate", agg.Kind)
	}
	if agg.Strategy != "concat" {
		t.Errorf("aggregate_text strategy: got %q want concat", agg.Strategy)
	}

	// web_scrape step in llm mode.
	scrape := findStep(t, w, "scrape_top")
	if scrape.Kind != workflows.StepKindWebScrape {
		t.Errorf("scrape_top kind: got %q want web_scrape", scrape.Kind)
	}
	if scrape.Mode != "llm" {
		t.Errorf("scrape_top mode: got %q want llm", scrape.Mode)
	}
}

// ---------------------------------------------------------------------------
// TestPRStatusPoll
// ---------------------------------------------------------------------------

func TestPRStatusPoll(t *testing.T) {
	w := loadBuiltinFile(t, "pr_status_poll.yaml")

	if w.ID != "pr_status_poll" {
		t.Errorf("id: got %q want %q", w.ID, "pr_status_poll")
	}

	// Schedule: every 30 minutes.
	if w.Schedule == "" {
		t.Error("schedule must not be empty")
	}
	assertCronParses(t, w.Schedule)
	if !strings.Contains(w.Schedule, "30") && !strings.Contains(w.Schedule, "*/30") {
		t.Errorf("schedule %q does not look like a 30-minute interval", w.Schedule)
	}

	// Inputs: repo, pr_numbers.
	inputNames := make(map[string]bool)
	for _, in := range w.Inputs {
		inputNames[in.Name] = true
	}
	for _, want := range []string{"repo", "pr_numbers"} {
		if !inputNames[want] {
			t.Errorf("expected input %q not found", want)
		}
	}

	// Must include parallel http_request fetches, aggregate, conditional, notify.
	var hasHTTP, hasAggregate, hasConditional, hasNotify bool
	for _, s := range w.Steps {
		switch s.Kind {
		case workflows.StepKindHTTPRequest:
			hasHTTP = true
		case workflows.StepKindAggregate:
			hasAggregate = true
			if s.Strategy != "array" {
				t.Errorf("aggregate strategy: got %q want array", s.Strategy)
			}
		case workflows.StepKindConditional:
			hasConditional = true
		case workflows.StepKindNotify:
			hasNotify = true
		}
	}
	if !hasHTTP {
		t.Error("expected at least one http_request step")
	}
	if !hasAggregate {
		t.Error("expected aggregate step")
	}
	if !hasConditional {
		t.Error("expected conditional step")
	}
	if !hasNotify {
		t.Error("expected notify step")
	}
}

// ---------------------------------------------------------------------------
// TestDocGenerator
// ---------------------------------------------------------------------------

func TestDocGenerator(t *testing.T) {
	w := loadBuiltinFile(t, "doc_generator.yaml")

	if w.ID != "doc_generator" {
		t.Errorf("id: got %q want %q", w.ID, "doc_generator")
	}

	// Inputs: pkg_path.
	if len(w.Inputs) == 0 {
		t.Fatal("inputs must not be empty")
	}
	var pkgInput *workflows.Input
	for i := range w.Inputs {
		if w.Inputs[i].Name == "pkg_path" {
			pkgInput = &w.Inputs[i]
		}
	}
	if pkgInput == nil {
		t.Fatal("input pkg_path not found")
	}

	// Required steps.
	stepNames := make(map[string]bool)
	for _, s := range w.Steps {
		stepNames[s.Name] = true
	}
	required := []string{"list_files", "aggregate_source", "synthesize_readme", "save"}
	for _, name := range required {
		if !stepNames[name] {
			t.Errorf("expected step %q not found", name)
		}
	}

	// list_files must be a shell step.
	lf := findStep(t, w, "list_files")
	if lf.Kind != workflows.StepKindShell {
		t.Errorf("list_files kind: got %q want shell", lf.Kind)
	}

	// aggregate_source must use concat strategy.
	agg := findStep(t, w, "aggregate_source")
	if agg.Kind != workflows.StepKindAggregate {
		t.Errorf("aggregate_source kind: got %q want aggregate", agg.Kind)
	}
	if agg.Strategy != "concat" {
		t.Errorf("aggregate_source strategy: got %q want concat", agg.Strategy)
	}

	// save must be write_artifact with text/markdown mime.
	save := findStep(t, w, "save")
	if save.Kind != workflows.StepKindWriteArtifact {
		t.Errorf("save kind: got %q want write_artifact", save.Kind)
	}
	if save.MimeType != "text/markdown" {
		t.Errorf("save mime_type: got %q want text/markdown", save.MimeType)
	}

	// synthesize_readme must be a model_turn.
	synth := findStep(t, w, "synthesize_readme")
	if synth.Kind != workflows.StepKindModelTurn {
		t.Errorf("synthesize_readme kind: got %q want model_turn", synth.Kind)
	}
}
