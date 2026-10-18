package rpc

// AC-006 / AC-007 (model-scheduled-jobs-01PMSJ01 WP06, FR-004): an
// unattended unmatched filesystem write is denied, recorded, and
// re-runnable, and deleting the scheduling job does not destroy the
// record.
//
// Real sqlite (CLAUDE.md blind spot #2) and a REAL cedar.Engine with the
// embedded default policy set — never cedar.AllowAll{}, which is
// documented (builtins_wiring.go, spec.md §10 rule 4) as exactly how the
// C-2 defect survived undetected: a fixture that permits everything
// never reaches the NotApplicable -> Prompter branch this test exists to
// exercise.
//
// This drives the REAL unattended-deny path end to end: runposture.
// Unattended(ctx) -> corefs.Gate.Evaluate's NotApplicable branch ->
// corefs.RecordingPrompter -> corefs.CedarPrompter -> a REAL
// cedar.Registry.RequestInteractive, which denies immediately because
// the ctx is marked unattended (core/policy/cedar/prompt.go, "model-
// scheduled-jobs-01PMSJ01 WP05") — not a stubbed Prompter standing in for
// that chain. RecordingPrompter only observes the PromptDeny outcome
// that real chain already produced.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/blockedrequests"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
	"github.com/kameas-ai/kenaz-harness/core/tools/fsbuiltins"

	_ "modernc.org/sqlite"
)

