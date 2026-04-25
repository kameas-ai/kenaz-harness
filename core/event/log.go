package event

import "context"

type AppendOpts struct {
	Type      Type
	Actor     Actor
	Payload   any
	ParentSeq *int64
}

type Cursor struct {
	SessionID string
	After     int64
}

type Log interface {
	Append(ctx context.Context, sessionID string, opts AppendOpts) (Event, error)
	Read(ctx context.Context, sessionID string, fromSeq, toSeq int64) ([]Event, error)
	Subscribe(ctx context.Context, c Cursor) (<-chan Event, error)
	LatestSeq(ctx context.Context, sessionID string) (int64, error)
	Close() error
}

const Schema = `
CREATE TABLE IF NOT EXISTS events (
  session_id   TEXT    NOT NULL,
  seq          INTEGER NOT NULL,
  parent_seq   INTEGER,
  type         TEXT    NOT NULL,
  ts           INTEGER NOT NULL,
  actor        TEXT    NOT NULL,
  content_hash TEXT    NOT NULL,
  payload      BLOB    NOT NULL,
  PRIMARY KEY (session_id, seq)
);

CREATE INDEX IF NOT EXISTS events_by_type ON events(session_id, type);
CREATE INDEX IF NOT EXISTS events_by_hash ON events(content_hash);
`
