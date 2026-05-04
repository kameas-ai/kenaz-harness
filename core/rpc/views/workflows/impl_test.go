package workflows

import (
	"context"
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
	corewf "github.com/sigil-tech/kaneaz-harness/core/workflows"
	wfcatalog "github.com/sigil-tech/kaneaz-harness/core/workflows/catalog"

	_ "modernc.org/sqlite"
)

func newTestAPI(t *testing.T) *API {
	t.Helper()
	wfs, errs := corewf.LoadBuiltins()
	for _, e := range errs {
		t.Fatalf("LoadBuiltins: %v", e)
	}
	if len(wfs) == 0 {
		t.Fatal("no builtins")
	}
	return New(Config{Engine: corewf.NewEngine(), Catalog: wfs})
}

func TestList_ReturnsBundledWorkflows(t *testing.T) {
	api := newTestAPI(t)
	got, err := api.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected at least one bundled workflow")
	}
	found := false
	for _, s := range got {
		if s.ID == "plan_implement_review" {
			found = true
			if s.StepCount == 0 || s.Name == "" {
				t.Errorf("summary fields not populated: %+v", s)
			}
		}
	}
	if !found {
		t.Errorf("plan_implement_review not in catalog")
	}
}

func TestGet_ReturnsFullWorkflow(t *testing.T) {
	api := newTestAPI(t)
	w, err := api.Get(context.Background(), "plan_implement_review")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(w.Steps) != 4 {
		t.Errorf("steps: got %d want 4", len(w.Steps))
	}
}

func TestGet_UnknownIDErrors(t *testing.T) {
	api := newTestAPI(t)
	_, err := api.Get(context.Background(), "no-such-flow")
	if !errors.Is(err, corewf.ErrWorkflowNotFound) {
		t.Errorf("want ErrWorkflowNotFound, got %v", err)
	}
}

