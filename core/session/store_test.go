package session

import (
	"context"
	"errors"
	"testing"
	"time"
)

func mustRecord(id, name string, position int64, now time.Time) Record {
	return Record{
		ID:           id,
		Name:         name,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
		Position:     position,
	}
}

func TestMemStore_CreateAndGet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)

	r := mustRecord("s1", "first", 0, now)
	if err := s.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(ctx, "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "first" || got.Position != 0 {
		t.Errorf("got %+v, want name=first position=0", got)
	}
}

func TestMemStore_CreateRejectsEmptyName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	r := mustRecord("s1", "", 0, time.Now())
	if err := s.Create(ctx, r); !errors.Is(err, ErrInvalidName) {
		t.Errorf("got %v, want ErrInvalidName", err)
	}
}

func TestMemStore_CreateDuplicateRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	if err := s.Create(ctx, mustRecord("s1", "a", 0, now)); err != nil {
		t.Fatal(err)
	}
	err := s.Create(ctx, mustRecord("s1", "b", 1, now))
	if !errors.Is(err, ErrSessionExists) {
		t.Errorf("got %v, want ErrSessionExists", err)
	}
}

func TestMemStore_GetMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	_, err := s.Get(ctx, "ghost")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_ListByPosition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	if err := s.Create(ctx, mustRecord("s2", "second", 1, now)); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, mustRecord("s1", "first", 0, now)); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, mustRecord("s3", "third", 2, now)); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	wantOrder := []string{"s1", "s2", "s3"}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("List[%d].ID = %s, want %s", i, got[i].ID, id)
		}
	}
}

func TestMemStore_ListSkipsArchived(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	r := mustRecord("s1", "first", 0, now)
	archived := now.Add(time.Minute)
	r.ArchivedAt = &archived
	if err := s.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, mustRecord("s2", "second", 1, now)); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "s2" {
		t.Errorf("List = %+v, want [s2]", got)
	}
}

func TestMemStore_Rename(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, mustRecord("s1", "old", 0, now)); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	if err := s.Rename(ctx, "s1", "new", later); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "s1")
	if got.Name != "new" {
		t.Errorf("Name = %q, want new", got.Name)
	}
	if !got.UpdatedAt.Equal(later) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, later)
	}
}

func TestMemStore_RenameMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	err := s.Rename(ctx, "ghost", "x", time.Now())
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_RenameEmptyRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	_ = s.Create(ctx, mustRecord("s1", "ok", 0, now))
	if err := s.Rename(ctx, "s1", "", now); !errors.Is(err, ErrInvalidName) {
		t.Errorf("got %v, want ErrInvalidName", err)
	}
}

func TestMemStore_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	if err := s.Create(ctx, mustRecord("s1", "a", 0, now)); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "s1"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("after Delete: got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_DeleteMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	if err := s.Delete(ctx, "ghost"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_Reorder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b", "c"} {
		if err := s.Create(ctx, mustRecord(id, id, int64(i), now)); err != nil {
			t.Fatal(err)
		}
	}
	later := now.Add(time.Hour)
	if err := s.Reorder(ctx, []string{"c", "a", "b"}, later); err != nil {
		t.Fatal(err)
	}
	got, _ := s.List(ctx)
	wantOrder := []string{"c", "a", "b"}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("List[%d] = %s, want %s", i, got[i].ID, want)
		}
		if got[i].Position != int64(i) {
			t.Errorf("List[%d].Position = %d, want %d", i, got[i].Position, i)
		}
	}
}

func TestMemStore_ReorderRejectsUnknownID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	_ = s.Create(ctx, mustRecord("a", "a", 0, now))
	err := s.Reorder(ctx, []string{"a", "ghost"}, now)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
	// Mutation must roll back.
	got, _ := s.Get(ctx, "a")
	if got.Position != 0 {
		t.Errorf("Position changed despite failed Reorder: %d", got.Position)
	}
}

