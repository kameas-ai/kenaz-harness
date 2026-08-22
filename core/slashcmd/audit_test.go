package slashcmd_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// fakeAuditEmitter records every emitted event for assertion.
type fakeAuditEmitter struct {
	events []audit.Event
}

func (f *fakeAuditEmitter) Emit(_ context.Context, e audit.Event) error {
	f.events = append(f.events, e)
	return nil
}

func openAuditTestDB(t *testing.T) (storage.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db, dir
}

func TestEmitRun_RecordsArgNamesNotValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, dir := openAuditTestDB(t)
	store := coreslashcmd.NewStore(db, dir)
	em := &fakeAuditEmitter{}
	dispatch := coreslashcmd.NewDispatch(store, nil).WithAuditEmitter(em)

	// Save a text command.
	if err := store.SaveUser(ctx, coreslashcmd.UserCommand{
		Name:        "greet",
		Scope:       "global",
		Kind:        "text",
		Description: "Greeting",
		Body:        "Hello {{name}}!",
	}); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	// Run with a secret-looking arg value.
	_, _ = dispatch.Run(ctx, "greet", map[string]string{
		"name": "SuperSecretToken!!!",
	}, coreslashcmd.SessionContext{
		SessionID: "s1",
		ProjectID: "",
	})

	if len(em.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(em.events))
	}

	e := em.events[0]
	if e.Kind != audit.KindSlashCommandRun {
		t.Errorf("Kind = %q, want %q", e.Kind, audit.KindSlashCommandRun)
	}

	var payload audit.SlashCommandRunPayload
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	// Arg names should be present.
	if len(payload.ArgNames) != 1 || payload.ArgNames[0] != "name" {
		t.Errorf("ArgNames = %v, want [name]", payload.ArgNames)
	}

	// The secret VALUE must NOT appear anywhere in the raw payload JSON.
	raw := string(e.Payload)
	if contains(raw, "SuperSecretToken!!!") {
		t.Errorf("audit payload contains arg value — privacy violation: %s", raw)
	}

	if payload.Name != "greet" {
		t.Errorf("Name = %q, want %q", payload.Name, "greet")
	}
	if !payload.Success {
		t.Errorf("Success = false, want true")
	}
	if payload.SessionID != "s1" {
		t.Errorf("SessionID = %q, want %q", payload.SessionID, "s1")
	}
}

func TestEmitRun_RecordsFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, dir := openAuditTestDB(t)
	store := coreslashcmd.NewStore(db, dir)
	em := &fakeAuditEmitter{}
	dispatch := coreslashcmd.NewDispatch(store, nil).WithAuditEmitter(em)

	// Run a command that doesn't exist.
	_, _ = dispatch.Run(ctx, "nonexistent", nil, coreslashcmd.SessionContext{
		SessionID: "s2",
	})

	// Nonexistent command: cmd.Name is empty after load failure so
	// EmitRun skips (cmd.Name == ""). Verify the emitter was NOT called.
	if len(em.events) != 0 {
		t.Errorf("expected 0 audit events for not-found, got %d", len(em.events))
	}
}

func TestEmitRun_NilEmitterIsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, dir := openAuditTestDB(t)
	store := coreslashcmd.NewStore(db, dir)
	// No WithAuditEmitter call — emitter is nil.
	dispatch := coreslashcmd.NewDispatch(store, nil)

	if err := store.SaveUser(ctx, coreslashcmd.UserCommand{
		Name:        "noop",
		Scope:       "global",
		Kind:        "text",
		Description: "No-op",
		Body:        "hello",
	}); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	// Should not panic.
	_, err := dispatch.Run(ctx, "noop", nil, coreslashcmd.SessionContext{SessionID: "s3"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEmitRun_ToolKindRecordsTool(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, dir := openAuditTestDB(t)
	store := coreslashcmd.NewStore(db, dir)
	em := &fakeAuditEmitter{}
	dispatch := coreslashcmd.NewDispatch(store, nil).WithAuditEmitter(em)

	if err := store.SaveUser(ctx, coreslashcmd.UserCommand{
		Name:             "deploy",
		Scope:            "global",
		Kind:             "tool",
		Description:      "Deploy",
		Tool:             "bash",
		ToolArgsTemplate: "./deploy.sh",
	}); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	_, _ = dispatch.Run(ctx, "deploy", nil, coreslashcmd.SessionContext{SessionID: "s4"})

	if len(em.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(em.events))
	}

	var payload audit.SlashCommandRunPayload
	if err := json.Unmarshal(em.events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.DispatchedTool != "bash" {
		t.Errorf("DispatchedTool = %q, want %q", payload.DispatchedTool, "bash")
	}
	if payload.Kind != "tool" {
		t.Errorf("Kind = %q, want %q", payload.Kind, "tool")
	}
}

// TestEmitRun_RunModelInvoked_RecordsRejection is the audit half of
// AC-14 (trust-surfaces-that-fire-01PMZ202 WP20): a model attempt against
// a model_invokable=false command must be visible in the audit log, not
// merely refused. Without this, the only trace of a disallowed attempt
// is a transient error string the caller may or may not surface.
func TestEmitRun_RunModelInvoked_RecordsRejection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	db, dir := openAuditTestDB(t)
	store := coreslashcmd.NewStore(db, dir)
	em := &fakeAuditEmitter{}
	dispatch := coreslashcmd.NewDispatch(store, nil).WithAuditEmitter(em)

	if err := store.SaveUser(ctx, coreslashcmd.UserCommand{
		Name:        "restricted",
		Scope:       "global",
		Kind:        "text",
		Description: "Not model-invokable",
		Body:        "restricted body",
		// ModelInvokable left false.
	}); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	_, err := dispatch.RunModelInvoked(ctx, "restricted", nil, coreslashcmd.SessionContext{
		SessionID: "s5",
	})
	if err == nil {
		t.Fatal("expected RunModelInvoked to refuse a model_invokable=false command")
	}

	if len(em.events) != 1 {
		t.Fatalf("expected 1 audit event for the rejected attempt, got %d", len(em.events))
	}

	var payload audit.SlashCommandRunPayload
	if err := json.Unmarshal(em.events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Name != "restricted" {
		t.Errorf("Name = %q, want %q", payload.Name, "restricted")
	}
	if payload.Success {
		t.Error("Success = true, want false for a refused model attempt")
	}
	if payload.ModelInvokable {
		t.Error("ModelInvokable = true, want false (that is why it was refused)")
	}
	raw := string(em.events[0].Payload)
	if contains(raw, "restricted body") {
		t.Errorf("audit payload contains the command body — privacy violation: %s", raw)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsAt(s, sub))
}

func containsAt(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
