package artifacts_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/artifacts"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// openTestDB opens a fresh on-disk SQLite for store tests. Migration
// 0303 has already applied by the time storagesqlite.Open returns.
func openTestDB(t *testing.T) storage.DB {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close(context.Background())
	})
	return db
}

// seedSession writes a sessions row directly via the unified storage
// connection so the FK constraints on artifacts.session_id are
// satisfied without spinning up the full session.Manager.
func seedSession(t *testing.T, db storage.DB, sessionID string, projectID *string) {
	t.Helper()
	now := time.Now().UTC().UnixNano()
	if err := db.WriteTx(context.Background(), func(tx storage.WriteTx) error {
		var pid any
		if projectID != nil {
			pid = *projectID
		}
		_, err := tx.Exec(context.Background(), `
            INSERT INTO sessions
                (id, name, created_at, updated_at, last_active_at,
                 position, draft, scroll_position, archived_at,
                 system_prompt, context_kind, project_id)
            VALUES (?, ?, ?, ?, ?, 0, '', 0, NULL, '', 'system', ?)
        `, sessionID, sessionID, now, now, now, pid)
		return err
	}); err != nil {
		t.Fatalf("seed session %s: %v", sessionID, err)
	}
}

// seedProject writes a projects row directly. Mirror of seedSession
// for the project FK side.
func seedProject(t *testing.T, db storage.DB, projectID string) {
	t.Helper()
	now := time.Now().UTC().UnixNano()
	if err := db.WriteTx(context.Background(), func(tx storage.WriteTx) error {
		_, err := tx.Exec(context.Background(), `
            INSERT INTO projects
                (id, name, description, created_at, updated_at)
            VALUES (?, ?, '', ?, ?)
        `, projectID, projectID, now, now)
		return err
	}); err != nil {
		t.Fatalf("seed project %s: %v", projectID, err)
	}
}

func TestSQLStore_InsertAndGet_RoundTrip(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "s1", nil)
	store := artifacts.NewSQLStore(db)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	a := artifacts.Artifact{
		ID:          "a1",
		SessionID:   "s1",
		Title:       "tictactoe.html",
		MimeType:    "text/html",
		ContentHash: "abc",
		ByteSize:    42,
		Source:      artifacts.SourceCodeBlock,
		SourceRef: artifacts.ArtifactSourceRef{
			MessageID:      "m1",
			CodeBlockIndex: 2,
			Filename:       "tictactoe.html",
		},
		ScopeKind: artifacts.ScopeKindSession,
		CreatedAt: now,
	}
	got, err := store.Insert(ctx, a)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if got.ID != "a1" {
		t.Errorf("ID = %q", got.ID)
	}

	round, err := store.Get(ctx, "a1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if round.Title != "tictactoe.html" || round.MimeType != "text/html" {
		t.Errorf("Title/MimeType drift: %+v", round)
	}
	if round.SourceRef.MessageID != "m1" || round.SourceRef.CodeBlockIndex != 2 ||
		round.SourceRef.Filename != "tictactoe.html" {
		t.Errorf("SourceRef did not round-trip: %+v", round.SourceRef)
	}
	if !round.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt drift: got %v want %v", round.CreatedAt, now)
	}
	if round.ProjectID != nil {
		t.Errorf("ProjectID = %v, want nil", round.ProjectID)
	}
}

func TestSQLStore_Insert_AutoIDAndTimestamp(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "s1", nil)
	store := artifacts.NewSQLStore(db)

	a, err := store.Insert(context.Background(), artifacts.Artifact{
		SessionID:   "s1",
		MimeType:    "text/plain",
		ContentHash: "h",
		Source:      artifacts.SourceUserPin,
		SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m1"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if a.ID == "" {
		t.Errorf("expected fresh id; got empty")
	}
	if a.CreatedAt.IsZero() {
		t.Errorf("expected populated CreatedAt; got zero")
	}
	if a.ScopeKind != artifacts.ScopeKindSession {
		t.Errorf("ScopeKind default = %q, want session", a.ScopeKind)
	}
}

func TestSQLStore_Insert_RejectsUnknownSource(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "s1", nil)
	store := artifacts.NewSQLStore(db)
	_, err := store.Insert(context.Background(), artifacts.Artifact{
		SessionID: "s1",
		Source:    "invalid",
	})
	if !errors.Is(err, artifacts.ErrUnsupportedSource) {
		t.Errorf("got %v, want ErrUnsupportedSource", err)
	}
}

