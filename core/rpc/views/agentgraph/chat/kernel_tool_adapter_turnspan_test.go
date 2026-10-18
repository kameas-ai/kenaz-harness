package chat

import (
	"context"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// ctxCapturingPool records the dispatch context's session + turn span.
// Race-safe: reads go through snapshot().
type ctxCapturingPool struct {
	mu      sync.Mutex
	session string
	span    string
}

func (p *ctxCapturingPool) Tools(context.Context) ([]ToolEntry, error) {
	return []ToolEntry{{Server: "kenaz", Name: "fork_conversation"}}, nil
}

func (p *ctxCapturingPool) Call(ctx context.Context, _, _ string, _ []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.session = toolloop.SessionIDFromContext(ctx)
	p.span = toolloop.TurnSpanIDFromContext(ctx)
	return []byte(`"ok"`), nil
}

func (p *ctxCapturingPool) snapshot() (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session, p.span
}

// TestKernelToolAdapter_AttachesTurnSpanToDispatchContext pins the
// review-M1 plumbing: a tool dispatched inside a chat turn can read the
// turn's span (the user message that opened it) from its context, so
// kenaz__fork_conversation can default its branch point to BEFORE the
// live turn.
func TestKernelToolAdapter_AttachesTurnSpanToDispatchContext(t *testing.T) {
	t.Parallel()
	pool := &ctxCapturingPool{}
	a := newKernelToolAdapter(pool, nil, "sess-1").
		withMoves(newTurnJournal(nil, nil, "sess-1", "span-user-msg", nil))
	if _, err := a.Call(context.Background(), makeCall("kenaz", "fork_conversation")); err != nil {
		t.Fatalf("Call: %v", err)
	}
	sess, span := pool.snapshot()
	if sess != "sess-1" || span != "span-user-msg" {
		t.Fatalf("dispatch ctx session=%q span=%q, want sess-1 / span-user-msg", sess, span)
	}

	// No journal (non-chat callers): no span, session still set.
	pool2 := &ctxCapturingPool{}
	if _, err := newKernelToolAdapter(pool2, nil, "sess-2").Call(context.Background(), makeCall("kenaz", "fork_conversation")); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if sess, span := pool2.snapshot(); sess != "sess-2" || span != "" {
		t.Fatalf("no-journal dispatch ctx session=%q span=%q, want sess-2 / empty", sess, span)
	}
}
