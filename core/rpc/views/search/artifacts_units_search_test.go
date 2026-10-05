package search

// artifacts-as-units-01DOGF0C WP04: the unified search's artifacts
// corpus reads artifact units (units.kind='artifact') after the
// units/1104 migration renamed the legacy artifacts table. Real sqlite,
// rows written by the production artifacts store.

import (
	"context"
	"database/sql"
	"testing"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

func TestArtifactsSearcher_ReadsArtifactUnits(t *testing.T) {
	ctx := context.Background()
	db, err := storagesqlite.Open(storage.Config{DataDir: t.TempDir(), EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	raw := db.(interface{ SQL() *sql.DB }).SQL()

	pid := "proj-q"
	a, err := coreart.NewSQLStore(db).Insert(ctx, coreart.Artifact{
		SessionID: "sess-q", ProjectID: &pid, Title: "Quarterly report", MimeType: "text/markdown",
		ContentHash: "h", Source: coreart.SourceUserPin,
	})
	if err != nil {
		t.Fatalf("insert artifact: %v", err)
	}
	// A document unit with a matching title must NOT surface as an
	// artifact hit.
	if _, err := units.NewManager(units.NewSQLStore(db)).Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeSession, ScopeID: "sess-q", Classification: units.ClassPersonal,
		LoadPolicy: units.LoadOnDemand, Title: "Quarterly doc",
	}); err != nil {
		t.Fatalf("create doc unit: %v", err)
	}

	hits, err := (&artifactsSearcher{db: raw}).search(ctx, "Quarterly", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v, want exactly the one artifact", hits)
	}
	h := hits[0]
	if h.EntityID != a.ID || h.SessionID != "sess-q" || h.ProjectID != "proj-q" || h.Snippet != "Quarterly report (text/markdown)" {
		t.Errorf("hit = %+v", h)
	}
	if byMime, _ := (&artifactsSearcher{db: raw}).search(ctx, "markdown", 10); len(byMime) != 1 {
		t.Errorf("MIME search hits = %d, want 1", len(byMime))
	}
}
