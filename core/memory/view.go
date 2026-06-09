package memory

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/event"
)

type Snapshot struct {
	SessionID string            `json:"session_id"`
	Watermark int64             `json:"watermark"`
	Entries   map[string]string `json:"entries"`
}

type View interface {
	At(ctx context.Context, sessionID string, watermark int64) (*Snapshot, error)
	Latest(ctx context.Context, sessionID string) (*Snapshot, error)
}

type Extractor interface {
	Apply(prev Snapshot, e event.Event) (Snapshot, bool)
}
