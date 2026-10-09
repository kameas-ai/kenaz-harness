package sessions

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

// SessionToolExposure is the Sessions_GetToolExposure wire shape: the
// session's override layer and its activated tool set
// (tool-context-budget-01TCBUD01 §2.1 step 2, §2.2).
type SessionToolExposure struct {
	Exposure    toolexposure.Exposure     `json:"exposure"`
	Activations []toolexposure.Activation `json:"activations"`
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
	return a.mgr.SetToolExposure(ctx, id, e)
}
