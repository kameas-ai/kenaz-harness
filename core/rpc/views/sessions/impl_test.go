package sessions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// timeNow is a stable clock for the WP07 ListMessagesActive tests so
// the relative ArchivedAt / CompactedAt windows stay deterministic.
func timeNow() time.Time {
	return time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
}

// TestManagerAPI_RoundTrip exercises the rpc-side impl against a real
// Manager backed by an in-memory store. The intent is to pin the
// adapter's view-shape projection (Record -> Session) and to make
// sure the standard CRUD verbs surface through cleanly.
func TestManagerAPI_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mgr := session.NewManager(session.NewMemoryStore())
	api := NewManagerAPI(mgr)

	a, err := api.Create(ctx, "alpha")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.ID == "" || a.Name != "alpha" {
		t.Errorf("Create returned %+v, want non-empty ID + name=alpha", a)
	}

	b, _ := api.Create(ctx, "beta")
	c, _ := api.Create(ctx, "gamma")

	got, err := api.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("List len = %d, want 3", len(got))
	}

	if err := api.Rename(ctx, a.ID, "alpha-prime"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	got1, _ := api.Get(ctx, a.ID)
	if got1.Name != "alpha-prime" {
		t.Errorf("Get.Name = %q, want alpha-prime", got1.Name)
	}

	if err := api.Reorder(ctx, []string{c.ID, a.ID, b.ID}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	got2, _ := api.List(ctx)
	wantOrder := []string{c.ID, a.ID, b.ID}
	for i, want := range wantOrder {
		if got2[i].ID != want {
			t.Errorf("List[%d].ID = %s, want %s", i, got2[i].ID, want)
		}
	}

	if err := api.Delete(ctx, b.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := api.Get(ctx, b.ID); err == nil {
		t.Errorf("Get after Delete returned no error")
	}

	// StartStream + StopStream are no-ops today; just assert they don't error.
	if _, err := api.StartStream(ctx, a.ID); err != nil {
		t.Errorf("StartStream: %v", err)
	}
	if err := api.StopStream(ctx, "sub-1"); err != nil {
		t.Errorf("StopStream: %v", err)
	}
}

// TestManagerAPI_MoveToProject pins the projectId round-trip in the
// view shape. An empty projectID detaches (loose).
func TestManagerAPI_MoveToProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := session.NewManager(session.NewMemoryStore())
	api := NewManagerAPI(mgr)

	s, err := api.Create(ctx, "alpha")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if s.ProjectID != "" {
		t.Errorf("fresh session ProjectID = %q, want empty", s.ProjectID)
	}
	if err := api.MoveToProject(ctx, s.ID, "p-1"); err != nil {
		t.Fatalf("MoveToProject: %v", err)
	}
	got, _ := api.Get(ctx, s.ID)
	if got.ProjectID != "p-1" {
		t.Errorf("ProjectID after attach = %q, want p-1", got.ProjectID)
	}
	if err := api.MoveToProject(ctx, s.ID, ""); err != nil {
		t.Fatalf("MoveToProject(detach): %v", err)
	}
	got, _ = api.Get(ctx, s.ID)
	if got.ProjectID != "" {
		t.Errorf("ProjectID after detach = %q, want empty", got.ProjectID)
	}
}

// TestSetSystemPrompt_UpsertsAttachment pins the WP03 shim: when an
// attachments manager is wired, SetSystemPrompt mirrors the prompt into
// a position-0 inline session-scope attachment with a sha256 source.
// The legacy session.system_prompt column is also populated for the
// one-release compat buffer.
func TestSetSystemPrompt_UpsertsAttachment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	smgr := session.NewManager(session.NewMemoryStore())
	att := attachments.NewManager(attachments.NewMemoryStore())
	api := NewManagerAPIWithAttachments(smgr, att)

	s, err := api.Create(ctx, "alpha")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := api.SetSystemPrompt(ctx, s.ID, "be brief", session.ContextKindSystem); err != nil {
		t.Fatalf("SetSystemPrompt: %v", err)
	}

	got, err := att.List(ctx, attachments.ScopeFilter{
		ScopeKind: attachments.ScopeKindSession, ScopeID: s.ID,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("attachments len = %d, want 1", len(got))
	}
	hash := sha256.Sum256([]byte("be brief"))
	wantSource := "inline:" + hex.EncodeToString(hash[:])
	if got[0].ContentSource != wantSource {
		t.Errorf("ContentSource = %q, want %q", got[0].ContentSource, wantSource)
	}
	if got[0].Content != "be brief" {
		t.Errorf("Content = %q", got[0].Content)
	}
	if got[0].Position != 0 {
		t.Errorf("Position = %d", got[0].Position)
	}
	if got[0].Kind != attachments.KindSystem {
		t.Errorf("Kind = %q, want system", got[0].Kind)
	}

	// Resetting to a new prompt removes the prior row and inserts a fresh one.
	if err := api.SetSystemPrompt(ctx, s.ID, "be terse", session.ContextKindSystem); err != nil {
		t.Fatalf("SetSystemPrompt second: %v", err)
	}
	got, _ = att.List(ctx, attachments.ScopeFilter{
		ScopeKind: attachments.ScopeKindSession, ScopeID: s.ID,
	})
	if len(got) != 1 {
		t.Fatalf("after reset len = %d, want 1", len(got))
	}
	if got[0].Content != "be terse" {
		t.Errorf("after reset content = %q", got[0].Content)
	}

	// Clearing removes the attachment entirely.
	if err := api.SetSystemPrompt(ctx, s.ID, "", session.ContextKindSystem); err != nil {
		t.Fatalf("SetSystemPrompt clear: %v", err)
	}
	got, _ = att.List(ctx, attachments.ScopeFilter{
		ScopeKind: attachments.ScopeKindSession, ScopeID: s.ID,
	})
	if len(got) != 0 {
		t.Errorf("after clear len = %d, want 0", len(got))
	}

	// Legacy column still populated for the one-release compat buffer.
	rec, _ := smgr.Get(ctx, s.ID)
	if rec.SystemPrompt != "" {
		t.Errorf("legacy SystemPrompt = %q, want empty after clear", rec.SystemPrompt)
	}
}

// TestSetSystemPrompt_UserSeedKindMaps verifies user_seed → user.
func TestSetSystemPrompt_UserSeedKindMaps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	smgr := session.NewManager(session.NewMemoryStore())
	att := attachments.NewManager(attachments.NewMemoryStore())
	api := NewManagerAPIWithAttachments(smgr, att)
	s, _ := api.Create(ctx, "x")
	if err := api.SetSystemPrompt(ctx, s.ID, "seed", session.ContextKindUserSeed); err != nil {
		t.Fatalf("SetSystemPrompt: %v", err)
	}
	got, _ := att.List(ctx, attachments.ScopeFilter{
		ScopeKind: attachments.ScopeKindSession, ScopeID: s.ID,
	})
	if len(got) != 1 || got[0].Kind != attachments.KindUser {
		t.Errorf("got %+v, want kind=user", got)
	}
}

