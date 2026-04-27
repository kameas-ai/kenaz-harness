package slashcmd

import (
	"context"
	"errors"
	"testing"
)

// fakeAppender records every AppendSystemMessage call.
type fakeAppender struct {
	calls []fakeAppendCall
	err   error
}

type fakeAppendCall struct {
	sessionID string
	content   string
}

func (f *fakeAppender) AppendSystemMessage(_ context.Context, sessionID, content string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.calls = append(f.calls, fakeAppendCall{sessionID, content})
	return "msg-" + sessionID, nil
}

func TestClearCommand_AppendsSystemDivider(t *testing.T) {
	t.Parallel()
	app := &fakeAppender{}
	r, _ := NewRegistry(Deps{Sessions: app})
	res, err := r.Execute(context.Background(), "session-1", "/clear")
	if err != nil {
		t.Fatalf("Execute(/clear): %v", err)
	}
	if res.Kind != ResultKindSystem {
		t.Errorf("Kind = %q, want %q", res.Kind, ResultKindSystem)
	}
	if res.Text != clearDividerText {
		t.Errorf("Text = %q, want %q", res.Text, clearDividerText)
	}
	if len(app.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(app.calls))
	}
	got := app.calls[0]
	if got.sessionID != "session-1" {
		t.Errorf("sessionID = %q, want session-1", got.sessionID)
	}
	if got.content != clearDividerText {
		t.Errorf("content = %q, want %q", got.content, clearDividerText)
	}
}

func TestClearCommand_NoSessionReturnsError(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{Sessions: &fakeAppender{}})
	res, err := r.Execute(context.Background(), "", "/clear")
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want %q", res.Kind, ResultKindError)
	}
}

func TestClearCommand_NoAppenderReturnsError(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	res, err := r.Execute(context.Background(), "session-1", "/clear")
	if err != nil {
		t.Fatalf("Execute returned %v", err)
	}
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want %q", res.Kind, ResultKindError)
	}
}

func TestClearCommand_AppenderErrorPropagates(t *testing.T) {
	t.Parallel()
	app := &fakeAppender{err: errors.New("boom")}
	r, _ := NewRegistry(Deps{Sessions: app})
	res, err := r.Execute(context.Background(), "session-1", "/clear")
	if err == nil || err.Error() != "boom" {
		t.Errorf("err = %v, want boom", err)
	}
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want %q", res.Kind, ResultKindError)
	}
}