func TestRun_ExecutesAllSteps(t *testing.T) {
	api := newTestAPI(t)
	res, err := api.Run(context.Background(), "plan_implement_review", map[string]string{
		"task": "Build a feature",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != "completed" {
		t.Errorf("status: got %q want completed (err=%s)", res.Status, res.Err)
	}
	if len(res.Steps) != 4 {
		t.Errorf("steps: got %d want 4", len(res.Steps))
	}
	for _, s := range res.Steps {
		if s.Status != "completed" {
			t.Errorf("step %s status: %s", s.Name, s.Status)
		}
		if s.Output == "" {
			t.Errorf("step %s: empty output", s.Name)
		}
	}
}

type recordingPublisher struct{ events []any }

func (r *recordingPublisher) Publish(_ string, payload any) {
	r.events = append(r.events, payload)
}

func TestRun_PublishesProgress(t *testing.T) {
	wfs, _ := corewf.LoadBuiltins()
	pub := &recordingPublisher{}
	api := New(Config{Engine: corewf.NewEngine(), Catalog: wfs, Publisher: pub})
	_, err := api.Run(context.Background(), "plan_implement_review", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 4 steps × 2 transitions (running + completed) = 8 events.
	if len(pub.events) != 8 {
		t.Errorf("progress events: got %d want 8", len(pub.events))
	}
}

func TestDisabled_BlocksAllMethods(t *testing.T) {
	api := New(Config{Disabled: true})
	if _, err := api.List(context.Background()); !errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("List: want ErrFeatureDisabled got %v", err)
	}
	if _, err := api.Get(context.Background(), "x"); !errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("Get: want ErrFeatureDisabled got %v", err)
	}
	if _, err := api.Run(context.Background(), "x", nil); !errors.Is(err, ErrFeatureDisabled) {
		t.Errorf("Run: want ErrFeatureDisabled got %v", err)
	}
}

func TestRun_NilEngineErrors(t *testing.T) {
	wfs, _ := corewf.LoadBuiltins()
	api := New(Config{Catalog: wfs})
	_, err := api.Run(context.Background(), "plan_implement_review", nil)
	if !errors.Is(err, ErrEngineUnavailable) {
		t.Errorf("want ErrEngineUnavailable, got %v", err)
	}
}

// newWP07TestStore returns a real corewf.Store backed by an on-disk
// sqlite DB so the Save/Delete RPC tests exercise the same code path
// production hits. A fake-Store route would need to fabricate the
// unexported Workflow metadata fields, which would force a
// not-allowed change to the WP06 storage API.
func newWP07TestStore(t *testing.T) corewf.Store {
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
	return corewf.NewSQLiteStore(db)
}

const wp07ValidYAML = `
id: wp07_test
name: "WP07 test flow"
version: 1
steps:
  - name: a
    kind: shell
    cmd: "echo"
    args: ["hi"]
`

func TestSave_YAMLImportPath(t *testing.T) {
	api := New(Config{Catalog: nil, Store: newWP07TestStore(t)})

	out, err := api.Save(context.Background(), SaveInput{YAML: wp07ValidYAML})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if out.ID == "" || out.ID == "wp07_test" {
		// ImportYAML must rewrite the id.
		t.Errorf("Save: expected fresh id, got %q", out.ID)
	}
	if out.Version != 1 {
		t.Errorf("Save: version = %d, want 1", out.Version)
	}
	if out.Hash == "" {
		t.Errorf("Save: empty hash")
	}
	// Catalog should now include the freshly saved row tagged "user".
	list, err := api.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, s := range list {
		if s.ID == out.ID {
			found = true
			if s.Source != "user" {
				t.Errorf("Source = %q, want user", s.Source)
			}
		}
	}
	if !found {
		t.Errorf("saved workflow %q missing from catalog", out.ID)
	}
}

func TestSave_WorkflowPath_IsIdempotent(t *testing.T) {
	api := New(Config{Catalog: nil, Store: newWP07TestStore(t)})

	wf := Workflow{
		ID: "wp07_idem", Name: "Idem", Version: 1,
		Steps: []Step{{Name: "a", Kind: "shell", Cmd: "echo", Args: []string{"hi"}}},
	}
	first, err := api.Save(context.Background(), SaveInput{Workflow: &wf})
	if err != nil {
		t.Fatalf("Save#1: %v", err)
	}
	second, err := api.Save(context.Background(), SaveInput{Workflow: &wf})
	if err != nil {
		t.Fatalf("Save#2: %v", err)
	}
	if second.Version != first.Version {
		t.Errorf("idempotent re-save bumped version: %d → %d", first.Version, second.Version)
	}
	if second.Hash != first.Hash {
		t.Errorf("hash drifted on no-op Save")
	}
}

func TestSave_RequiresInput(t *testing.T) {
	api := New(Config{Store: newWP07TestStore(t)})
	_, err := api.Save(context.Background(), SaveInput{})
	if !errors.Is(err, ErrInvalidSaveInput) {
		t.Errorf("want ErrInvalidSaveInput, got %v", err)
	}
}

func TestSave_NoStoreErrors(t *testing.T) {
	api := New(Config{})
	_, err := api.Save(context.Background(), SaveInput{YAML: wp07ValidYAML})
	if !errors.Is(err, ErrStorageUnavailable) {
		t.Errorf("want ErrStorageUnavailable, got %v", err)
	}
}

func TestDelete_HappyPath(t *testing.T) {
	api := New(Config{Store: newWP07TestStore(t)})

	out, err := api.Save(context.Background(), SaveInput{YAML: wp07ValidYAML})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := api.Delete(context.Background(), out.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Catalog no longer contains the row.
	list, _ := api.List(context.Background())
	for _, s := range list {
		if s.ID == out.ID {
			t.Errorf("deleted workflow %q still in catalog", out.ID)
		}
	}
}

func TestDelete_MissingReturnsNotFound(t *testing.T) {
	api := New(Config{Store: newWP07TestStore(t)})
	err := api.Delete(context.Background(), "no-such-id")
	if !errors.Is(err, corewf.ErrWorkflowNotFound) {
		t.Errorf("want ErrWorkflowNotFound, got %v", err)
	}
}

func TestDelete_NoStoreErrors(t *testing.T) {
	api := New(Config{})
	err := api.Delete(context.Background(), "anything")
	if !errors.Is(err, ErrStorageUnavailable) {
		t.Errorf("want ErrStorageUnavailable, got %v", err)
	}
}

// --- WP03 Catalog RPC tests ---

// TestCatalogList_NoCatalogReturnsUnavailable confirms the nil-catalog
// guard.
func TestCatalogList_NoCatalogReturnsUnavailable(t *testing.T) {
	api := New(Config{Engine: corewf.NewEngine()})
	_, err := api.Catalog_List(context.Background())
	if !errors.Is(err, ErrCatalogUnavailable) {
		t.Errorf("want ErrCatalogUnavailable, got %v", err)
	}
}

// TestCatalogList_ReturnsCatalogEntries confirms that Catalog_List
// surfaces the builtin entries when a real catalog is wired.
func TestCatalogList_ReturnsCatalogEntries(t *testing.T) {
	wfCatalog := newTestCatalog(t)
	api := New(Config{Engine: corewf.NewEngine(), WorkflowCatalog: wfCatalog})

	entries, err := api.Catalog_List(context.Background())
	if err != nil {
		t.Fatalf("Catalog_List: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one catalog entry")
	}
	found := false
	for _, e := range entries {
		if e.ID == "plan_implement_review" {
			found = true
			if e.Name == "" {
				t.Error("entry.Name must not be empty")
			}
			if e.InstallStatus == "" {
				t.Error("entry.InstallStatus must not be empty")
			}
		}
	}
	if !found {
		t.Error("plan_implement_review not in Catalog_List")
	}
}

// TestCatalogGet_ReturnsPreview confirms Catalog_Get returns both YAML
// and structured entry metadata.
func TestCatalogGet_ReturnsPreview(t *testing.T) {
	wfCatalog := newTestCatalog(t)
	api := New(Config{Engine: corewf.NewEngine(), WorkflowCatalog: wfCatalog})

	preview, err := api.Catalog_Get(context.Background(), "plan_implement_review")
	if err != nil {
		t.Fatalf("Catalog_Get: %v", err)
	}
	if preview.YAMLSource == "" {
		t.Error("CatalogPreview.YAMLSource must not be empty")
	}
	if preview.Entry.ID != "plan_implement_review" {
		t.Errorf("Entry.ID: got %q want plan_implement_review", preview.Entry.ID)
	}
}

// TestCatalogInstall_PersistsAndReturnsRef confirms that Catalog_Install
// stores the workflow and returns a non-empty WorkflowID.
func TestCatalogInstall_PersistsAndReturnsRef(t *testing.T) {
	store := newWP07TestStore(t)
	wfCatalog := newTestCatalogWithStore(t, store)
	api := New(Config{
		Engine:          corewf.NewEngine(),
		Store:           store,
		WorkflowCatalog: wfCatalog,
	})

	result, err := api.Catalog_Install(context.Background(), "plan_implement_review")
	if err != nil {
		t.Fatalf("Catalog_Install: %v", err)
	}
	if result.WorkflowID == "" {
		t.Error("CatalogInstallResult.WorkflowID must not be empty")
	}
}

// TestCatalogInstall_NoCatalogReturnsUnavailable.
func TestCatalogInstall_NoCatalogReturnsUnavailable(t *testing.T) {
	api := New(Config{Engine: corewf.NewEngine()})
	_, err := api.Catalog_Install(context.Background(), "plan_implement_review")
	if !errors.Is(err, ErrCatalogUnavailable) {
		t.Errorf("want ErrCatalogUnavailable, got %v", err)
	}
}

// newTestCatalog returns a catalog.Catalog backed by the embedded builtins.
func newTestCatalog(t *testing.T) wfcatalog.Catalog {
	t.Helper()
	return wfcatalog.New(wfcatalog.Config{})
}

// newTestCatalogWithStore returns a catalog.Catalog backed by the embedded
// builtins plus the provided Store so Install can persist.
func newTestCatalogWithStore(t *testing.T, store corewf.Store) wfcatalog.Catalog {
	t.Helper()
	return wfcatalog.New(wfcatalog.Config{Store: store})
}
