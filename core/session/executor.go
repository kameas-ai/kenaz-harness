package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/event"
)

type Executor interface {
	Start(ctx context.Context, spec Spec) (*Session, error)
	Resume(ctx context.Context, sessionID string) (*Session, error)
	Send(ctx context.Context, sessionID, input string) error
	Interrupt(ctx context.Context, sessionID string) error
	Subscribe(ctx context.Context, sessionID string, fromSeq int64) (<-chan event.Event, error)
	Get(ctx context.Context, sessionID string) (*Session, error)
	List(ctx context.Context) ([]Session, error)
}

type Store interface {
	Create(ctx context.Context, s *Session) error
	Update(ctx context.Context, s *Session) error
	Get(ctx context.Context, id string) (*Session, error)
	List(ctx context.Context) ([]Session, error)
}
