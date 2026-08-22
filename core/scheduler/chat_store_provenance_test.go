package scheduler_test

// Real-sqlite persistence tests for created_by / tool_allowlist
// (model-scheduled-jobs-01PMSJ01 WP09, migration sessions/0340). Per
// CLAUDE.md blind spot #2 ("test fixtures that bypass the layer under
// test"), anything asserting persistence must drive real sqlite, not
// an in-memory fake — core/rpc/views/scheduledchat/impl_test.go's
// fakeStore exercises API-layer control flow only (see that file's own
// doctrine comment) and its Update was found, while writing this WP, to
// silently clobber created_by unless explicitly patched to mirror the
// real SQL statement's column set. These tests assert the real
// statement's behaviour directly.

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/scheduler"
)

func TestSQLiteChatStore_CreateAndGet_RoundTripsProvenance(t *testing.T) {
	store := openTestChatStore(t)
	now := time.Now().UTC()

	if err := store.Create(context.Background(), scheduler.ChatRunRecord{
		ID: "prov-1", Cron: "0 9 * * *", CreatedAt: now, UpdatedAt: now,
		CreatedBy:     scheduler.ScheduledRunCreatedByModel,
		ToolAllowlist: []string{"kenaz__web_fetch", "kenaz__read_file"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get(context.Background(), "prov-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CreatedBy != scheduler.ScheduledRunCreatedByModel {
		t.Errorf("CreatedBy=%q, want %q", got.CreatedBy, scheduler.ScheduledRunCreatedByModel)
	}
	if len(got.ToolAllowlist) != 2 || got.ToolAllowlist[0] != "kenaz__web_fetch" || got.ToolAllowlist[1] != "kenaz__read_file" {
		t.Errorf("ToolAllowlist=%v, want [kenaz__web_fetch kenaz__read_file]", got.ToolAllowlist)
	}
}

// TestSQLiteChatStore_Create_DefaultsCreatedByToUser exercises the
// column's DEFAULT 'user' via the empty-string fallback Create()
// applies (mirrors migration 0340's column default for pre-existing
// rows, and covers a caller that leaves CreatedBy unset).
func TestSQLiteChatStore_Create_DefaultsCreatedByToUser(t *testing.T) {
	store := openTestChatStore(t)
	now := time.Now().UTC()
	if err := store.Create(context.Background(), scheduler.ChatRunRecord{
		ID: "prov-default", Cron: "0 9 * * *", CreatedAt: now, UpdatedAt: now,
		// CreatedBy intentionally left zero-value.
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(context.Background(), "prov-default")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CreatedBy != scheduler.ScheduledRunCreatedByUser {
		t.Errorf("CreatedBy=%q, want %q (default)", got.CreatedBy, scheduler.ScheduledRunCreatedByUser)
	}
	if len(got.ToolAllowlist) != 0 {
		t.Errorf("ToolAllowlist=%v, want empty", got.ToolAllowlist)
	}
}

// TestSQLiteChatStore_Update_PreservesCreatedBy drives the REAL SQL
// UPDATE statement (core/scheduler/chat_store.go), which deliberately
// omits the created_by column, and asserts the persisted value survives
// an Update — the actual enforcement of "stamped server-side, never
// settable by the caller" for the mutation path, not just the create
// path.
func TestSQLiteChatStore_Update_PreservesCreatedBy(t *testing.T) {
	store := openTestChatStore(t)
	now := time.Now().UTC()
	if err := store.Create(context.Background(), scheduler.ChatRunRecord{
		ID: "prov-immutable", Cron: "0 9 * * *", CreatedAt: now, UpdatedAt: now,
		CreatedBy:     scheduler.ScheduledRunCreatedByModel,
		ToolAllowlist: []string{"kenaz__web_fetch"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Update with CreatedBy left zero-value / different, and a changed
	// ToolAllowlist — the created_by column must not move even though
	// the struct passed in doesn't carry the original value.
	if err := store.Update(context.Background(), scheduler.ChatRunRecord{
		ID: "prov-immutable", Cron: "0 10 * * *", Enabled: true, UpdatedAt: time.Now().UTC(),
		CreatedBy:     scheduler.ScheduledRunCreatedByUser, // attempted smuggle
		ToolAllowlist: []string{"kenaz__web_fetch", "kenaz__edit_file"},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.Get(context.Background(), "prov-immutable")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CreatedBy != scheduler.ScheduledRunCreatedByModel {
		t.Fatalf("CreatedBy=%q after Update, want %q (immutable) — "+
			"the UPDATE statement must not include the created_by column",
			got.CreatedBy, scheduler.ScheduledRunCreatedByModel)
	}
	// ToolAllowlist IS mutable (a schedule's containment can be
	// tightened/loosened by an Update) — confirm the new value landed,
	// so this test is not accidentally asserting nothing moved at all.
	if len(got.ToolAllowlist) != 2 || got.ToolAllowlist[1] != "kenaz__edit_file" {
		t.Errorf("ToolAllowlist=%v, want the updated [kenaz__web_fetch kenaz__edit_file]", got.ToolAllowlist)
	}
}
