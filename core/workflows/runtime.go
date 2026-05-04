package workflows

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// RunContext is the in-memory state of one in-flight workflow run.
// One RunContext per Engine.Run invocation; never shared across runs.
type RunContext struct {
	RunID       string
	Workflow    Workflow
	Inputs      map[string]TypedValue
	StepOutputs map[string]TypedValue
	mu          sync.RWMutex
}

// SetOutput stores a step output. Safe for concurrent reads from
// other goroutines (e.g. the progress emitter).
func (rc *RunContext) SetOutput(name string, v TypedValue) {
	rc.mu.Lock()
	rc.StepOutputs[name] = v
	rc.mu.Unlock()
}

// Engine sequences workflow execution. The beta engine accepts an
// optional Now func so tests can pin timestamps; everything else is
// resolved through the StepRunner registry.
type Engine struct {
	// Runners maps StepKind → StepRunner. nil falls back to the
	// process-wide DefaultRunners().
	Runners map[StepKind]StepRunner
	// Now is a pluggable wall-clock; nil uses time.Now().UTC().
	Now func() time.Time
}

// NewEngine returns an Engine wired with the package-default runners.
func NewEngine() *Engine {
	return &Engine{Runners: DefaultRunners(), Now: func() time.Time { return time.Now().UTC() }}
}

// runner returns the registered StepRunner for kind, falling back to
// the package defaults if e.Runners doesn't contain it.
func (e *Engine) runner(kind StepKind) (StepRunner, bool) {
	if r, ok := e.Runners[kind]; ok {
		return r, true
	}
	if r, ok := DefaultRunners()[kind]; ok {
		return r, true
	}
	return nil, false
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}

// Run executes wf sequentially. It always returns a non-nil *Run; the
// returned error mirrors run.Err for convenience but the caller can
// inspect run.Steps to find the failing step.
//
// Cancellation: when ctx is cancelled mid-step, the run aborts after
// the in-flight step's Run returns; status is set to "interrupted"
// and remaining steps are marked "skipped".
func (e *Engine) Run(ctx context.Context, wf Workflow, inputs map[string]TypedValue, opts RunOptions) (*Run, error) {
	if err := Validate(wf); err != nil {
		return &Run{
			ID:         randomRunID(),
			WorkflowID: wf.ID,
			Status:     "failed",
			StartedAt:  e.now(),
			EndedAt:    e.now(),
			Err:        err.Error(),
		}, err
	}
	rc := &RunContext{
		RunID:       randomRunID(),
		Workflow:    wf,
		Inputs:      mergeInputDefaults(wf, inputs),
		StepOutputs: make(map[string]TypedValue, len(wf.Steps)),
	}
	run := &Run{
		ID:         rc.RunID,
		WorkflowID: wf.ID,
		Status:     "running",
		StartedAt:  e.now(),
		Steps:      make([]StepResult, 0, len(wf.Steps)),
	}
	emit := func(ev ProgressEvent) {
		if opts.ProgressSink == nil {
			return
		}
		// Fire-and-forget; the sink owns its own backpressure.
		opts.ProgressSink(ev)
	}

	for i, st := range wf.Steps {
		select {
		case <-ctx.Done():
			run.Status = "interrupted"
			run.Err = ErrCancelled.Error()
			// Mark remaining steps as skipped.
			for j := i; j < len(wf.Steps); j++ {
				run.Steps = append(run.Steps, StepResult{
					Name:   wf.Steps[j].Name,
					Kind:   wf.Steps[j].Kind,
					Status: "skipped",
				})
			}
			run.EndedAt = e.now()
			return run, ctx.Err()
		default:
		}

		stepStart := e.now()
		emit(ProgressEvent{RunID: rc.RunID, WorkflowID: wf.ID, Step: st.Name, Kind: st.Kind, Status: "running", At: stepStart})

		runner, ok := e.runner(st.Kind)
		if !ok {
			err := fmt.Errorf("%w: %s", ErrUnknownStepKind, st.Kind)
			run.Steps = append(run.Steps, StepResult{
				Name: st.Name, Kind: st.Kind, Status: "failed",
				StartedAt: stepStart, EndedAt: e.now(), Err: err.Error(),
			})
			run.Status = "failed"
			run.Err = err.Error()
			run.EndedAt = e.now()
			emit(ProgressEvent{RunID: rc.RunID, WorkflowID: wf.ID, Step: st.Name, Kind: st.Kind, Status: "failed", Err: err.Error(), At: e.now()})
			return run, err
		}

		// Resolve refs in any user-text fields on a per-step copy so
		// the StepRunner sees fully-substituted strings.
		expanded, err := expandStep(st, rc)
		if err != nil {
			run.Steps = append(run.Steps, StepResult{
				Name: st.Name, Kind: st.Kind, Status: "failed",
				StartedAt: stepStart, EndedAt: e.now(), Err: err.Error(),
			})
			run.Status = "failed"
			run.Err = err.Error()
			run.EndedAt = e.now()
			emit(ProgressEvent{RunID: rc.RunID, WorkflowID: wf.ID, Step: st.Name, Kind: st.Kind, Status: "failed", Err: err.Error(), At: e.now()})
			return run, err
		}

		out, err := runner.Run(ctx, expanded, rc)
		stepEnd := e.now()
		if err != nil {
			run.Steps = append(run.Steps, StepResult{
				Name: st.Name, Kind: st.Kind, Status: "failed",
				StartedAt: stepStart, EndedAt: stepEnd, Err: err.Error(),
				Output: out.Text,
			})
			run.Status = "failed"
			run.Err = err.Error()
			run.EndedAt = stepEnd
			emit(ProgressEvent{RunID: rc.RunID, WorkflowID: wf.ID, Step: st.Name, Kind: st.Kind, Status: "failed", Err: err.Error(), At: stepEnd})
			return run, err
		}
		rc.SetOutput(st.Name, out)
		run.Steps = append(run.Steps, StepResult{
			Name: st.Name, Kind: st.Kind, Status: "completed",
			StartedAt: stepStart, EndedAt: stepEnd, Output: out.Text,
		})
		emit(ProgressEvent{RunID: rc.RunID, WorkflowID: wf.ID, Step: st.Name, Kind: st.Kind, Status: "completed", Output: out.Text, At: stepEnd})
	}

	run.Status = "completed"
	run.EndedAt = e.now()
	run.Outputs = copyOutputs(rc)
	return run, nil
}