// TestNewManagerAPI_NilManager pins the construction precondition.
func TestNewManagerAPI_NilManager(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("NewManagerAPI(nil) did not panic")
		}
	}()
	_ = NewManagerAPI(nil)
}

// TestSendMessageWithBlocks_RoundTrip pins the multimodal-io WP03
// blocks-aware send path: the persisted row carries the polymorphic
// content shape and ListMessages surfaces the same blocks back.
func TestSendMessageWithBlocks_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	smgr := session.NewManager(session.NewMemoryStore())
	api := NewManagerAPI(smgr)

	s, err := api.Create(ctx, "alpha")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	blocks := []ContentBlock{
		{Type: "text", Text: "describe this"},
		{Type: "image", Source: &llm.MediaSource{Kind: "base64", MediaType: "image/png", Data: "AAAA"}},
	}
	stored, err := api.SendMessageWithBlocks(ctx, s.ID, blocks)
	if err != nil {
		t.Fatalf("SendMessageWithBlocks: %v", err)
	}
	if stored.ID == "" {
		t.Errorf("stored.ID empty: %+v", stored)
	}
	if stored.Role != string(session.RoleUser) {
		t.Errorf("Role = %q, want %q", stored.Role, session.RoleUser)
	}

	// ListMessages — the rpc wire shape's Content is the flattened
	// text projection; the durable session.Message keeps the full
	// block list. Round-trip via the manager so we can verify both.
	rawMsgs, err := smgr.ListMessages(ctx, s.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(rawMsgs) != 1 {
		t.Fatalf("len = %d, want 1", len(rawMsgs))
	}
	got := rawMsgs[0]
	if len(got.ContentBlocks) != 2 {
		t.Fatalf("ContentBlocks len = %d, want 2", len(got.ContentBlocks))
	}
	if got.ContentBlocks[0].Type != "text" || got.ContentBlocks[0].Text != "describe this" {
		t.Errorf("first block = %+v", got.ContentBlocks[0])
	}
	if got.ContentBlocks[1].Type != "image" {
		t.Errorf("second block = %+v", got.ContentBlocks[1])
	}
	if got.ContentBlocks[1].Source == nil || got.ContentBlocks[1].Source.MediaType != "image/png" {
		t.Errorf("image source lost: %+v", got.ContentBlocks[1].Source)
	}
}