// buildRealDeniedWriteFixture wires the full production shape: a real
// sqlite DB, a real scheduled_chat_runs row, a real Cedar engine + a real
// interactive Registry, RecordingPrompter, and the actual
// fsbuiltins.WriteFileTool the model calls in production — everything
// AC-006's own text names, none of it stubbed.
func buildRealDeniedWriteFixture(t *testing.T) (
	tool *fsbuiltins.WriteFileTool,
	blockedStore blockedrequests.Store,
	chatStore scheduler.ScheduledChatStore,
	chatRunID string,
	sessionID string,
	ctx context.Context,
) {
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

	blockedStore = blockedrequests.NewSQLiteStore(db)
	chatStore = scheduler.NewSQLiteChatStore(db)

	chatRunID = "chatrun-ac006"
	now := time.Now().UTC()
	if err := chatStore.Create(context.Background(), scheduler.ChatRunRecord{
		ID:             chatRunID,
		Name:           "AC-006 fixture",
		PromptTemplate: "x",
		Cron:           "0 9 * * *",
		OutputSink:     "none",
		Enabled:        true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("seed scheduled_chat_runs row: %v", err)
	}

	sessionID = "sess-ac006"
	origins := NewScheduledRunOriginRegistry()
	origins.Set(sessionID, chatRunID)

	sink := newBlockedRequestSink(blockedStore, nil, nil)

	// REAL cedar engine — embedded default policy set. C-2's own lesson:
	// the default filesystem policy has exactly one permit (read), so an
	// unmatched write resolves NotApplicable, not an explicit Deny —
	// that distinction is what routes through the Prompter at all.
	engine, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	registry := cedar.NewRegistry()

	gate := corefs.NewGate(corefs.GateOptions{
		Engine: engine,
		Prompter: &corefs.RecordingPrompter{
			Inner:   &corefs.CedarPrompter{Registry: registry},
			Sink:    sink,
			Resolve: origins.Resolve,
		},
	})

	tool = fsbuiltins.NewWriteFileTool(fsbuiltins.Options{
		Gate:         gate,
		WriteEnabled: func() bool { return true },
	})

	ctx = runposture.Unattended(toolloop.WithSessionID(context.Background(), sessionID))
	return tool, blockedStore, chatStore, chatRunID, sessionID, ctx
}

func TestFSDenialRecording_AC006_UnattendedUnmatchedWriteIsDeniedRecordedAndReRunnable(t *testing.T) {
	tool, blockedStore, _, chatRunID, sessionID, ctx := buildRealDeniedWriteFixture(t)

	target := filepath.Join(t.TempDir(), "should-not-exist.txt")
	argsJSON, _ := json.Marshal(map[string]any{"path": target, "content": "should never land"})

	result, callErr := tool.Call(ctx, argsJSON)
	if callErr != nil {
		t.Fatalf("Call returned a Go error (want a JSON is_error result instead): %v", callErr)
	}

	var decoded struct {
		IsError bool   `json:"is_error"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("decode tool result: %v", err)
	}
	if !decoded.IsError {
		t.Fatalf("tool result is_error = false, want true (result: %s)", result)
	}

	// *Fails if the row is written but the write also succeeded* — assert
	// the file does not exist on disk (spec.md AC-006's own mutation
	// clause).
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target file exists on disk after a denied write: stat err = %v", statErr)
	}

	pending, err := blockedStore.ListByStatus(context.Background(), blockedrequests.StatusPending)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending blocked_permission_requests rows = %d, want exactly 1", len(pending))
	}
	row := pending[0]
	if row.Origin != corefs.OriginScheduledChatRun {
		t.Errorf("Origin = %q, want %q", row.Origin, corefs.OriginScheduledChatRun)
	}
	if row.OriginID != chatRunID {
		t.Errorf("OriginID = %q, want %q", row.OriginID, chatRunID)
	}
	if row.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", row.SessionID, sessionID)
	}
	if row.Action != "write_filesystem" {
		t.Errorf("Action = %q, want write_filesystem", row.Action)
	}
	canonical, _ := corefs.Canonicalize(target)
	if row.Resource != canonical {
		t.Errorf("Resource = %q, want the canonical path %q", row.Resource, canonical)
	}
	if row.Status != blockedrequests.StatusPending {
		t.Errorf("Status = %q, want pending", row.Status)
	}
}

// stubAllowingPrompter always grants the write — the "an attended user
// (or a misconfigured test) said yes" case, used below to prove
// TestFSDenialRecording_AC006... is not vacuously passing: RecordingPrompter
// only records on PromptDeny, so a real ALLOW must record nothing and let
// the write through.
type stubAllowingPrompter struct{}

func (stubAllowingPrompter) Prompt(_ context.Context, _ corefs.PromptSurface) (corefs.PromptResponse, error) {
	return corefs.PromptAllowOnce, nil
}

// TestFSDenialRecording_AC006_Mutation_AllowedWriteRecordsNothing is the
// mutation proof for the primary AC-006 test above: if the write is
// actually PERMITTED (RecordingPrompter's Inner returns something other
// than PromptDeny), the write must succeed on disk and the
// blocked_permission_requests table must stay empty. This is what makes
// the primary test's assertions non-vacuous — it proves this fixture
// CAN observe a write succeeding, so its "the write is denied" assertion
// in the real test is actually exercising the deny path, not a fixture
// that always errors for an unrelated reason (a bad temp dir, a
// mis-wired WriteEnabled, etc).
//
// *Fails if:* RecordingPrompter recorded on every outcome instead of only
// PromptDeny, or if gateCheck/atomicWrite were broken in a way that
// denies every write regardless of the Cedar/Prompter chain.
func TestFSDenialRecording_AC006_Mutation_AllowedWriteRecordsNothing(t *testing.T) {
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	blockedStore := blockedrequests.NewSQLiteStore(db)
	sink := newBlockedRequestSink(blockedStore, nil, nil)

	// Real embedded engine (same as the primary test) — an unmatched
	// write still resolves NotApplicable and reaches the Prompter; the
	// only thing different from the primary test is the Prompter's
	// answer.
	engine, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	gate := corefs.NewGate(corefs.GateOptions{
		Engine: engine,
		Prompter: &corefs.RecordingPrompter{
			Inner:   stubAllowingPrompter{},
			Sink:    sink,
			Resolve: nil,
		},
	})
	tool := fsbuiltins.NewWriteFileTool(fsbuiltins.Options{Gate: gate, WriteEnabled: func() bool { return true }})

	target := filepath.Join(t.TempDir(), "allowed.txt")
	argsJSON, _ := json.Marshal(map[string]any{"path": target, "content": "hello"})
	ctx := runposture.Unattended(toolloop.WithSessionID(context.Background(), "sess-mutation"))
	result, callErr := tool.Call(ctx, argsJSON)
	if callErr != nil {
		t.Fatalf("Call: %v", callErr)
	}
	var decoded struct {
		IsError bool `json:"is_error"`
	}
	_ = json.Unmarshal(result, &decoded)
	if decoded.IsError {
		t.Fatalf("expected a permitted write to succeed, got is_error result: %s", result)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("expected the file to exist after a permitted write: %v", statErr)
	}
	pending, _ := blockedStore.ListByStatus(context.Background(), "")
	if len(pending) != 0 {
		t.Fatalf("a permitted write recorded %d blocked request(s), want 0 (RecordingPrompter must only record PromptDeny)", len(pending))
	}
}

// TestFSDenialRecording_AC007_DeletingTheJobDoesNotDestroyTheRecord.
// *Fails if:* an FK with ON DELETE CASCADE is added, matching the
// scheduled_chat_run_history shape — this is a behavioural proof, not
// only the schema-level pragma_foreign_key_list check in
// core/storage/sqlite/blocked_permission_requests_upgrade_test.go.
func TestFSDenialRecording_AC007_DeletingTheJobDoesNotDestroyTheRecord(t *testing.T) {
	tool, blockedStore, chatStore, chatRunID, _, ctx := buildRealDeniedWriteFixture(t)

	target := filepath.Join(t.TempDir(), "blocked.txt")
	argsJSON, _ := json.Marshal(map[string]any{"path": target, "content": "x"})
	if _, err := tool.Call(ctx, argsJSON); err != nil {
		t.Fatalf("Call: %v", err)
	}

	pending, err := blockedStore.ListByStatus(context.Background(), blockedrequests.StatusPending)
	if err != nil || len(pending) != 1 {
		t.Fatalf("setup: expected exactly 1 pending row, got %d (err=%v)", len(pending), err)
	}
	requestID := pending[0].ID

	// Delete the scheduled_chat_runs row the request was attributed to.
	if err := chatStore.Delete(context.Background(), chatRunID); err != nil {
		t.Fatalf("Delete scheduled_chat_runs row: %v", err)
	}

	// The blocked_permission_requests row must survive.
	got, err := blockedStore.Get(context.Background(), requestID)
	if err != nil {
		t.Fatalf("blocked_permission_requests row did not survive deleting its origin job: %v", err)
	}
	if got.OriginID != chatRunID {
		t.Errorf("OriginID = %q, want %q (unchanged even though the job no longer exists)", got.OriginID, chatRunID)
	}
}
