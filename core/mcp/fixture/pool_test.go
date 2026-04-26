package fixture

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestPool_RegisterAndCall(t *testing.T) {
	p := New()
	p.Register("github", "get_issue", func(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"number":42}`), nil
	})
	out, err := p.Call(context.Background(), "github", "get_issue", json.RawMessage(`{"id":42}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(out) != `{"number":42}` {
		t.Fatalf("Call result = %s", out)
	}
	calls := p.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Server != "github" || calls[0].Tool != "get_issue" {
		t.Fatalf("call meta = %+v", calls[0])
	}
	if string(calls[0].Args) != `{"id":42}` {
		t.Fatalf("call args = %s", calls[0].Args)
	}
}

func TestPool_UnregisteredCallReturnsError(t *testing.T) {
	p := New()
	_, err := p.Call(context.Background(), "missing", "tool", nil)
	if err == nil {
		t.Fatal("expected error for unregistered tool")
	}
	// Even unregistered calls are recorded so tests can assert the
	// toolloop attempted dispatch before failing.
	if len(p.Calls()) != 1 {
		t.Fatalf("expected one recorded call, got %d", len(p.Calls()))
	}
}

func TestPool_HandlerErrorPropagates(t *testing.T) {
	p := New()
	want := errors.New("upstream 500")
	p.Register("svc", "boom", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return nil, want
	})
	_, err := p.Call(context.Background(), "svc", "boom", nil)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestPool_ToolsListsRegistered(t *testing.T) {
	p := New()
	p.Register("a", "x", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return nil, nil
	})
	p.Register("b", "y", func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return nil, nil
	})
	tools, err := p.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %d", len(tools))
	}
}
