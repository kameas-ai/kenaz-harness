package projects

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

// SetToolExposure implements ProjectsAPI.
func (a *API) SetToolExposure(ctx context.Context, projectID string, e toolexposure.Exposure) error {
	return a.projects.SetToolExposure(ctx, projectID, e)
}