func TestSQLStore_List_FilterBySession(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "s1", nil)
	seedSession(t, db, "s2", nil)
	store := artifacts.NewSQLStore(db)
	ctx := context.Background()

	for i, sid := range []string{"s1", "s1", "s2"} {
		_, err := store.Insert(ctx, artifacts.Artifact{
			SessionID:   sid,
			MimeType:    "text/plain",
			ContentHash: "h",
			Source:      artifacts.SourceCodeBlock,
			SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m"},
			CreatedAt:   time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
		})
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	got, err := store.List(ctx, artifacts.ArtifactFilter{SessionID: "s1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List(s1) len = %d, want 2", len(got))
	}
	for _, a := range got {
		if a.SessionID != "s1" {
			t.Errorf("got artifact for session %q in s1 filter", a.SessionID)
		}
	}
}

func TestSQLStore_List_FilterByProject(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedProject(t, db, "p1")
	seedSession(t, db, "s1", strPtr("p1"))
	seedSession(t, db, "s2", nil)
	store := artifacts.NewSQLStore(db)
	ctx := context.Background()

	pid := "p1"
	_, _ = store.Insert(ctx, artifacts.Artifact{
		SessionID:   "s1",
		ProjectID:   &pid,
		MimeType:    "text/plain",
		ContentHash: "h",
		Source:      artifacts.SourceCodeBlock,
		SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m"},
		ScopeKind:   artifacts.ScopeKindProject,
	})
	_, _ = store.Insert(ctx, artifacts.Artifact{
		SessionID:   "s2",
		MimeType:    "text/plain",
		ContentHash: "h",
		Source:      artifacts.SourceCodeBlock,
		SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m"},
	})

	got, err := store.List(ctx, artifacts.ArtifactFilter{ProjectID: "p1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "s1" {
		t.Errorf("List(p1) = %+v", got)
	}
}

func TestSQLStore_List_FilterByMimePrefix(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "s1", nil)
	store := artifacts.NewSQLStore(db)
	ctx := context.Background()

	for _, mt := range []string{"image/png", "image/jpeg", "text/html"} {
		_, _ = store.Insert(ctx, artifacts.Artifact{
			SessionID:   "s1",
			MimeType:    mt,
			ContentHash: mt,
			Source:      artifacts.SourceToolOutput,
			SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m"},
		})
	}
	got, err := store.List(ctx, artifacts.ArtifactFilter{MimeTypePrefix: "image/"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List(image/) len = %d, want 2", len(got))
	}
}

func TestSQLStore_List_FilterBySource(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "s1", nil)
	store := artifacts.NewSQLStore(db)
	ctx := context.Background()

	for _, src := range []string{
		artifacts.SourceCodeBlock,
		artifacts.SourceToolOutput,
		artifacts.SourceUserPin,
		artifacts.SourceCodeBlock,
	} {
		_, _ = store.Insert(ctx, artifacts.Artifact{
			SessionID:   "s1",
			MimeType:    "text/plain",
			ContentHash: "h",
			Source:      src,
			SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m"},
		})
	}
	got, err := store.List(ctx, artifacts.ArtifactFilter{Source: artifacts.SourceCodeBlock})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List(code_block) len = %d, want 2", len(got))
	}
}

