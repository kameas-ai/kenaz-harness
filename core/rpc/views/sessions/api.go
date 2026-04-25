// Package sessions defines the SessionsAPI view-scoped accessor on the
// HarnessAPI surface. Frontend mission delivers the interface shape; the
// concrete implementation is wired by the sessions feature mission.
package sessions

import "context"

// Session is the lightweight session metadata the rail consumes.
type Session struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// SessionsAPI is the view-scoped accessor for session CRUD + streams.
// Implementations MUST be safe for concurrent use.
type SessionsAPI interface {
	List(ctx context.Context) ([]Session, error)
	Get(ctx context.Context, id string) (Session, error)
	Create(ctx context.Context, name string) (Session, error)
	Rename(ctx context.Context, id, name string) error
	Delete(ctx context.Context, id string) error
	Reorder(ctx context.Context, ids []string) error
	StartStream(ctx context.Context, id string) (subscriptionID string, err error)
	StopStream(ctx context.Context, subscriptionID string) error
}
