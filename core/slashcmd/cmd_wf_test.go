package slashcmd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeWorkflows is a deterministic WorkflowsGateway for tests.
type fakeWorkflows struct {
	mu sync.Mutex

	// Summaries returned by List.
	Summaries []WorkflowSummary
	// ListErr is returned by List when non-nil.
	ListErr error

	// Details maps id → WorkflowDetail for Get.
	Details map[string]WorkflowDetail
	// GetErr is returned by Get when non-nil (for unknown IDs not in Details).
	GetErrFn func(id string) error

	// RunEvents is the slice of events Run streams per workflow id.
	RunEvents map[string][]WorkflowProgressEvent
	// RunErr is returned by Run when non-nil.
	RunErr error
	// RunCalls records each (id, inputs, opts) tuple.
	RunCalls []struct {
		ID     string
		Inputs map[string]string
		Opts   WorkflowRunOptions
	}
}

func (f *fakeWorkflows) List(_ context.Context) ([]WorkflowSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	return append([]WorkflowSummary(nil), f.Summaries...), nil
}

func (f *fakeWorkflows) Get(_ context.Context, id string) (WorkflowDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.GetErrFn != nil {
		if err := f.GetErrFn(id); err != nil {
			return WorkflowDetail{}, err
		}
	}
	if f.Details != nil {
		if d, ok := f.Details[id]; ok {
			return d, nil
		}
	}
	return WorkflowDetail{}, errors.New("workflow not found: " + id)
}

func (f *fakeWorkflows) Run(_ context.Context, id string, inputs map[string]string, opts WorkflowRunOptions) (<-chan WorkflowProgressEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RunCalls = append(f.RunCalls, struct {
		ID     string
		Inputs map[string]string
		Opts   WorkflowRunOptions
	}{id, inputs, opts})
	if f.RunErr != nil {
		return nil, f.RunErr
	}
	events := f.RunEvents[id]
	ch := make(chan WorkflowProgressEvent, len(events)+1)
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

// --- /wf list tests ---

func TestWf_NoArg_ListsWorkflows(t *testing.T) {
	t.Parallel()
	wf := &fakeWorkflows{
		Summaries: []WorkflowSummary{
			{ID: "wf-alpha", Name: "Alpha", Description: "First workflow"},
			{ID: "wf-beta", Name: "Beta", Description: "Second workflow"},
			{ID: "wf-gamma", Name: "Gamma", Description: "Third workflow"},
		},
	}
	r, _ := NewRegistry(Deps{Workflows: wf})
	res, err := r.Execute(context.Background(), "sess-1", "/wf")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	for _, id := range []string{"wf-alpha", "wf-beta", "wf-gamma"} {
		if !strings.Contains(res.Text, id) {
			t.Errorf("Text missing workflow id %q: %q", id, res.Text)
		}
	}
	if !strings.Contains(res.Text, "/wf <id>") {
		t.Errorf("Text missing footer hint: %q", res.Text)
	}
}

func TestWf_NoArg_EmptyCatalog(t *testing.T) {
	t.Parallel()
	wf := &fakeWorkflows{Summaries: []WorkflowSummary{}}
	r, _ := NewRegistry(Deps{Workflows: wf})
	res, _ := r.Execute(context.Background(), "sess-1", "/wf")
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	if !strings.Contains(strings.ToLower(res.Text), "no workflows") {
		t.Errorf("Text should mention no workflows: %q", res.Text)
	}
}

// --- /wf <id> immediate dispatch (no required inputs) ---

func TestWf_KnownID_NoRequiredInputs_Dispatches(t *testing.T) {
	t.Parallel()
	wf := &fakeWorkflows{
		Details: map[string]WorkflowDetail{
			"wf-alpha": {ID: "wf-alpha", Name: "Alpha", Inputs: []WorkflowInput{}},
		},
		RunEvents: map[string][]WorkflowProgressEvent{
			"wf-alpha": {
				{RunID: "run-1", Step: "step1", Status: "running"},
				{RunID: "run-1", Step: "step1", Status: "completed", Output: "all done"},
			},
		},
	}
	r, _ := NewRegistry(Deps{Workflows: wf})
	res, err := r.Execute(context.Background(), "sess-1", "/wf wf-alpha")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	if !strings.Contains(res.Text, "Alpha") {
		t.Errorf("Text missing workflow name: %q", res.Text)
	}
	// Verify all progress events appear in output in order.
	if !strings.Contains(res.Text, "running") {
		t.Errorf("Text missing running event: %q", res.Text)
	}
	if !strings.Contains(res.Text, "all done") {
		t.Errorf("Text missing completed output: %q", res.Text)
	}
	// Verify dispatch was called once with Inline=true.
	if len(wf.RunCalls) != 1 {
		t.Fatalf("Run called %d times, want 1", len(wf.RunCalls))
	}
	if !wf.RunCalls[0].Opts.Inline {
		t.Errorf("Run called without Inline=true")
	}
	if wf.RunCalls[0].ID != "wf-alpha" {
		t.Errorf("Run called with id=%q, want wf-alpha", wf.RunCalls[0].ID)
	}
}

// --- /wf <id> with required input → prompt ---

func TestWf_KnownID_RequiredInput_PromptsUser(t *testing.T) {
	t.Parallel()
	wf := &fakeWorkflows{
		Details: map[string]WorkflowDetail{
			"wf-prompt": {
				ID:   "wf-prompt",
				Name: "Prompter",
				Inputs: []WorkflowInput{
					{Name: "query", Required: true, Default: ""},
				},
			},
		},
	}
	r, _ := NewRegistry(Deps{Workflows: wf})
	res, err := r.Execute(context.Background(), "sess-1", "/wf wf-prompt")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info (prompt is friendly)", res.Kind)
	}
	// Should prompt for the missing input.
	if !strings.Contains(res.Text, "query") {
		t.Errorf("Text should mention missing input 'query': %q", res.Text)
	}
	// No dispatch should have happened.
	if len(wf.RunCalls) != 0 {
		t.Errorf("Run should not have been called when input is missing; got %d calls", len(wf.RunCalls))
	}
}