func mergeInputDefaults(wf Workflow, supplied map[string]TypedValue) map[string]TypedValue {
	out := make(map[string]TypedValue, len(wf.Inputs))
	for _, in := range wf.Inputs {
		if v, ok := supplied[in.Name]; ok {
			out[in.Name] = v
			continue
		}
		if in.Default != "" {
			out[in.Name] = TypedValue{Type: ValueTypeText, Text: in.Default}
		}
	}
	// Preserve any extras the caller passed (e.g. ad-hoc values not
	// declared as inputs).
	for k, v := range supplied {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	return out
}

func expandStep(st Step, rc *RunContext) (Step, error) {
	out := st
	var err error
	if out.UserPrompt, err = expandRefs(st.UserPrompt, rc); err != nil {
		return st, err
	}
	if out.URL, err = expandRefs(st.URL, rc); err != nil {
		return st, err
	}
	if out.Body, err = expandRefs(st.Body, rc); err != nil {
		return st, err
	}
	if out.Title, err = expandRefs(st.Title, rc); err != nil {
		return st, err
	}
	if out.ContentRef, err = expandRefs(st.ContentRef, rc); err != nil {
		return st, err
	}
	if out.ArtifactIDRef, err = expandRefs(st.ArtifactIDRef, rc); err != nil {
		return st, err
	}
	if len(st.Args) > 0 {
		out.Args = make([]string, len(st.Args))
		for i, a := range st.Args {
			if out.Args[i], err = expandRefs(a, rc); err != nil {
				return st, err
			}
		}
	}
	return out, nil
}

func copyOutputs(rc *RunContext) map[string]TypedValue {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	out := make(map[string]TypedValue, len(rc.StepOutputs))
	for k, v := range rc.StepOutputs {
		out[k] = v
	}
	return out
}

func randomRunID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// In the (vanishingly unlikely) event the OS RNG fails,
		// fall back to a timestamp-based id rather than panic.
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return "run-" + hex.EncodeToString(b[:])
}

// IsCancelled reports whether err signals a cancelled run.
func IsCancelled(err error) bool {
	return errors.Is(err, ErrCancelled) || errors.Is(err, context.Canceled)
}