func TestMemStore_AppendMessageAssignsSequence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	if err := s.Create(ctx, mustRecord("s1", "a", 0, now)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		m := Message{
			ID:        "m" + string(rune('0'+i)),
			SessionID: "s1",
			Role:      RoleUser,
			Content:   "hello",
			CreatedAt: now,
		}
		out, err := s.AppendMessage(ctx, m)
		if err != nil {
			t.Fatalf("AppendMessage[%d]: %v", i, err)
		}
		if out.Sequence != int64(i) {
			t.Errorf("Sequence = %d, want %d", out.Sequence, i)
		}
	}
	msgs, err := s.ListMessages(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Errorf("len = %d, want 3", len(msgs))
	}
}

func TestMemStore_AppendMessage_UnknownSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	_, err := s.AppendMessage(ctx, Message{ID: "m1", SessionID: "ghost", Content: "x"})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_UpdateDraftAndScroll(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, mustRecord("s1", "a", 0, now)); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	if err := s.UpdateDraft(ctx, "s1", "hello world", later); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateScrollPosition(ctx, "s1", 42, later); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "s1")
	if got.Draft != "hello world" {
		t.Errorf("Draft = %q, want hello world", got.Draft)
	}
	if got.ScrollPosition != 42 {
		t.Errorf("ScrollPosition = %d, want 42", got.ScrollPosition)
	}
}

func TestMemStore_SetSystemPrompt_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, mustRecord("s1", "chat", 0, now)); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	if err := s.SetSystemPrompt(ctx, "s1", "you are helpful", ContextKindSystem, later); err != nil {
		t.Fatalf("SetSystemPrompt: %v", err)
	}
	got, _ := s.Get(ctx, "s1")
	if got.SystemPrompt != "you are helpful" {
		t.Errorf("SystemPrompt = %q, want 'you are helpful'", got.SystemPrompt)
	}
	if got.ContextKind != ContextKindSystem {
		t.Errorf("ContextKind = %q, want %q", got.ContextKind, ContextKindSystem)
	}
	if !got.UpdatedAt.Equal(later) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, later)
	}
}

func TestMemStore_SetSystemPrompt_RejectsInvalidKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	if err := s.Create(ctx, mustRecord("s1", "x", 0, now)); err != nil {
		t.Fatal(err)
	}
	err := s.SetSystemPrompt(ctx, "s1", "anything", "garbage", now)
	if !errors.Is(err, ErrInvalidContextKind) {
		t.Errorf("got %v, want ErrInvalidContextKind", err)
	}
	got, _ := s.Get(ctx, "s1")
	if got.SystemPrompt != "" || got.ContextKind != ContextKindSystem {
		t.Errorf("record mutated despite rejected kind: %+v", got)
	}
}

func TestMemStore_SetSystemPrompt_MissingSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	err := s.SetSystemPrompt(ctx, "ghost", "x", ContextKindSystem, time.Now())
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_Create_DefaultsContextKindWhenEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	if err := s.Create(ctx, mustRecord("s1", "x", 0, now)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "s1")
	if got.ContextKind != ContextKindSystem {
		t.Errorf("ContextKind = %q, want %q (default)", got.ContextKind, ContextKindSystem)
	}
}

func TestMemStore_UpdateLastActive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, mustRecord("s1", "a", 0, now)); err != nil {
		t.Fatal(err)
	}
	later := now.Add(2 * time.Hour)
	if err := s.UpdateLastActive(ctx, "s1", later); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "s1")
	if !got.LastActiveAt.Equal(later) {
		t.Errorf("LastActiveAt = %v, want %v", got.LastActiveAt, later)
	}
}

