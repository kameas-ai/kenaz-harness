package workflows_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
	"github.com/sigil-tech/kaneaz-harness/core/workflows"

	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T) (workflows.Store, storage.DB) {
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
	return workflows.NewSQLiteStore(db), db
}

const validYAML = `
id: my_flow
name: "My flow"
version: 1
inputs:
  - name: ref
    kind: string
    default: "main"
steps:
  - name: a
    kind: shell
    cmd: "echo"
    args: ["hello"]
  - name: b
    kind: model_turn
    user_prompt: |
      summarize ${step.a.output}
`

const validYAMLChanged = `
id: my_flow
name: "My flow updated"
version: 1
inputs:
  - name: ref
    kind: string
    default: "main"
steps:
  - name: a
    kind: shell
    cmd: "echo"
    args: ["world"]
  - name: b
    kind: model_turn
    user_prompt: |
      summarize ${step.a.output}
`

func TestSQLiteStore_SaveAndLoad(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	w, err := workflows.LoadYAML([]byte(validYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}

	saved, err := store.Save(ctx, w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Version != 1 {
		t.Errorf("first Save version = %d, want 1", saved.Version)
	}
	if saved.Hash() == "" {
		t.Errorf("expected non-empty hash after Save")
	}
	if saved.CreatedAt().IsZero() || saved.UpdatedAt().IsZero() {
		t.Errorf("timestamps not populated: created=%v updated=%v", saved.CreatedAt(), saved.UpdatedAt())
	}

	loaded, err := store.Load(ctx, "my_flow")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ID != "my_flow" || loaded.Name != "My flow" {
		t.Errorf("Load: got id=%q name=%q", loaded.ID, loaded.Name)
	}
	if loaded.Version != 1 {
		t.Errorf("Load version = %d, want 1", loaded.Version)
	}
	if loaded.YAMLSource() == "" {
		t.Errorf("Load: yaml_source empty")
	}
}

func TestSQLiteStore_SaveNoOpOnSameHash(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	w, err := workflows.LoadYAML([]byte(validYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	first, err := store.Save(ctx, w)
	if err != nil {
		t.Fatalf("Save#1: %v", err)
	}

	// Second Save with the identical YAML must not bump version.
	second, err := store.Save(ctx, w)
	if err != nil {
		t.Fatalf("Save#2: %v", err)
	}
	if second.Version != first.Version {
		t.Errorf("no-op Save bumped version: %d → %d", first.Version, second.Version)
	}
	if second.Hash() != first.Hash() {
		t.Errorf("hash drifted on no-op Save")
	}

	hist, err := store.History(ctx, "my_flow")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 1 {
		t.Errorf("history len = %d, want 1 (no-op should not append)", len(hist))
	}
}

func TestSQLiteStore_SaveBumpsVersionOnHashChange(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	w, err := workflows.LoadYAML([]byte(validYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if _, err := store.Save(ctx, w); err != nil {
		t.Fatalf("Save#1: %v", err)
	}

	w2, err := workflows.LoadYAML([]byte(validYAMLChanged))
	if err != nil {
		t.Fatalf("LoadYAML#2: %v", err)
	}
	saved, err := store.Save(ctx, w2)
	if err != nil {
		t.Fatalf("Save#2: %v", err)
	}
	if saved.Version != 2 {
		t.Errorf("Save#2 version = %d, want 2", saved.Version)
	}

	hist, err := store.History(ctx, "my_flow")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("history len = %d, want 2", len(hist))
	}
	if hist[0].Version != 2 || hist[1].Version != 1 {
		t.Errorf("history versions newest-first: %d,%d", hist[0].Version, hist[1].Version)
	}
}

func TestSQLiteStore_List(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	for _, body := range []string{validYAML, strings.ReplaceAll(validYAML, "my_flow", "other_flow")} {
		w, err := workflows.LoadYAML([]byte(body))
		if err != nil {
			t.Fatalf("LoadYAML: %v", err)
		}
		if _, err := store.Save(ctx, w); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List len = %d, want 2", len(list))
	}
	ids := map[string]bool{list[0].ID: true, list[1].ID: true}
	if !ids["my_flow"] || !ids["other_flow"] {
		t.Errorf("List ids = %v, want my_flow + other_flow", ids)
	}
}

func TestSQLiteStore_DeleteCascadesHistory(t *testing.T) {
	t.Parallel()
	store, db := openTestStore(t)
	ctx := context.Background()

	w, err := workflows.LoadYAML([]byte(validYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if _, err := store.Save(ctx, w); err != nil {
		t.Fatalf("Save#1: %v", err)
	}
	w2, err := workflows.LoadYAML([]byte(validYAMLChanged))
	if err != nil {
		t.Fatalf("LoadYAML#2: %v", err)
	}
	if _, err := store.Save(ctx, w2); err != nil {
		t.Fatalf("Save#2: %v", err)
	}

	if err := store.Delete(ctx, "my_flow"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = store.Load(ctx, "my_flow")
	if !errors.Is(err, workflows.ErrWorkflowNotFound) {
		t.Errorf("Load after Delete: want ErrWorkflowNotFound, got %v", err)
	}

	// Confirm cascade landed on workflow_versions.
	var n int
	if err := db.Reader().QueryRow(ctx,
		`SELECT COUNT(*) FROM workflow_versions WHERE workflow_id = ?`, "my_flow").
		Scan(&n); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if n != 0 {
		t.Errorf("history rows after Delete = %d, want 0", n)
	}
}

func TestSQLiteStore_LoadUnknownReturnsErrWorkflowNotFound(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()
	_, err := store.Load(ctx, "does_not_exist")
	if !errors.Is(err, workflows.ErrWorkflowNotFound) {
		t.Errorf("want ErrWorkflowNotFound, got %v", err)
	}
}

func TestSQLiteStore_HistoryUnknownReturnsErrWorkflowNotFound(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()
	_, err := store.History(ctx, "missing")
	if !errors.Is(err, workflows.ErrWorkflowNotFound) {
		t.Errorf("want ErrWorkflowNotFound, got %v", err)
	}
}

func TestSQLiteStore_ConcurrentSavesSerialize(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	// Seed v1.
	w, err := workflows.LoadYAML([]byte(validYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if _, err := store.Save(ctx, w); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	// Two concurrent writers each push a *different* payload.
	w2, err := workflows.LoadYAML([]byte(validYAMLChanged))
	if err != nil {
		t.Fatalf("LoadYAML#2: %v", err)
	}
	w3, err := workflows.LoadYAML([]byte(strings.Replace(validYAMLChanged, "updated", "updated-again", 1)))
	if err != nil {
		t.Fatalf("LoadYAML#3: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for _, payload := range []workflows.Workflow{w2, w3} {
		wg.Add(1)
		go func(p workflows.Workflow) {
			defer wg.Done()
			if _, err := store.Save(ctx, p); err != nil {
				errCh <- err
			}
		}(payload)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent Save: %v", err)
	}

	// After two distinct-hash saves, version should be exactly 3
	// (1 seed + 2 bumps) — no missed updates from racing transactions.
	loaded, err := store.Load(ctx, "my_flow")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version != 3 {
		t.Errorf("post-concurrent version = %d, want 3", loaded.Version)
	}
	hist, err := store.History(ctx, "my_flow")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 3 {
		t.Errorf("history len = %d, want 3", len(hist))
	}
}

func TestExportYAML_RoundTrip(t *testing.T) {
	t.Parallel()
	w, err := workflows.LoadYAML([]byte(validYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	out, err := workflows.ExportYAML(w)
	if err != nil {
		t.Fatalf("ExportYAML: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("ExportYAML returned empty bytes")
	}
	w2, err := workflows.LoadYAML(out)
	if err != nil {
		t.Fatalf("re-load ExportYAML: %v", err)
	}
	if w2.ID != w.ID {
		t.Errorf("round-trip id: got %q want %q", w2.ID, w.ID)
	}
}

func TestExportYAML_ReturnsCachedSourceWhenLoaded(t *testing.T) {
	t.Parallel()
	store, _ := openTestStore(t)
	ctx := context.Background()

	w, err := workflows.LoadYAML([]byte(validYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if _, err := store.Save(ctx, w); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load(ctx, "my_flow")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := workflows.ExportYAML(loaded)
	if err != nil {
		t.Fatalf("ExportYAML: %v", err)
	}
	if string(out) != loaded.YAMLSource() {
		t.Errorf("ExportYAML did not return cached yaml_source")
	}
}

func TestImportYAML_AssignsFreshID(t *testing.T) {
	t.Parallel()
	w, err := workflows.ImportYAML([]byte(validYAML))
	if err != nil {
		t.Fatalf("ImportYAML: %v", err)
	}
	if w.ID == "my_flow" {
		t.Errorf("ImportYAML did not rewrite id; still %q", w.ID)
	}
	if !strings.HasPrefix(w.ID, "wf-") {
		t.Errorf("ImportYAML id = %q, want wf- prefix", w.ID)
	}
	if w.YAMLSource() == "" {
		t.Errorf("ImportYAML left yaml_source empty")
	}
	// The imported workflow must be Save-able without further massage.
	store, _ := openTestStore(t)
	if _, err := store.Save(context.Background(), w); err != nil {
		t.Fatalf("Save imported: %v", err)
	}
}

func TestImportYAML_RejectsInvalidPayload(t *testing.T) {
	t.Parallel()
	_, err := workflows.ImportYAML([]byte(`id: "Bad ID"`))
	if err == nil {
		t.Fatalf("expected error on invalid payload")
	}
}