func TestSQLStore_UpdateScope_PromoteSessionToProject(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedProject(t, db, "p1")
	seedSession(t, db, "s1", strPtr("p1"))
	store := artifacts.NewSQLStore(db,
		artifacts.WithSessionProjectReader(
			artifacts.NewStaticSessionProjectReader(map[string]string{"s1": "p1"}),
		),
	)
	ctx := context.Background()

	a, err := store.Insert(ctx, artifacts.Artifact{
		SessionID:   "s1",
		MimeType:    "text/plain",
		ContentHash: "h",
		Source:      artifacts.SourceCodeBlock,
		SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	updated, err := store.UpdateScope(ctx, a.ID, artifacts.ScopeKindProject, "p1")
	if err != nil {
		t.Fatalf("UpdateScope: %v", err)
	}
	if updated.ScopeKind != artifacts.ScopeKindProject {
		t.Errorf("ScopeKind = %q, want project", updated.ScopeKind)
	}
	if updated.ProjectID == nil || *updated.ProjectID != "p1" {
		t.Errorf("ProjectID = %v, want p1", updated.ProjectID)
	}
}

func TestSQLStore_UpdateScope_RejectsWhenSessionHasNoProject(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "s1", nil)
	store := artifacts.NewSQLStore(db,
		artifacts.WithSessionProjectReader(
			artifacts.NewStaticSessionProjectReader(map[string]string{"s1": ""}),
		),
	)
	ctx := context.Background()
	a, _ := store.Insert(ctx, artifacts.Artifact{
		SessionID:   "s1",
		MimeType:    "text/plain",
		ContentHash: "h",
		Source:      artifacts.SourceCodeBlock,
		SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m"},
	})
	if _, err := store.UpdateScope(ctx, a.ID, artifacts.ScopeKindProject, ""); !errors.Is(err, artifacts.ErrUnsupportedScope) {
		t.Errorf("got %v, want ErrUnsupportedScope", err)
	}
}

func TestSQLStore_UpdateScope_DemoteToSessionClearsProject(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedProject(t, db, "p1")
	seedSession(t, db, "s1", strPtr("p1"))
	store := artifacts.NewSQLStore(db,
		artifacts.WithSessionProjectReader(
			artifacts.NewStaticSessionProjectReader(map[string]string{"s1": "p1"}),
		),
	)
	ctx := context.Background()
	pid := "p1"
	a, _ := store.Insert(ctx, artifacts.Artifact{
		SessionID:   "s1",
		ProjectID:   &pid,
		MimeType:    "text/plain",
		ContentHash: "h",
		Source:      artifacts.SourceCodeBlock,
		SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m"},
		ScopeKind:   artifacts.ScopeKindProject,
	})
	updated, err := store.UpdateScope(ctx, a.ID, artifacts.ScopeKindSession, "")
	if err != nil {
		t.Fatalf("UpdateScope demote: %v", err)
	}
	if updated.ProjectID != nil {
		t.Errorf("ProjectID = %v, want nil after demote", updated.ProjectID)
	}
	if updated.ScopeKind != artifacts.ScopeKindSession {
		t.Errorf("ScopeKind = %q, want session", updated.ScopeKind)
	}
}

func TestSQLStore_Delete_ReturnsContentHashAndRemovesRow(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "s1", nil)
	store := artifacts.NewSQLStore(db)
	ctx := context.Background()
	a, _ := store.Insert(ctx, artifacts.Artifact{
		SessionID:   "s1",
		MimeType:    "text/plain",
		ContentHash: "deadbeef",
		Source:      artifacts.SourceCodeBlock,
		SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m"},
	})
	hash, err := store.Delete(ctx, a.ID)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if hash != "deadbeef" {
		t.Errorf("Delete content_hash = %q, want deadbeef", hash)
	}
	if _, err := store.Get(ctx, a.ID); !errors.Is(err, artifacts.ErrArtifactNotFound) {
		t.Errorf("Get after Delete = %v, want ErrArtifactNotFound", err)
	}
	if _, err := store.Delete(ctx, a.ID); !errors.Is(err, artifacts.ErrArtifactNotFound) {
		t.Errorf("double Delete = %v, want ErrArtifactNotFound", err)
	}
}

func TestSQLStore_RefcountFor(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "s1", nil)
	store := artifacts.NewSQLStore(db)
	ctx := context.Background()

	if n, err := store.RefcountFor(ctx, "ghost"); err != nil || n != 0 {
		t.Errorf("RefcountFor(ghost) = (%d, %v), want (0, nil)", n, err)
	}
	for i := 0; i < 3; i++ {
		_, _ = store.Insert(ctx, artifacts.Artifact{
			SessionID:   "s1",
			MimeType:    "text/plain",
			ContentHash: "samehash",
			Source:      artifacts.SourceCodeBlock,
			SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m"},
		})
	}
	n, err := store.RefcountFor(ctx, "samehash")
	if err != nil {
		t.Fatalf("RefcountFor: %v", err)
	}
	if n != 3 {
		t.Errorf("RefcountFor(samehash) = %d, want 3", n)
	}
}

