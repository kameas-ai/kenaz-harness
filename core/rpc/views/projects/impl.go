package projects

import (
	"context"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
	coreprojects "github.com/sigil-tech/kaneaz-harness/core/projects"
	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// API is the concrete ProjectsAPI implementation. It coordinates the
// project Manager with the session Manager so deletes can opt into
// cascading session removal and ListSessions reads through the
// authoritative session store.
type API struct {
	projects *coreprojects.Manager
	sessions *session.Manager
}

// New constructs the API. Both managers are required; callers should
// gate construction on a non-nil core.
func New(projects *coreprojects.Manager, sessions *session.Manager) *API {
	if projects == nil {
		panic("rpc/projects: nil project manager")
	}
	if sessions == nil {
		panic("rpc/projects: nil session manager")
	}
	return &API{projects: projects, sessions: sessions}
}

func projectToView(p coreprojects.Project) Project {
	return Project{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		CreatedAt:   p.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:   p.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func sessionToView(r session.Record) Session {
	out := Session{
		ID:        r.ID,
		Name:      r.Name,
		CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: r.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if !r.LastActiveAt.IsZero() {
		out.LastActiveAt = r.LastActiveAt.UTC().Format(time.RFC3339Nano)
	}
	if r.ProjectID != nil {
		out.ProjectID = *r.ProjectID
	}
	return out
}

// List implements ProjectsAPI.
func (a *API) List(ctx context.Context) ([]Project, error) {
	rows, err := a.projects.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(rows))
	for _, p := range rows {
		out = append(out, projectToView(p))
	}
	return out, nil
}

// Get implements ProjectsAPI.
func (a *API) Get(ctx context.Context, id string) (Project, error) {
	p, err := a.projects.Get(ctx, id)
	if err != nil {
		return Project{}, err
	}
	return projectToView(p), nil
}

// Create implements ProjectsAPI.
func (a *API) Create(ctx context.Context, name, description string) (Project, error) {
	p, err := a.projects.Create(ctx, name, description)
	if err != nil {
		return Project{}, err
	}
	return projectToView(p), nil
}

// Rename implements ProjectsAPI.
func (a *API) Rename(ctx context.Context, id, name string) error {
	return a.projects.Rename(ctx, id, name)
}

// UpdateDescription implements ProjectsAPI.
func (a *API) UpdateDescription(ctx context.Context, id, description string) error {
	return a.projects.UpdateDescription(ctx, id, description)
}

// Delete implements ProjectsAPI. When deleteSessions is true, every
// session attached to the project is removed before the project row
// itself; otherwise sessions become loose via the FK ON DELETE SET NULL.
func (a *API) Delete(ctx context.Context, id string, deleteSessions bool) error {
	if deleteSessions {
		records, err := a.sessions.ListByProject(ctx, id)
		if err != nil {
			return err
		}
		for _, r := range records {
			if err := a.sessions.Delete(ctx, r.ID); err != nil {
				return err
			}
		}
	}
	return a.projects.Delete(ctx, id)
}

// AddSession implements ProjectsAPI by setting the session's project
// id. Returns ErrNotFound from the project store if projectID is
// invalid (caller-side guard) and ErrSessionNotFound if the session
// does not exist.
func (a *API) AddSession(ctx context.Context, projectID, sessionID string) error {
	if _, err := a.projects.Get(ctx, projectID); err != nil {
		return err
	}
	return a.sessions.MoveToProject(ctx, sessionID, &projectID)
}

// RemoveSession implements ProjectsAPI by clearing the session's
// project id (making the session loose).
func (a *API) RemoveSession(ctx context.Context, sessionID string) error {
	return a.sessions.MoveToProject(ctx, sessionID, nil)
}

// ListSessions implements ProjectsAPI.
func (a *API) ListSessions(ctx context.Context, projectID string) ([]Session, error) {
	records, err := a.sessions.ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(records))
	for _, r := range records {
		out = append(out, sessionToView(r))
	}
	return out, nil
}

// LoadAutonomyProfile implements ProjectsAPI.
func (a *API) LoadAutonomyProfile(ctx context.Context, projectID string) (autonomy.Layer, error) {
	return a.projects.GetAutonomyProfile(ctx, projectID)
}

// SaveAutonomyProfile implements ProjectsAPI.
func (a *API) SaveAutonomyProfile(ctx context.Context, projectID string, layer autonomy.Layer) error {
	return a.projects.SetAutonomyProfile(ctx, projectID, layer)
}

// Compile-time witness.
var _ ProjectsAPI = (*API)(nil)
