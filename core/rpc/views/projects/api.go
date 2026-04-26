// Package projects is the view-scoped accessor for the Project entity.
// Wire shapes are deliberately small so the JSON payload that crosses
// the Wails boundary stays compact.
package projects

import "context"

// Project is the wire shape for a project entity. Timestamps render as
// RFC3339Nano UTC for byte-stable JSON.
type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// Session is the wire shape for a session record returned by
// ListSessions. Mirrors the sessions view's Session subset; duplicated
// here to avoid a cross-view import chain.
//
// LastActiveAt surfaces the session's most recent activity stamp so
// the project landing page can render an "ordered by last-active"
// table without a second round-trip per row (WP07 T002).
type Session struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ProjectID    string `json:"projectId,omitempty"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	LastActiveAt string `json:"lastActiveAt,omitempty"`
}

// ProjectsAPI is the view-scoped surface backing /projects.
type ProjectsAPI interface {
	List(ctx context.Context) ([]Project, error)
	Get(ctx context.Context, id string) (Project, error)
	Create(ctx context.Context, name, description string) (Project, error)
	Rename(ctx context.Context, id, name string) error
	UpdateDescription(ctx context.Context, id, description string) error
	// Delete removes the project. When deleteSessions is true, every
	// session attached to the project is deleted as part of the same
	// operation; otherwise sessions become loose (project_id NULL via
	// the FK ON DELETE SET NULL).
	Delete(ctx context.Context, id string, deleteSessions bool) error
	AddSession(ctx context.Context, projectID, sessionID string) error
	RemoveSession(ctx context.Context, sessionID string) error
	ListSessions(ctx context.Context, projectID string) ([]Session, error)
}
