package agentgraph

import (
	"context"
	"errors"
)

// Impl is the concrete API implementation backed by a Manager.
type Impl struct {
	mgr *Manager
}

// New constructs the API. nil manager is allowed; methods return
// ErrManagerUnavailable so the chassis can boot without a wired
// kernel + the frontend renders an empty state.
func New(mgr *Manager) *Impl { return &Impl{mgr: mgr} }

// ErrManagerUnavailable signals the chassis booted without the manager
// wired. The empty-state card on the frontend is the user-visible
// behaviour.
var ErrManagerUnavailable = errors.New("agentgraph: manager unavailable")

// ListGraphs implements API.
func (a *Impl) ListGraphs(_ context.Context, scope GraphScope) ([]GraphInfo, error) {
	if a == nil || a.mgr == nil {
		return []GraphInfo{}, ErrManagerUnavailable
	}
	return a.mgr.listLibrary(scope), nil
}

// LoadGraph implements API.
func (a *Impl) LoadGraph(_ context.Context, id string) (GraphSpec, error) {
	if a == nil || a.mgr == nil {
		return GraphSpec{}, ErrManagerUnavailable
	}
	return a.mgr.loadGraph(id)
}

// SaveGraph implements API.
func (a *Impl) SaveGraph(_ context.Context, spec GraphSpec) error {
	if a == nil || a.mgr == nil {
		return ErrManagerUnavailable
	}
	return a.mgr.saveGraph(spec)
}

// DeleteGraph implements API.
func (a *Impl) DeleteGraph(_ context.Context, id string) error {
	if a == nil || a.mgr == nil {
		return ErrManagerUnavailable
	}
	return a.mgr.deleteGraph(id)
}

// Validate implements API.
func (a *Impl) Validate(_ context.Context, yaml string) (ValidationResult, error) {
	if a == nil || a.mgr == nil {
		return ValidationResult{}, ErrManagerUnavailable
	}
	return a.mgr.validateYAML(yaml), nil
}

// StartRun implements API.
func (a *Impl) StartRun(_ context.Context, req StartRunRequest) (StartRunResponse, error) {
	if a == nil || a.mgr == nil {
		return StartRunResponse{}, ErrManagerUnavailable
	}
	return a.mgr.startRun(req)
}

// GetRunStatus implements API.
func (a *Impl) GetRunStatus(_ context.Context, runID string) (RunStatus, error) {
	if a == nil || a.mgr == nil {
		return RunStatus{}, ErrManagerUnavailable
	}
	return a.mgr.runStatus(runID)
}

// GetRunTrace implements API.
func (a *Impl) GetRunTrace(_ context.Context, runID string, since int64) ([]RunTraceEvent, error) {
	if a == nil || a.mgr == nil {
		return []RunTraceEvent{}, ErrManagerUnavailable
	}
	return a.mgr.runTrace(runID, since)
}

// Resume implements API.
func (a *Impl) Resume(_ context.Context, runID, askResponse string) error {
	if a == nil || a.mgr == nil {
		return ErrManagerUnavailable
	}
	return a.mgr.resumeRun(runID, askResponse)
}

// CancelRun implements API.
func (a *Impl) CancelRun(_ context.Context, runID string) error {
	if a == nil || a.mgr == nil {
		return ErrManagerUnavailable
	}
	return a.mgr.cancelRun(runID)
}

// MaterializeRun implements API.
func (a *Impl) MaterializeRun(_ context.Context, runID string) (GraphSpec, error) {
	if a == nil || a.mgr == nil {
		return GraphSpec{}, ErrManagerUnavailable
	}
	return a.mgr.materializeRun(runID)
}

// Compile-time witness.
var _ API = (*Impl)(nil)
