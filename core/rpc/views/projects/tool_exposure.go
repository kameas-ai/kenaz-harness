package projects

import (
	"context"
	"sync/atomic"

	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

// exposureGuard holds the late-bound write guard for project layers.
type exposureGuard struct {
	g atomic.Pointer[toolexposure.WriteGuard]
}

func (e *exposureGuard) check(ctx context.Context, w toolexposure.LayerWrite) error {
	if p := e.g.Load(); p != nil && *p != nil {
		return (*p).CheckLayerWrite(ctx, w)
	}
	return nil
}

// WithToolExposureGuard installs the guard SetToolExposure consults
// after validation (spec FR-E3: refuse turning kenaz__load_tools off
// while summary tools exist). Returns api unchanged when it is not this
// package's *API.
func WithToolExposureGuard(api ProjectsAPI, g toolexposure.WriteGuard) ProjectsAPI {
	if a, ok := api.(*API); ok && g != nil {
		a.exposureGuard.g.Store(&g)
	}
	return api
}

// GetToolExposure implements ProjectsAPI.
func (a *API) GetToolExposure(ctx context.Context, projectID string) (toolexposure.Exposure, error) {
	return a.projects.ProjectToolExposure(ctx, projectID)
}

// SetToolExposure implements ProjectsAPI.
func (a *API) SetToolExposure(ctx context.Context, projectID string, e toolexposure.Exposure) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if err := a.exposureGuard.check(ctx, toolexposure.LayerWrite{
		Level: toolexposure.LevelProject, ProjectID: projectID, Exposure: e,
	}); err != nil {
		return err
	}
	return a.projects.SetToolExposure(ctx, projectID, e)
}
