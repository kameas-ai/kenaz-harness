package audit

// must_emit_test.go — tests for MustEmit (WP05 / FR-005).
//
// Verifies:
//   - MustEmit with a succeeding emitter emits the event.
//   - MustEmit with a failing emitter WARN-logs (observable via slog) and
//     does NOT panic or return an error.
//   - MustEmit with a nil emitter is a no-op (inherits Emit's nil guard).

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// failingEmitter always returns the supplied error from Emit.
type failingEmitter struct {
	err error
}

func (f *failingEmitter) Emit(_ context.Context, _ Event) error {
	return f.err
}

func TestMustEmit_SucceedingEmitter_EmitsEvent(t *testing.T) {
	em := &recordingEmitter{}
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	MustEmit(context.Background(), em, KindBranchCreated, BranchCreatedPayload{
		ParentSessionID: "s1",
		BranchSessionID: "s2",
		CreationPath:    "explicit",
	}, now)
	if len(em.events) != 1 {
		t.Fatalf("expected 1 event; got %d", len(em.events))
	}
	if em.events[0].Kind != KindBranchCreated {
		t.Errorf("kind = %q, want %q", em.events[0].Kind, KindBranchCreated)
	}
}

func TestMustEmit_NilEmitter_IsNoOp(t *testing.T) {
	// Should not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("MustEmit with nil emitter panicked: %v", r)
		}
	}()
	MustEmit(context.Background(), nil, KindBranchCreated, BranchCreatedPayload{}, time.Now())
}

func TestMustEmit_FailingEmitter_WarnLogsAndDoesNotPanic(t *testing.T) {
	// Capture slog output.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	logger := slog.New(handler)
	origLogger := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(origLogger) })

	em := &failingEmitter{err: errors.New("store unavailable")}
	MustEmit(context.Background(), em, KindSlashCommandRun, SlashCommandRunPayload{
		Name: "test-cmd", Scope: "global", Kind: "text",
	}, time.Now())

	logged := buf.String()
	if !strings.Contains(logged, "audit emit failed") {
		t.Errorf("expected WARN 'audit emit failed' in log; got: %s", logged)
	}
	if !strings.Contains(logged, "store unavailable") {
		t.Errorf("expected error message in log; got: %s", logged)
	}
}