// TestListMessagesActive_HidesArchivedRows pins the WP07
// scrollback contract: ListMessagesActive omits rows whose
// ArchivedAt is non-nil, while ListMessagesAll surfaces every row.
func TestListMessagesActive_HidesArchivedRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := session.NewMemoryStore()
	smgr := session.NewManager(store)
	api := NewManagerAPI(smgr)

	s, err := api.Create(ctx, "compacted")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Append three messages: one archived, one summary, one live.
	// AppendMessage preserves the bookkeeping fields the engine
	// would set in production.
	now := timeNow()
	archivedAt := now.Add(-time.Hour)
	summaryID := "msg-summary"
	if _, err := store.AppendMessage(ctx, session.Message{
		ID: "msg-archived", SessionID: s.ID, Role: session.RoleUser,
		Content: "old user turn", CreatedAt: now.Add(-2 * time.Hour),
		ArchivedAt: &archivedAt, CompactedIntoID: &summaryID,
	}); err != nil {
		t.Fatalf("append archived: %v", err)
	}
	compactedAt := archivedAt
	if _, err := store.AppendMessage(ctx, session.Message{
		ID: summaryID, SessionID: s.ID, Role: session.RoleSystem,
		Content: "[Earlier conversation summary: ...]", CreatedAt: now.Add(-time.Hour),
		CompactedAt: &compactedAt,
	}); err != nil {
		t.Fatalf("append summary: %v", err)
	}
	if _, err := store.AppendMessage(ctx, session.Message{
		ID: "msg-live", SessionID: s.ID, Role: session.RoleUser,
		Content: "live turn", CreatedAt: now,
	}); err != nil {
		t.Fatalf("append live: %v", err)
	}

	// Active view: archived row is filtered out, summary + live
	// remain. The summary carries CompactedAt; the live row is
	// untouched.
	active, err := api.ListMessagesActive(ctx, s.ID)
	if err != nil {
		t.Fatalf("ListMessagesActive: %v", err)
	}
	if len(active.Messages) != 2 {
		t.Fatalf("active len = %d, want 2", len(active.Messages))
	}
	if active.Messages[0].ID != summaryID {
		t.Errorf("active[0].ID = %q, want %q", active.Messages[0].ID, summaryID)
	}
	if active.Messages[0].CompactedAt == "" {
		t.Errorf("active[0].CompactedAt empty; expected populated")
	}
	if active.Messages[1].ID != "msg-live" {
		t.Errorf("active[1].ID = %q, want msg-live", active.Messages[1].ID)
	}
	// SweptCount is a stub zero in WP07 — see plan §2.8.
	if active.SweptCount != 0 {
		t.Errorf("active.SweptCount = %d, want 0 (WP07 stub)", active.SweptCount)
	}

	// Full view: every row including the archived original. The
	// archived row carries CompactedIntoID + ArchivedAt so the UI
	// can render the chip + faded background.
	all, err := api.ListMessagesAll(ctx, s.ID)
	if err != nil {
		t.Fatalf("ListMessagesAll: %v", err)
	}
	if len(all.Messages) != 3 {
		t.Fatalf("all len = %d, want 3", len(all.Messages))
	}
	if all.Messages[0].ID != "msg-archived" {
		t.Errorf("all[0].ID = %q, want msg-archived", all.Messages[0].ID)
	}
	if all.Messages[0].ArchivedAt == "" {
		t.Errorf("all[0].ArchivedAt empty; expected populated")
	}
	if all.Messages[0].CompactedIntoID != summaryID {
		t.Errorf("all[0].CompactedIntoID = %q, want %q",
			all.Messages[0].CompactedIntoID, summaryID)
	}
	if all.SweptCount != 0 {
		t.Errorf("all.SweptCount = %d, want 0 (WP07 stub)", all.SweptCount)
	}
}

// TestSendMessageWithBlocks_RejectsEmpty pins the validation
// guarantee — an empty / nil contentBlocks slice fails closed before
// any persistence work.
func TestSendMessageWithBlocks_RejectsEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	smgr := session.NewManager(session.NewMemoryStore())
	api := NewManagerAPI(smgr)
	s, err := api.Create(ctx, "alpha")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := api.SendMessageWithBlocks(ctx, s.ID, nil); !errors.Is(err, ErrEmptyContentBlocks) {
		t.Errorf("got %v, want ErrEmptyContentBlocks", err)
	}
	if _, err := api.SendMessageWithBlocks(ctx, s.ID, []ContentBlock{}); !errors.Is(err, ErrEmptyContentBlocks) {
		t.Errorf("got %v on empty slice, want ErrEmptyContentBlocks", err)
	}
	// Verify nothing was persisted on the failure path.
	msgs, _ := smgr.ListMessages(ctx, s.ID)
	if len(msgs) != 0 {
		t.Errorf("len = %d, want 0 after rejected sends", len(msgs))
	}
}
