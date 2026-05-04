// Concrete WorkflowsAPI implementation backed by core/workflows.
package workflows

import (
	"context"
	"errors"
	"fmt"
	"sync"

	corewf "github.com/sigil-tech/kaneaz-harness/core/workflows"
)

// ErrEngineUnavailable is returned when the chassis booted without
// the workflows engine wired (e.g. test harness rpc.New(nil)).
var ErrEngineUnavailable = errors.New("workflows: engine unavailable")

// ErrFeatureDisabled is returned when HARNESS_WORKFLOWS=off.
var ErrFeatureDisabled = errors.New("workflows: feature disabled")

// ProgressPublisher is the interface the API uses to fan progress
// events onto the broker. Decoupled from rpc.StreamBroker so the
// view stays import-clean.
type ProgressPublisher interface {
	Publish(topic string, payload any)
}

// Config bundles the dependencies the impl needs.
type Config struct {
	Engine    *corewf.Engine
	Catalog   []corewf.Workflow
	Publisher ProgressPublisher
	Disabled  bool
}

// API is the concrete WorkflowsAPI.
type API struct {
	cfg     Config
	mu      sync.RWMutex
	byID    map[string]corewf.Workflow
}

// New returns a real-engine-backed API. A nil engine returns a
// graceful-empty surface (List returns the catalog, Get/Run return
// ErrEngineUnavailable).
func New(cfg Config) *API {
	a := &API{cfg: cfg, byID: make(map[string]corewf.Workflow, len(cfg.Catalog))}
	for _, w := range cfg.Catalog {
		a.byID[w.ID] = w
	}
	return a
}

// List implements WorkflowsAPI.
func (a *API) List(_ context.Context) ([]Summary, error) {
	if a == nil || a.cfg.Disabled {
		return nil, ErrFeatureDisabled
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Summary, 0, len(a.byID))
	for _, w := range a.byID {
		out = append(out, Summary{
			ID:          w.ID,
			Name:        w.Name,
			Description: w.Description,
			Version:     w.Version,
			StepCount:   len(w.Steps),
			Source:      "builtin",
		})
	}
	return out, nil
}

// Get implements WorkflowsAPI.
func (a *API) Get(_ context.Context, id string) (Workflow, error) {
	if a == nil || a.cfg.Disabled {
		return Workflow{}, ErrFeatureDisabled
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	w, ok := a.byID[id]
	if !ok {
		return Workflow{}, fmt.Errorf("%w: %s", corewf.ErrWorkflowNotFound, id)
	}
	return projectWorkflow(w), nil
}

// Run implements WorkflowsAPI.
func (a *API) Run(ctx context.Context, id string, inputs map[string]string) (RunResult, error) {
	if a == nil || a.cfg.Disabled {
		return RunResult{}, ErrFeatureDisabled
	}
	if a.cfg.Engine == nil {
		return RunResult{}, ErrEngineUnavailable
	}
	a.mu.RLock()
	w, ok := a.byID[id]
	a.mu.RUnlock()
	if !ok {
		return RunResult{}, fmt.Errorf("%w: %s", corewf.ErrWorkflowNotFound, id)
	}
	typed := make(map[string]corewf.TypedValue, len(inputs))
	for k, v := range inputs {
		typed[k] = corewf.TypedValue{Type: corewf.ValueTypeText, Text: v}
	}

	pub := a.cfg.Publisher
	opts := corewf.RunOptions{}
	if pub != nil {
		opts.ProgressSink = func(ev corewf.ProgressEvent) {
			pub.Publish("workflows:run-progress", ev)
		}
	}
	run, err := a.cfg.Engine.Run(ctx, w, typed, opts)
	res := RunResult{
		RunID:      run.ID,
		WorkflowID: run.WorkflowID,
		Status:     run.Status,
		Steps:      make([]StepRun, 0, len(run.Steps)),
	}
	for _, s := range run.Steps {
		res.Steps = append(res.Steps, StepRun{
			Name: s.Name, Kind: string(s.Kind), Status: s.Status,
			Output: s.Output, Err: s.Err,
		})
	}
	if err != nil {
		res.Err = err.Error()
	}
	return res, nil
}

func projectWorkflow(w corewf.Workflow) Workflow {
	out := Workflow{
		ID: w.ID, Name: w.Name, Description: w.Description, Version: w.Version,
		Inputs: make([]Input, 0, len(w.Inputs)),
		Steps:  make([]Step, 0, len(w.Steps)),
	}
	for _, in := range w.Inputs {
		out.Inputs = append(out.Inputs, Input{
			Name: in.Name, Kind: string(in.Kind),
			Required: in.Required, Default: in.Default, Options: in.Options,
		})
	}
	for _, st := range w.Steps {
		out.Steps = append(out.Steps, Step{
			Name: st.Name, Kind: string(st.Kind),
			UserPrompt: st.UserPrompt, Cmd: st.Cmd, Args: st.Args,
		})
	}
	return out
}
