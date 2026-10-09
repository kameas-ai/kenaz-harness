package projects

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

// GetToolExposure implements ProjectsAPI.
func (a *API) GetToolExposure(ctx context.Context, projectID string) (toolexposure.Exposure, error) {
	return a.projects.ProjectToolExposure(ctx, projectID)
}

// SetToolExposure implements ProjectsAPI.
func (a *API) SetToolExposure(ctx context.Context, projectID string, e toolexposure.Exposure) error {
	return a.projects.SetToolExposure(ctx, projectID, e)
}
