package artifacts_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/artifacts"
)

// TestMemStore_GlobalScope_Insert verifies that ScopeKindGlobal is
// accepted by both the memory and SQL stores.
func TestMemStore_GlobalScope_Insert(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := artifacts.NewMemoryStore()

	got, err := st.Insert(ctx, artifacts.Artifact{
		SessionID:   "s1",
		MimeType:    "text/plain",
		ContentHash: "hashglobal",
		Source:      artifacts.SourceUserPin,
		SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m1"},
		ScopeKind:   artifacts.ScopeKindGlobal,
	})
	if err != nil {
		t.Fatalf("Insert global scope: %v", err)
	}
	if got.ScopeKind != artifacts.ScopeKindGlobal {
		t.Errorf("ScopeKind = %q, want %q", got.ScopeKind, artifacts.ScopeKindGlobal)
	}
}

// TestMemStore_GlobalScope_UpdateScope verifies that UpdateScope accepts
// ScopeKindGlobal as a valid target and clears ProjectID.
func TestMemStore_GlobalScope_UpdateScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := artifacts.NewMemoryStore(artifacts.WithMemSessionProjectReader(
		artifacts.NewStaticSessionProjectReader(map[string]string{
			"s1": "proj-1",
		}),
	))

	pid := "proj-1"
	inserted, err := st.Insert(ctx, artifacts.Artifact{
		SessionID:   "s1",
		ProjectID:   &pid,
		MimeType:    "text/plain",
		ContentHash: "h1",
		Source:      artifacts.SourceCodeBlock,
		SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m1"},
		ScopeKind:   artifacts.ScopeKindProject,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	updated, err := st.UpdateScope(ctx, inserted.ID, artifacts.ScopeKindGlobal, "")
	if err != nil {
		t.Fatalf("UpdateScope to global: %v", err)
	}
	if updated.ScopeKind != artifacts.ScopeKindGlobal {
		t.Errorf("ScopeKind = %q, want global", updated.ScopeKind)
	}
	if updated.ProjectID != nil {
		t.Errorf("ProjectID = %v, want nil for global scope", updated.ProjectID)
	}
}

// TestMemStore_GlobalScope_RejectsUnknown verifies that an unknown scope
// is still rejected.
func TestMemStore_GlobalScope_RejectsUnknown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := artifacts.NewMemoryStore()

	_, err := st.Insert(ctx, artifacts.Artifact{
		SessionID:   "s1",
		MimeType:    "text/plain",
		ContentHash: "h",
		Source:      artifacts.SourceUserPin,
		SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m1"},
		ScopeKind:   "universe",
	})
	if !errors.Is(err, artifacts.ErrUnsupportedScope) {
		t.Errorf("got %v, want ErrUnsupportedScope", err)
	}
}

// TestSQLStore_GlobalScope_Insert verifies the SQL store accepts
// ScopeKindGlobal after migration 0332.
func TestSQLStore_GlobalScope_Insert(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "s1", nil)
	store := artifacts.NewSQLStore(db)
	ctx := context.Background()

	got, err := store.Insert(ctx, artifacts.Artifact{
		SessionID:   "s1",
		MimeType:    "text/plain",
		ContentHash: "hashglobal",
		ByteSize:    5,
		Source:      artifacts.SourceUserPin,
		SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m1"},
		ScopeKind:   artifacts.ScopeKindGlobal,
	})
	if err != nil {
		t.Fatalf("Insert global scope: %v", err)
	}
	if got.ScopeKind != artifacts.ScopeKindGlobal {
		t.Errorf("ScopeKind = %q, want global", got.ScopeKind)
	}

	// Verify round-trip via Get.
	round, err := store.Get(ctx, got.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if round.ScopeKind != artifacts.ScopeKindGlobal {
		t.Errorf("round-trip ScopeKind = %q, want global", round.ScopeKind)
	}
}

// TestSQLStore_GlobalScope_List verifies filtering by ScopeKindGlobal
// works correctly.
func TestSQLStore_GlobalScope_List(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	seedSession(t, db, "s1", nil)
	seedProject(t, db, "p1")
	seedSession(t, db, "s2", strPtr("p1"))
	store := artifacts.NewSQLStore(db)
	ctx := context.Background()

	// Insert one global, one session, one project artifact.
	for _, scope := range []string{
		artifacts.ScopeKindGlobal,
		artifacts.ScopeKindSession,
	} {
		_, err := store.Insert(ctx, artifacts.Artifact{
			SessionID:   "s1",
			MimeType:    "text/plain",
			ContentHash: "h" + scope,
			ByteSize:    1,
			Source:      artifacts.SourceUserPin,
			SourceRef:   artifacts.ArtifactSourceRef{MessageID: "m1"},
			ScopeKind:   scope,
		})
		if err != nil {
			t.Fatalf("Insert %s: %v", scope, err)
		}
	}

	got, err := store.List(ctx, artifacts.ArtifactFilter{ScopeKind: artifacts.ScopeKindGlobal})
	if err != nil {
		t.Fatalf("List global: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("List(global) len = %d, want 1", len(got))
	}
	if got[0].ScopeKind != artifacts.ScopeKindGlobal {
		t.Errorf("ScopeKind = %q, want global", got[0].ScopeKind)
	}
}