func TestWf_KnownID_RequiredInputWithDefault_Dispatches(t *testing.T) {
	t.Parallel()
	wf := &fakeWorkflows{
		Details: map[string]WorkflowDetail{
			"wf-defaulted": {
				ID:   "wf-defaulted",
				Name: "Defaulted",
				Inputs: []WorkflowInput{
					{Name: "mode", Required: true, Default: "fast"},
				},
			},
		},
		RunEvents: map[string][]WorkflowProgressEvent{
			"wf-defaulted": {
				{RunID: "run-2", Step: "exec", Status: "completed", Output: "ok"},
			},
		},
	}
	r, _ := NewRegistry(Deps{Workflows: wf})
	res, err := r.Execute(context.Background(), "sess-1", "/wf wf-defaulted")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	// Should have dispatched since required input has a default.
	if len(wf.RunCalls) != 1 {
		t.Fatalf("Run called %d times, want 1", len(wf.RunCalls))
	}
}

// --- /wf <unknown-id> → error ---

func TestWf_UnknownID_ErrorMessage(t *testing.T) {
	t.Parallel()
	wf := &fakeWorkflows{
		Details: map[string]WorkflowDetail{},
	}
	r, _ := NewRegistry(Deps{Workflows: wf})
	res, _ := r.Execute(context.Background(), "sess-1", "/wf no-such-wf")
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !strings.Contains(res.Text, "no-such-wf") {
		t.Errorf("Text should contain the unknown id: %q", res.Text)
	}
	if !strings.Contains(res.Text, "/wf") {
		t.Errorf("Text should suggest /wf to list: %q", res.Text)
	}
}

// --- nil gateway → "not wired" ---

func TestWf_NilGateway_NotWiredMessage(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{}) // no Workflows set
	res, _ := r.Execute(context.Background(), "sess-1", "/wf")
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !strings.Contains(res.Text, "not wired") {
		t.Errorf("Text missing not-wired hint: %q", res.Text)
	}
}

func TestWf_NilGateway_WithID_NoCrash(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	res, _ := r.Execute(context.Background(), "sess-1", "/wf wf-alpha")
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !strings.Contains(res.Text, "not wired") {
		t.Errorf("Text missing not-wired hint: %q", res.Text)
	}
}

// --- progress event ordering ---

func TestWf_ProgressEventsForwardedInOrder(t *testing.T) {
	t.Parallel()
	wf := &fakeWorkflows{
		Details: map[string]WorkflowDetail{
			"wf-seq": {ID: "wf-seq", Name: "Seq"},
		},
		RunEvents: map[string][]WorkflowProgressEvent{
			"wf-seq": {
				{RunID: "r", Step: "s1", Status: "running"},
				{RunID: "r", Step: "s1", Status: "completed", Output: "output-s1"},
				{RunID: "r", Step: "s2", Status: "running"},
				{RunID: "r", Step: "s2", Status: "completed", Output: "output-s2"},
			},
		},
	}
	r, _ := NewRegistry(Deps{Workflows: wf})
	res, err := r.Execute(context.Background(), "sess-1", "/wf wf-seq")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Verify both step outputs appear in the rendered text.
	for _, want := range []string{"output-s1", "output-s2"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("Text missing %q: %q", want, res.Text)
		}
	}
	// Verify order: s1 output before s2 output.
	if strings.Index(res.Text, "output-s1") >= strings.Index(res.Text, "output-s2") {
		t.Errorf("Events appear out of order in: %q", res.Text)
	}
}

// --- failed run ---

func TestWf_FailedRun_ErrorKind(t *testing.T) {
	t.Parallel()
	wf := &fakeWorkflows{
		Details: map[string]WorkflowDetail{
			"wf-fail": {ID: "wf-fail", Name: "Failer"},
		},
		RunEvents: map[string][]WorkflowProgressEvent{
			"wf-fail": {
				{RunID: "r", Step: "boom", Status: "failed", Err: "boom error"},
			},
		},
	}
	r, _ := NewRegistry(Deps{Workflows: wf})
	res, _ := r.Execute(context.Background(), "sess-1", "/wf wf-fail")
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error on failed run", res.Kind)
	}
	if !strings.Contains(res.Text, "boom error") {
		t.Errorf("Text missing error detail: %q", res.Text)
	}
}