// TestSQLStore_SessionDeleteCascadesArtifacts verifies the FK ON
// DELETE CASCADE: removing a sessions row drops every artifact that
// references it.
func TestSQLStore_SessionDeleteCascadesArtifacts(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "s1", nil)
	store := artifacts.NewSQLStore(db)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		_, _ = store.Insert(ctx, artifacts.Artifact{
			SessionID:   "s1",
			MimeType:    "text/plain",
			ContentHash: "h",
			Source:      artifacts.SourceCodeBlock,
			SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m"},
		})
	}
	got, _ := store.List(ctx, artifacts.ArtifactFilter{SessionID: "s1"})
	if len(got) != 2 {
		t.Fatalf("setup: List len = %d, want 2", len(got))
	}

	if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, "DELETE FROM sessions WHERE id = ?", "s1")
		return err
	}); err != nil {
		t.Fatalf("delete sessions row: %v", err)
	}
	got, _ = store.List(ctx, artifacts.ArtifactFilter{SessionID: "s1"})
	if len(got) != 0 {
		t.Errorf("after session cascade: List len = %d, want 0", len(got))
	}
}

// TestSQLStore_ProjectDeleteSetsArtifactProjectNull verifies the FK ON
// DELETE SET NULL: removing a project severs the project_id link
// without losing the artifact row, and scope_kind is preserved per
// FR-015 (caller decides demotion semantics).
func TestSQLStore_ProjectDeleteSetsArtifactProjectNull(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedProject(t, db, "p1")
	seedSession(t, db, "s1", strPtr("p1"))
	store := artifacts.NewSQLStore(db)
	ctx := context.Background()

	pid := "p1"
	a, _ := store.Insert(ctx, artifacts.Artifact{
		SessionID:   "s1",
		ProjectID:   &pid,
		MimeType:    "text/plain",
		ContentHash: "h",
		Source:      artifacts.SourceCodeBlock,
		SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m"},
		ScopeKind:   artifacts.ScopeKindProject,
	})

	// Detach the session from the project before deleting (sessions
	// have ON DELETE SET NULL on project_id too — but we want to
	// isolate the artifact-side cascade behavior).
	if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, "UPDATE sessions SET project_id = NULL WHERE id = ?", "s1")
		return err
	}); err != nil {
		t.Fatalf("detach session: %v", err)
	}
	if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, "DELETE FROM projects WHERE id = ?", "p1")
		return err
	}); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	round, err := store.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get after project delete: %v", err)
	}
	if round.ProjectID != nil {
		t.Errorf("ProjectID = %v, want nil after project delete", round.ProjectID)
	}
	// scope_kind stays project — caller decides whether to demote.
	if round.ScopeKind != artifacts.ScopeKindProject {
		t.Errorf("ScopeKind = %q, want project preserved", round.ScopeKind)
	}
}

// TestSQLStore_GetNotFound exercises the sentinel.
func TestSQLStore_GetNotFound(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store := artifacts.NewSQLStore(db)
	if _, err := store.Get(context.Background(), "ghost"); !errors.Is(err, artifacts.ErrArtifactNotFound) {
		t.Errorf("got %v, want ErrArtifactNotFound", err)
	}
}

// TestArtifactsRefcountSource_Refcount exercises the structural
// adapter that WP02 will plug into MediaStore.RegisterRefcountSource.
func TestArtifactsRefcountSource_Refcount(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "s1", nil)
	store := artifacts.NewSQLStore(db)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_, _ = store.Insert(ctx, artifacts.Artifact{
			SessionID:   "s1",
			MimeType:    "text/plain",
			ContentHash: "samehash",
			Source:      artifacts.SourceCodeBlock,
			SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m"},
		})
	}
	src := artifacts.ArtifactsRefcountSource{Store: store}
	n, err := src.Refcount(ctx, "samehash")
	if err != nil {
		t.Fatalf("Refcount: %v", err)
	}
	if n != 2 {
		t.Errorf("Refcount = %d, want 2", n)
	}
	if n, _ := (artifacts.ArtifactsRefcountSource{}).Refcount(ctx, "x"); n != 0 {
		t.Errorf("nil-store Refcount = %d, want 0", n)
	}
}

func strPtr(s string) *string { return &s }
