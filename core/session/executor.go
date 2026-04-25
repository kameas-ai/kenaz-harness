package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/event"
)

// Executor drives the agent execution loop for a Session (the
// SpecExec-side type from spec.go). The chat-rail facade for session
// CRUD lives on Manager (manager.go); the two surfaces deliberately
// stay separate so the rail does not transitively depend on
// llm/mcp/bundle wiring.
type Executor interface {
	Start(ctx context.Context, spec Spec) (*Session, error)
	Resume(ctx context.Context, sessionID string) (*Session, error)
	Send(ctx context.Context, sessionID, input string) error
	Interrupt(ctx context.Context, sessionID string) error
	Subscribe(ctx context.Context, sessionID string, fromSeq int64) (<-chan event.Event, error)
	Get(ctx context.Context, sessionID string) (*Session, error)
	List(ctx context.Context) ([]Session, error)
}
