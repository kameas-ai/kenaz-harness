package rpc

import (
	"context"
	"errors"

	"github.com/sigil-tech/kaneaz-harness/core"
	"github.com/sigil-tech/kaneaz-harness/core/event"
	"github.com/sigil-tech/kaneaz-harness/core/session"
)

type SessionAPI struct{ c *core.Core }

func NewSessionAPI(c *core.Core) *SessionAPI { return &SessionAPI{c: c} }

func (s *SessionAPI) Start(ctx context.Context, spec session.Spec) (*session.Session, error) {
	if s.c.Sessions == nil {
		return nil, errors.New("session: executor not configured")
	}
	return s.c.Sessions.Start(ctx, spec)
}

func (s *SessionAPI) Send(ctx context.Context, sessionID, input string) error {
	if s.c.Sessions == nil {
		return errors.New("session: executor not configured")
	}
	return s.c.Sessions.Send(ctx, sessionID, input)
}

func (s *SessionAPI) Interrupt(ctx context.Context, sessionID string) error {
	if s.c.Sessions == nil {
		return errors.New("session: executor not configured")
	}
	return s.c.Sessions.Interrupt(ctx, sessionID)
}

func (s *SessionAPI) Get(ctx context.Context, sessionID string) (*session.Session, error) {
	if s.c.Sessions == nil {
		return nil, errors.New("session: executor not configured")
	}
	return s.c.Sessions.Get(ctx, sessionID)
}

func (s *SessionAPI) List(ctx context.Context) ([]session.Session, error) {
	if s.c.Sessions == nil {
		return nil, errors.New("session: executor not configured")
	}
	return s.c.Sessions.List(ctx)
}

func (s *SessionAPI) Events(ctx context.Context, sessionID string, fromSeq, toSeq int64) ([]event.Event, error) {
	if s.c.Events == nil {
		return nil, errors.New("session: event log not configured")
	}
	return s.c.Events.Read(ctx, sessionID, fromSeq, toSeq)
}