// TestMemStore_ApplyCompaction_FlipsOriginalsAndInsertsSummary asserts
// the WP08 transactional helper behaves correctly on the in-memory
// store: archived_at + compacted_into_id flip on every original, the
// summary row appears in ListMessages, and ListMessagesActive hides
// the originals.
func TestMemStore_ApplyCompaction_FlipsOriginalsAndInsertsSummary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, mustRecord("s1", "x", 0, now)); err != nil {
		t.Fatal(err)
	}
	// Append three messages: user, assistant, user.
	a, err := s.AppendMessage(ctx, Message{ID: "m1", SessionID: "s1", Role: RoleUser, Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AppendMessage(ctx, Message{ID: "m2", SessionID: "s1", Role: RoleAssistant, Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AppendMessage(ctx, Message{ID: "m3", SessionID: "s1", Role: RoleUser, Content: "again"})
	if err != nil {
		t.Fatal(err)
	}

	at := now.Add(time.Hour)
	summary := Message{
		ID:        "sum-1",
		Role:      RoleSystem,
		Content:   "[Earlier conversation summary: hi]",
		Sequence:  a.Sequence,
		CreatedAt: at,
	}
	if err := s.ApplyCompaction(ctx, "s1", summary, []string{"m1", "m2"}, at); err != nil {
		t.Fatalf("ApplyCompaction: %v", err)
	}

	all, err := s.ListMessages(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Errorf("ListMessages returned %d rows, want 4 (3 originals + summary)", len(all))
	}
	active, err := s.ListMessagesActive(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	// Live rows: m3 + summary (m1, m2 archived).
	if len(active) != 2 {
		t.Errorf("ListMessagesActive returned %d rows, want 2", len(active))
	}
	// Confirm the originals carry the flip.
	for _, m := range all {
		if m.ID == "m1" || m.ID == "m2" {
			if m.ArchivedAt == nil {
				t.Errorf("%s: ArchivedAt nil; want non-nil", m.ID)
			}
			if m.CompactedIntoID == nil || *m.CompactedIntoID != "sum-1" {
				t.Errorf("%s: CompactedIntoID = %v; want sum-1", m.ID, m.CompactedIntoID)
			}
		}
	}
	_ = b
}

// TestMemStore_DeleteArchivedBefore_DropsOldArchivedRows asserts the
// soft-archive sweep adapter walks the in-memory slice and tombstones
// rows whose archived_at is past the cutoff.
func TestMemStore_DeleteArchivedBefore_DropsOldArchivedRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, mustRecord("s1", "x", 0, now)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m1", "m2", "m3"} {
		if _, err := s.AppendMessage(ctx, Message{ID: id, SessionID: "s1", Role: RoleUser, Content: id}); err != nil {
			t.Fatal(err)
		}
	}
	// Archive m1 + m2 in the past, leave m3 live.
	old := now.Add(-100 * 24 * time.Hour)
	summary := Message{ID: "sum-x", Role: RoleSystem, Content: "[s]", Sequence: 0, CreatedAt: old}
	if err := s.ApplyCompaction(ctx, "s1", summary, []string{"m1", "m2"}, old); err != nil {
		t.Fatal(err)
	}

	cutoff := now.Add(-7 * 24 * time.Hour)
	deleted, oldest, newest, err := s.DeleteArchivedBefore(ctx, cutoff, 1000)
	if err != nil {
		t.Fatalf("DeleteArchivedBefore: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted=%d, want 2", deleted)
	}
	if oldest.IsZero() || newest.IsZero() {
		t.Errorf("oldest=%v newest=%v; both should be non-zero", oldest, newest)
	}
	// Verify the surviving rows.
	all, _ := s.ListMessages(ctx, "s1")
	for _, m := range all {
		if m.ID == "m1" || m.ID == "m2" {
			t.Errorf("%s should have been deleted", m.ID)
		}
	}
}

// TestMemStore_DeleteArchivedBefore_NeverDeletesSummary asserts the
// summary row (CompactedIntoID is nil, CompactedAt is non-nil) is
// excluded from the sweep regardless of its archived_at timestamp.
// In practice the engine never archives a summary; this test pins the
// belt-and-braces filter for defense-in-depth.
func TestMemStore_DeleteArchivedBefore_NeverDeletesSummary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, mustRecord("s1", "x", 0, now)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMessage(ctx, Message{ID: "m1", SessionID: "s1", Role: RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	// Insert a summary row directly via ApplyCompaction; it gets a
	// non-nil CompactedAt and nil CompactedIntoID per the schema
	// convention.
	old := now.Add(-100 * 24 * time.Hour)
	summary := Message{ID: "sum-1", Role: RoleSystem, Content: "[s]", Sequence: 0, CreatedAt: old}
	if err := s.ApplyCompaction(ctx, "s1", summary, []string{"m1"}, old); err != nil {
		t.Fatal(err)
	}
	cutoff := now.Add(-7 * 24 * time.Hour)
	deleted, _, _, err := s.DeleteArchivedBefore(ctx, cutoff, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Errorf("deleted=%d, want 1 (only m1)", deleted)
	}
	all, _ := s.ListMessages(ctx, "s1")
	foundSummary := false
	for _, m := range all {
		if m.ID == "sum-1" {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Error("summary row was deleted; sweep must never delete summaries")
	}
}

// ── auto_titled store method tests ──────────────────────────────────────────

func TestMemStore_AutoTitle_SetsNameAndFlag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, mustRecord("s1", "New session", 0, now)); err != nil {
		t.Fatal(err)
	}
	if err := s.AutoTitle(ctx, "s1", "Rust basics", now.Add(time.Second)); err != nil {
		t.Fatalf("AutoTitle: %v", err)
	}
	got, _ := s.Get(ctx, "s1")
	if got.Name != "Rust basics" {
		t.Errorf("Name = %q, want Rust basics", got.Name)
	}
	if !got.AutoTitled {
		t.Error("AutoTitled = false, want true")
	}
}

func TestMemStore_AutoTitle_SupersededWhenAlreadySet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	r := mustRecord("s1", "New session", 0, now)
	r.AutoTitled = true
	if err := s.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	err := s.AutoTitle(ctx, "s1", "Should not apply", now)
	if !errors.Is(err, ErrAutoTitleSuperseded) {
		t.Errorf("got %v, want ErrAutoTitleSuperseded", err)
	}
	got, _ := s.Get(ctx, "s1")
	if got.Name != "New session" {
		t.Errorf("Name changed despite superseded; got %q", got.Name)
	}
}

func TestMemStore_AutoTitle_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	err := s.AutoTitle(ctx, "ghost", "title", time.Now())
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_MarkAutoTitleAttempted_SetsFlag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	if err := s.Create(ctx, mustRecord("s1", "session", 0, now)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkAutoTitleAttempted(ctx, "s1", now.Add(time.Second)); err != nil {
		t.Fatalf("MarkAutoTitleAttempted: %v", err)
	}
	got, _ := s.Get(ctx, "s1")
	if !got.AutoTitled {
		t.Error("AutoTitled = false after MarkAutoTitleAttempted, want true")
	}
	if got.Name != "session" {
		t.Errorf("Name changed; got %q, want %q", got.Name, "session")
	}
}

func TestMemStore_MarkAutoTitleAttempted_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	err := s.MarkAutoTitleAttempted(ctx, "ghost", time.Now())
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_ClearTitle_ResetsNameAndFlag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	r := mustRecord("s1", "some title", 0, now)
	r.AutoTitled = true
	if err := s.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearTitle(ctx, "s1", now.Add(time.Second)); err != nil {
		t.Fatalf("ClearTitle: %v", err)
	}
	got, _ := s.Get(ctx, "s1")
	if got.Name != "" {
		t.Errorf("Name = %q, want empty", got.Name)
	}
	if got.AutoTitled {
		t.Error("AutoTitled = true after ClearTitle, want false")
	}
}

func TestMemStore_ClearTitle_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	err := s.ClearTitle(ctx, "ghost", time.Now())
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_Rename_SetsAutoTitled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	if err := s.Create(ctx, mustRecord("s1", "old", 0, now)); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, "s1", "new name", now.Add(time.Second)); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	got, _ := s.Get(ctx, "s1")
	if !got.AutoTitled {
		t.Error("AutoTitled = false after non-empty Rename, want true")
	}
}
