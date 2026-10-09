package sessions

import (
	"context"
	"errors"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
	"github.com/kameas-ai/kenaz-harness/core/tools/loadtools"
)

// SessionToolExposure is the Sessions_GetToolExposure wire shape: the
// session's override layer and its activated tool set
// (tool-context-budget-01TCBUD01 §2.1 step 2, §2.2).
type SessionToolExposure struct {
	Exposure    toolexposure.Exposure     `json:"exposure"`
	Activations []toolexposure.Activation `json:"activations"`
}

// ToolLoader activates tools for a session; *loadtools.Service
// implements it. It is the same core kenaz__load_tools calls.
type ToolLoader interface {
	Load(ctx context.Context, sessionID string, req loadtools.Request, by string) (loadtools.Result, error)
}

// ErrToolLoadingNotConfigured is returned by LoadTools when the chassis
// wired no ToolLoader (the nil-core chassis).
var ErrToolLoadingNotConfigured = errors.New("rpc/sessions: tool loading not configured")

// WithToolLoading wires the tool loader behind LoadTools and the write
// guard SetToolExposure consults after validation (spec FR-E3). Returns
// api unchanged when it is not a *managerAPI.
func WithToolLoading(api SessionsAPI, loader ToolLoader, guard toolexposure.WriteGuard) SessionsAPI {
	if m, ok := api.(*managerAPI); ok {
		m.toolLoader = loader
		m.exposureGuard = guard
	}
	return api
}

// GetToolExposure implements SessionsAPI.
func (a *managerAPI) GetToolExposure(ctx context.Context, id string) (SessionToolExposure, error) {
	st, err := a.mgr.SessionToolExposure(ctx, id)
	if err != nil {
		return SessionToolExposure{}, err
	}
	out := SessionToolExposure{Exposure: st.Override, Activations: st.Activations}
	if out.Activations == nil {
		out.Activations = []toolexposure.Activation{}
	}
	return out, nil
}

// SetToolExposure implements SessionsAPI.
func (a *managerAPI) SetToolExposure(ctx context.Context, id string, e toolexposure.Exposure) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if a.exposureGuard != nil {
		if err := a.exposureGuard.CheckLayerWrite(ctx, toolexposure.LayerWrite{
			Level: toolexposure.LevelSession, SessionID: id, Exposure: e,
		}); err != nil {
			return err
		}
	}
	return a.mgr.SetToolExposure(ctx, id, e)
}

// LoadTools implements SessionsAPI: the human path to the activation
// kenaz__load_tools performs for the model, recorded with by "user".
func (a *managerAPI) LoadTools(ctx context.Context, id string, servers, tools []string, sticky bool) (loadtools.Result, error) {
	if a.toolLoader == nil {
		return loadtools.Result{}, ErrToolLoadingNotConfigured
	}
	return a.toolLoader.Load(ctx, id, loadtools.Request{Servers: servers, Tools: tools, Sticky: sticky}, audit.ToolsActivatedByUser)
}
