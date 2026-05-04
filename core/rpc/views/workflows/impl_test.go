package workflows

import (
	"context"
	"errors"
	"testing"

	corewf "github.com/sigil-tech/kaneaz-harness/core/workflows"
)

func newTestAPI(t *testing.T) *API {
	t.Helper()
	wfs, errs := corewf.LoadBuiltins()
	for _, e := range errs {
		t.Fatalf("LoadBuiltins: %v", e)
	}
	if len(wfs) == 0 {
		t.Fatal("no builtins")
	}
	return New(Config{Engine: corewf.NewEngine(), Catalog: wfs})
}

func TestList_ReturnsBundledWorkflows(t *testing.T) {
	api := newTestAPI(t)
	got, err := api.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected at least one bundled workflow")
	}
	found := false
	for _, s := range got {
		if s.ID == "plan_implement_review" {
			found = true
			if s.StepCount == 0 || s.Name == "" {
				t.Errorf("summary fields not populated: %+v", s)
			}
		}
	}
	if !found {
		t.Errorf("plan_implement_review not in catalog")
	}
}

func TestGet_ReturnsFullWorkflow(t *testing.T) {
	api := newTestAPI(t)
	w, err := api.Get(context.Background(), "plan_implement_review")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(w.Steps) != 4 {
		t.Errorf("steps: got %d want 4", len(w.Steps))
	}
}

func TestGet_UnknownIDErrors(t *testing.T) {
	api := newTestAPI(t)
	_, err := api.Get(context.Background(), "no-such-flow")
	if !errors.Is(err, corewf.ErrWorkflowNotFound) {
		t.Errorf("want ErrWorkflowNotFound, got %v", err)
	}
}

func TestRun_ExecutesAllSteps(t *testing.T) {
	api := newTestAPI(t)
	res, err := api.Run(context.Background(), "plan_implement_review", map[string]string{
		"task": "Build a feature",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "completed" {
		t.Errorf("status: got %q want completed (err=%s)", res.Status, res.Err)
	}
	if len(res.Steps) != 4 {
		t.Errorf("steps: got %d want 4", len(res.Steps))
	}
	for _, s := range res.Steps {
		if s.Status != "completed" {
			t.Errorf("step %s status: %s", s.Name, s.Status)
		}
		if s.Output == "" {
			t.Errorf("step %s: empty output", s.Name)
		}
	}
}

type recordingPublisher struct{ events []any }

func (r *recordingPublisher) Publish(_ string, payload any) {
	r.events = append(r.events, payload)
}

func TestRun_PublishesProgress(t *testing.T) {
	wfs, _ := corewf.LoadBuiltins()
	pub := &recordingPublisher{}
	api := New(Config{Engine: corewf.NewEngine(), Catalog: wfs, Publisher: pub})
	_, err := api.Run(context.Background(), "plan_implement_review", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 4 steps × 2 transitions (running + completed) = 8 events.
	if len(pub.events) != 8 {
		t.Errorf("progress events: got %d want 8", len(pub.events))
	}
}

func TestDisabled_BlocksAllMethods(t *testing.T) {
	api := New(Config{Disabled: true})
	if _, err := api.List(context.Background()); !errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("List: want ErrFeatureDisabled got %v", err)
	}
	if _, err := api.Get(context.Background(), "x"); !errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("Get: want ErrFeatureDisabled got %v", err)
	}
	if _, err := api.Run(context.Background(), "x", nil); !errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("Run: want ErrFeatureDisabled got %v", err)
	}
}

func TestRun_NilEngineErrors(t *testing.T) {
	wfs, _ := corewf.LoadBuiltins()
	api := New(Config{Catalog: wfs})
	_, err := api.Run(context.Background(), "plan_implement_review", nil)
	if !errors.Is(err, ErrEngineUnavailable) {
		t.Errorf("want ErrEngineUnavailable, got %v", err)
	}
}
