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

// ErrStorageUnavailable is returned by Save/Delete when the chassis
// booted without a Store wired (e.g. test harness without a DB).
var ErrStorageUnavailable = errors.New("workflows: storage unavailable")

// ErrInvalidSaveInput is returned by Save when neither YAML nor
// Workflow is populated on the SaveInput envelope.
var ErrInvalidSaveInput = errors.New("workflows: save requires yaml or workflow")

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
	// Store is the WP06 persistence layer for user-defined workflows.
	// When nil, Save/Delete return ErrStorageUnavailable but the
	// in-memory builtin catalog still works for List/Get/Run.
	Store corewf.Store
}

// API is the concrete WorkflowsAPI.
type API struct {
	cfg  Config
	mu   sync.RWMutex
	byID map[string]corewf.Workflow
	// source tracks per-id provenance ("builtin" | "user") so List
	// surfaces the right tag in the catalog after a Save round-trip.
	source map[string]string
}

// New returns a real-engine-backed API. A nil engine returns a
// graceful-empty surface (List returns the catalog, Get/Run return
// ErrEngineUnavailable).
func New(cfg Config) *API {
	a := &API{
		cfg:    cfg,
		byID:   make(map[string]corewf.Workflow, len(cfg.Catalog)),
		source: make(map[string]string, len(cfg.Catalog)),
	}
	for _, w := range cfg.Catalog {
		a.byID[w.ID] = w
		a.source[w.ID] = "builtin"
	}
	// Hydrate any user workflows that were persisted in prior sessions
	// so the catalog is complete on first List without a manual reload.
	if cfg.Store != nil {
		if summaries, err := cfg.Store.List(context.Background()); err == nil {
			for _, s := range summaries {
				if _, ok := a.byID[s.ID]; ok {
					continue
				}
				w, err := cfg.Store.Load(context.Background(), s.ID)
				if err != nil {
					continue
				}
				a.byID[s.ID] = w
				a.source[s.ID] = "user"
			}
		}
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
		src := a.source[w.ID]
		if src == "" {
			src = "builtin"
		}
		out = append(out, Summary{
			ID:          w.ID,
			Name:        w.Name,
			Description: w.Description,
			Version:     w.Version,
			StepCount:   len(w.Steps),
			Source:      src,
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

// Run implements WorkflowsAPI. Thin shim over RunWithOptions for the
// pre-WP08 callers that don't need inline / skip-cache flags.
func (a *API) Run(ctx context.Context, id string, inputs map[string]string) (RunResult, error) {
	return a.RunWithOptions(ctx, RunRequest{ID: id, Inputs: inputs})
}

// RunWithOptions implements WorkflowsAPI. Honours the inline +
// skip-cache envelope flags introduced by WP08.
//
// Inline path:
//   - Resolves the workflow from the catalog.
//   - Calls workflows.InlineRun, which validates inline_run:true and
//     spins a goroutine that streams ProgressEvents (tagged Inline=true)
//     into the broker on the same `workflows:run-progress` topic.
//   - Returns synchronously after the inline goroutine drains; the
//     RunResult shape mirrors the spawned-session path so the chat
//     renderer doesn't have to fork.
//
// Spawned path (the default): unchanged from the WP07 baseline.
func (a *API) RunWithOptions(ctx context.Context, req RunRequest) (RunResult, error) {
	if a == nil || a.cfg.Disabled {
		return RunResult{}, ErrFeatureDisabled
	}
	if a.cfg.Engine == nil {
		return RunResult{}, ErrEngineUnavailable
	}
	a.mu.RLock()
	w, ok := a.byID[req.ID]
	a.mu.RUnlock()
	if !ok {
		return RunResult{}, fmt.Errorf("%w: %s", corewf.ErrWorkflowNotFound, req.ID)
	}
	typed := make(map[string]corewf.TypedValue, len(req.Inputs))
	for k, v := range req.Inputs {
		typed[k] = corewf.TypedValue{Type: corewf.ValueTypeText, Text: v}
	}

	pub := a.cfg.Publisher

	if req.Inline {
		// Inline dispatch: hand the workflow + loose-typed inputs to
		// InlineRun, drain the channel into the broker (so the chat
		// renderer sees the same event stream the spawned path emits),
		// and project the terminal events into a RunResult.
		loose := make(map[string]any, len(req.Inputs))
		for k, v := range req.Inputs {
			loose[k] = v
		}
		ch, err := corewf.InlineRun(ctx, a.cfg.Engine, w, loose)
		if err != nil {
			return RunResult{}, err
		}
		res := RunResult{WorkflowID: w.ID, Status: "running"}
		stepIdx := make(map[string]int)
		for ev := range ch {
			if pub != nil {
				pub.Publish("workflows:run-progress", ev)
			}
			res.RunID = ev.RunID
			i, ok := stepIdx[ev.Step]
			if !ok {
				i = len(res.Steps)
				res.Steps = append(res.Steps, StepRun{Name: ev.Step, Kind: string(ev.Kind)})
				stepIdx[ev.Step] = i
			}
			res.Steps[i].Status = ev.Status
			if ev.Output != "" {
				res.Steps[i].Output = ev.Output
			}
			if ev.Err != "" {
				res.Steps[i].Err = ev.Err
				res.Err = ev.Err
				res.Status = "failed"
			}
		}
		if res.Status == "running" {
			res.Status = "completed"
		}
		return res, nil
	}

	opts := corewf.RunOptions{SkipCache: req.SkipCache}
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

// Save implements WorkflowsAPI.
//
// Routing:
//   - in.YAML non-empty → ImportYAML (assigns fresh id) then Store.Save
//   - in.Workflow set    → reproject to corewf.Workflow + Store.Save
//     (caller-controlled id; Store dedupes on hash so repeated saves
//     of the same canonical YAML are no-ops)
//
// Either path validates via the storage layer, which calls Validate
// internally. The catalog cache is updated in-place so a follow-on
// List reflects the new record without a chassis restart.
func (a *API) Save(ctx context.Context, in SaveInput) (SaveOutput, error) {
	if a == nil || a.cfg.Disabled {
		return SaveOutput{}, ErrFeatureDisabled
	}
	if a.cfg.Store == nil {
		return SaveOutput{}, ErrStorageUnavailable
	}
	var (
		w   corewf.Workflow
		err error
	)
	switch {
	case in.YAML != "":
		w, err = corewf.ImportYAML([]byte(in.YAML))
		if err != nil {
			return SaveOutput{}, fmt.Errorf("workflows: import yaml: %w", err)
		}
	case in.Workflow != nil:
		w = unprojectWorkflow(*in.Workflow)
	default:
		return SaveOutput{}, ErrInvalidSaveInput
	}
	saved, err := a.cfg.Store.Save(ctx, w)
	if err != nil {
		return SaveOutput{}, err
	}
	a.mu.Lock()
	a.byID[saved.ID] = saved
	a.source[saved.ID] = "user"
	a.mu.Unlock()
	return SaveOutput{
		ID:        saved.ID,
		Name:      saved.Name,
		Version:   saved.Version,
		Hash:      saved.Hash(),
		YAML:      saved.YAMLSource(),
		CreatedAt: saved.CreatedAt().Format("2006-01-02T15:04:05.000Z07:00"),
		UpdatedAt: saved.UpdatedAt().Format("2006-01-02T15:04:05.000Z07:00"),
	}, nil
}

// Delete implements WorkflowsAPI. Returns corewf.ErrWorkflowNotFound
// when the id is unknown so the frontend can render the gone-state
// without an error toast.
func (a *API) Delete(ctx context.Context, id string) error {
	if a == nil || a.cfg.Disabled {
		return ErrFeatureDisabled
	}
	if a.cfg.Store == nil {
		return ErrStorageUnavailable
	}
	// Probe the store first so the user-facing surface returns a typed
	// not-found rather than a silent ok. The storage Delete is idempotent
	// on missing rows, which is the wrong contract for the RPC.
	if _, err := a.cfg.Store.Load(ctx, id); err != nil {
		return err
	}
	if err := a.cfg.Store.Delete(ctx, id); err != nil {
		return err
	}
	a.mu.Lock()
	delete(a.byID, id)
	delete(a.source, id)
	a.mu.Unlock()
	return nil
}

// unprojectWorkflow lifts the wire Workflow back into a corewf.Workflow
// the storage layer can persist. It only copies the fields the wire
// shape carries; the wider corewf.Workflow surface (cwd, env, etc.) is
// left zero so the canonical re-marshal in Store.Save produces a
// minimal YAML payload.
func unprojectWorkflow(w Workflow) corewf.Workflow {
	out := corewf.Workflow{
		ID:          w.ID,
		Name:        w.Name,
		Description: w.Description,
		Version:     w.Version,
		Inputs:      make([]corewf.Input, 0, len(w.Inputs)),
		Steps:       make([]corewf.Step, 0, len(w.Steps)),
	}
	for _, in := range w.Inputs {
		out.Inputs = append(out.Inputs, corewf.Input{
			Name: in.Name, Kind: corewf.InputKind(in.Kind),
			Required: in.Required, Default: in.Default, Options: in.Options,
		})
	}
	for _, st := range w.Steps {
		out.Steps = append(out.Steps, corewf.Step{
			Name: st.Name, Kind: corewf.StepKind(st.Kind),
			UserPrompt: st.UserPrompt, Cmd: st.Cmd, Args: st.Args,
		})
	}
	return out
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
