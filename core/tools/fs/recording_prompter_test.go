package fs

// Unit-level tests for RecordingPrompter in isolation — no database, no
// rpc layer, no real Cedar engine. This is the layer where
// RecordingPrompter is "the only guard" (CLAUDE.md's quality bar: "when
// two layers both enforce a rule, one end-to-end test proves neither").
// core/rpc/fs_denial_recording_test.go covers the same behaviour through
// the full production stack (real sqlite, real cedar.Engine, the actual
// fsbuiltins.WriteFileTool) — this file isolates RecordingPrompter's own
// decision to record-or-not from every other moving part.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// fakeSink is a race-safe BlockedRequestSink fake (CLAUDE.md: writes may
// come from a goroutine in production; snapshot for test-side reads).
type fakeSink struct {
	mu       sync.Mutex
	recorded []BlockedRequest
	err      error
}

func (f *fakeSink) RecordBlocked(_ context.Context, req BlockedRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = append(f.recorded, req)
	return f.err
}

func (f *fakeSink) snapshot() []BlockedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]BlockedRequest, len(f.recorded))
	copy(out, f.recorded)
	return out
}

type stubRespondingPrompter struct {
	resp PromptResponse
	err  error
}

func (s stubRespondingPrompter) Prompt(_ context.Context, _ PromptSurface) (PromptResponse, error) {
	return s.resp, s.err
}

func TestRecordingPrompter_RecordsOnDeny(t *testing.T) {
	sink := &fakeSink{}
	p := &RecordingPrompter{
		Inner: stubRespondingPrompter{resp: PromptDeny},
		Sink:  sink,
	}
	ctx := toolloop.WithSessionID(context.Background(), "sess-1")
	resp, err := p.Prompt(ctx, PromptSurface{Op: OpWrite, CanonicalPath: "/tmp/x"})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if resp != PromptDeny {
		t.Errorf("resp = %v, want PromptDeny (RecordingPrompter must not change Inner's decision)", resp)
	}
	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("recorded = %d, want exactly 1", len(got))
	}
	if got[0].SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", got[0].SessionID)
	}
	if got[0].Action != "write_filesystem" {
		t.Errorf("Action = %q, want write_filesystem", got[0].Action)
	}
	if got[0].Origin != OriginInteractive {
		t.Errorf("Origin = %q, want %q (no Resolve configured)", got[0].Origin, OriginInteractive)
	}
}

func TestRecordingPrompter_DoesNotRecordOnAllow(t *testing.T) {
	for _, resp := range []PromptResponse{PromptAllowOnce, PromptAllowExact, PromptAllowDirectory} {
		sink := &fakeSink{}
		p := &RecordingPrompter{Inner: stubRespondingPrompter{resp: resp}, Sink: sink}
		got, err := p.Prompt(context.Background(), PromptSurface{Op: OpRead, CanonicalPath: "/tmp/y"})
		if err != nil {
			t.Fatalf("Prompt: %v", err)
		}
		if got != resp {
			t.Errorf("resp = %v, want %v (unchanged)", got, resp)
		}
		if n := len(sink.snapshot()); n != 0 {
			t.Errorf("resp=%v: recorded = %d, want 0", resp, n)
		}
	}
}

func TestRecordingPrompter_UsesResolveForOrigin(t *testing.T) {
	sink := &fakeSink{}
	p := &RecordingPrompter{
		Inner: stubRespondingPrompter{resp: PromptDeny},
		Sink:  sink,
		Resolve: func(sessionID string) (string, string) {
			if sessionID == "sess-scheduled" {
				return OriginScheduledChatRun, "chatrun-42"
			}
			return "", ""
		},
	}
	ctx := toolloop.WithSessionID(context.Background(), "sess-scheduled")
	if _, err := p.Prompt(ctx, PromptSurface{Op: OpWrite, CanonicalPath: "/tmp/z"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("recorded = %d, want 1", len(got))
	}
	if got[0].Origin != OriginScheduledChatRun || got[0].OriginID != "chatrun-42" {
		t.Errorf("Origin/OriginID = %q/%q, want %q/chatrun-42", got[0].Origin, got[0].OriginID, OriginScheduledChatRun)
	}
}

func TestRecordingPrompter_ResolveEmptySessionFallsBackToInteractive(t *testing.T) {
	sink := &fakeSink{}
	p := &RecordingPrompter{
		Inner: stubRespondingPrompter{resp: PromptDeny},
		Sink:  sink,
		Resolve: func(string) (string, string) {
			return "", "" // unrecognised session
		},
	}
	if _, err := p.Prompt(context.Background(), PromptSurface{Op: OpWrite, CanonicalPath: "/tmp/w"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	got := sink.snapshot()
	if len(got) != 1 || got[0].Origin != OriginInteractive {
		t.Fatalf("got %+v, want exactly 1 record with Origin=%q", got, OriginInteractive)
	}
}

func TestRecordingPrompter_NilSinkDisablesRecordingButStillDenies(t *testing.T) {
	p := &RecordingPrompter{Inner: stubRespondingPrompter{resp: PromptDeny}, Sink: nil}
	resp, err := p.Prompt(context.Background(), PromptSurface{Op: OpWrite, CanonicalPath: "/tmp/v"})
	if err != nil || resp != PromptDeny {
		t.Fatalf("resp=%v err=%v, want PromptDeny/nil", resp, err)
	}
}

func TestRecordingPrompter_NilInnerAlwaysDeniesAndRecordsNothing(t *testing.T) {
	sink := &fakeSink{}
	p := &RecordingPrompter{Inner: nil, Sink: sink}
	resp, err := p.Prompt(context.Background(), PromptSurface{Op: OpWrite, CanonicalPath: "/tmp/u"})
	if err != nil || resp != PromptDeny {
		t.Fatalf("resp=%v err=%v, want PromptDeny/nil", resp, err)
	}
	if n := len(sink.snapshot()); n != 0 {
		t.Errorf("recorded = %d, want 0 (nothing meaningful to record with no Inner)", n)
	}
}

func TestRecordingPrompter_RecordingFailureDoesNotChangeDenyOutcome(t *testing.T) {
	sink := &fakeSink{err: errors.New("db write failed")}
	p := &RecordingPrompter{Inner: stubRespondingPrompter{resp: PromptDeny}, Sink: sink}
	resp, err := p.Prompt(context.Background(), PromptSurface{Op: OpWrite, CanonicalPath: "/tmp/t"})
	if err != nil {
		t.Fatalf("a Sink failure must not surface as a Prompt error: %v", err)
	}
	if resp != PromptDeny {
		t.Errorf("resp = %v, want PromptDeny even when recording failed (fail-safe: deny is never contingent on the DB write)", resp)
	}
}

func TestRecordingPrompter_ReadActionRecordedAsReadFilesystem(t *testing.T) {
	sink := &fakeSink{}
	p := &RecordingPrompter{Inner: stubRespondingPrompter{resp: PromptDeny}, Sink: sink}
	if _, err := p.Prompt(context.Background(), PromptSurface{Op: OpRead, CanonicalPath: "/tmp/r"}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	got := sink.snapshot()
	if len(got) != 1 || got[0].Action != "read_filesystem" {
		t.Fatalf("got %+v, want exactly 1 record with Action=read_filesystem", got)
	}
}
