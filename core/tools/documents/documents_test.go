package documents_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/docs"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	"github.com/kameas-ai/kenaz-harness/core/tools/documents"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

func newService(t *testing.T) *docs.Service {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return docs.NewService(units.NewManager(units.NewSQLStore(db)))
}

type recordingAuthorizer struct {
	mu    sync.Mutex
	paths []string
	deny  bool
}

func (r *recordingAuthorizer) authorize(_ context.Context, path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
	if r.deny {
		return errors.New("denied")
	}
	return nil
}

func (r *recordingAuthorizer) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

func call(t *testing.T, tool toolloop.BuiltinTool, ctx context.Context, args any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Call(ctx, raw)
	if err != nil {
		t.Fatalf("%s Call: %v", tool.Name(), err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("%s result not JSON: %s", tool.Name(), out)
	}
	return m
}

const secretTitle = "Q3 layoffs plan"

func TestSaveUpdateBuild_EndToEnd(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	ws := t.TempDir()
	auth := &recordingAuthorizer{}
	save := documents.NewSave(documents.Options{Store: svc})
	update := documents.NewUpdate(documents.Options{Store: svc})
	build := documents.NewBuildSite(documents.Options{
		Store: svc, WorkspaceDir: func() string { return ws }, AuthorizeWrite: auth.authorize,
	})
	ctx := toolloop.WithSessionID(context.Background(), "sess-1")

	saved := call(t, save, ctx, map[string]any{
		"title": "Incident comms",
		"html":  `<h1>Incident comms</h1><p>Page the on-call.</p><script>alert(1)</script>`,
	})
	id, _ := saved["document_id"].(string)
	if id == "" || saved["sanitized"] != true || saved["version"].(float64) != 0 {
		t.Fatalf("save result = %v", saved)
	}

	updated := call(t, update, ctx, map[string]any{
		"document_id": id, "html": "<h1>Incident comms</h1><p>Page the on-call, then status page.</p>", "base_version": 0,
	})
	if updated["version"].(float64) != 1 || updated["sanitized"] != false {
		t.Fatalf("update result = %v", updated)
	}
	stale := call(t, update, ctx, map[string]any{"document_id": id, "html": "<p>x</p>", "base_version": 0})
	if stale["error"] != docs.CodeVersionConflict {
		t.Errorf("stale update = %v", stale)
	}
	missingBase := call(t, update, ctx, map[string]any{"document_id": id, "html": "<p>x</p>"})
	if missingBase["error"] != "invalid_args" {
		t.Errorf("missing base_version = %v", missingBase)
	}

	call(t, save, ctx, map[string]any{"title": "Runbook", "html": "<p>Deploy steps</p>"})

	site := call(t, build, ctx, map[string]any{"slug": "team-kb", "title": "Team knowledge base"})
	if site["error"] != nil {
		t.Fatalf("build error = %v", site)
	}
	if site["published"] != false || site["documents"].(float64) != 2 || !strings.Contains(site["note"].(string), "nothing was uploaded") {
		t.Errorf("build result = %v", site)
	}
	wantDir := filepath.Join(ws, docs.ExportsDirName, "team-kb")
	if site["site_dir"] != wantDir {
		t.Errorf("site_dir = %v", site["site_dir"])
	}
	page, err := os.ReadFile(filepath.Join(wantDir, "public", "d", id+".html"))
	if err != nil {
		t.Fatalf("document page missing: %v", err)
	}
	if !strings.Contains(string(page), "then status page") || strings.Contains(string(page), "<script") {
		t.Errorf("page does not carry the sanitized latest version")
	}
	if got := auth.snapshot(); len(got) != 2 || got[0] != wantDir || got[1] != wantDir+".tar.gz" {
		t.Errorf("authorized paths = %v", got)
	}

	// Rebuilding the same documents reproduces the same bundle.
	again := call(t, build, ctx, map[string]any{"slug": "team-kb", "title": "Team knowledge base"})
	if again["bundle_sha256"] != site["bundle_sha256"] {
		t.Errorf("rebuild changed bundle: %v vs %v", again["bundle_sha256"], site["bundle_sha256"])
	}
}

func TestBuildSite_WritePolicyDenialWritesNothing(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	ws := t.TempDir()
	ctx := toolloop.WithSessionID(context.Background(), "s")
	call(t, documents.NewSave(documents.Options{Store: svc}), ctx, map[string]any{"title": "A", "html": "<p>a</p>"})

	build := documents.NewBuildSite(documents.Options{
		Store: svc, WorkspaceDir: func() string { return ws },
		AuthorizeWrite: (&recordingAuthorizer{deny: true}).authorize,
	})
	res := call(t, build, ctx, map[string]any{"slug": "kb"})
	if res["error"] != "write_denied" {
		t.Fatalf("result = %v", res)
	}
	if entries, _ := os.ReadDir(ws); len(entries) != 0 {
		t.Errorf("denied build wrote %v", entries)
	}
}

func TestTools_SessionIsolationDisabledAndNoContentInErrors(t *testing.T) {
	t.Parallel()
	svc := newService(t)
	ws := t.TempDir()
	auth := &recordingAuthorizer{}
	owner := toolloop.WithSessionID(context.Background(), "owner")
	other := toolloop.WithSessionID(context.Background(), "other")

	saved := call(t, documents.NewSave(documents.Options{Store: svc}), owner,
		map[string]any{"title": secretTitle, "html": "<p>confidential</p>"})
	id := saved["document_id"].(string)

	update := documents.NewUpdate(documents.Options{Store: svc})
	res := call(t, update, other, map[string]any{"document_id": id, "html": "<p>x</p>", "base_version": 0})
	if res["error"] != docs.CodeNotFound {
		t.Errorf("cross-session update = %v", res)
	}

	build := documents.NewBuildSite(documents.Options{Store: svc, WorkspaceDir: func() string { return ws }, AuthorizeWrite: auth.authorize})
	res = call(t, build, other, map[string]any{"slug": "kb", "document_ids": []string{id}})
	if res["error"] != docs.CodeNotFound {
		t.Errorf("cross-session site = %v", res)
	}
	res = call(t, build, other, map[string]any{"slug": "kb"})
	if res["error"] != docs.CodeNoSiteDocuments {
		t.Errorf("empty session site = %v", res)
	}
	if len(auth.snapshot()) != 0 {
		t.Error("authorizer consulted for a build that never reached the write step")
	}

	res = call(t, documents.NewSave(documents.Options{Store: svc}), context.Background(),
		map[string]any{"title": "t", "html": "<p>x</p>"})
	if res["error"] != docs.CodeNoSession {
		t.Errorf("no-session save = %v", res)
	}

	off := func() bool { return false }
	for _, tool := range []toolloop.BuiltinTool{
		documents.NewSave(documents.Options{Store: svc, Enabled: off}),
		documents.NewUpdate(documents.Options{Store: svc, Enabled: off}),
		documents.NewBuildSite(documents.Options{Store: svc, Enabled: off, WorkspaceDir: func() string { return ws }, AuthorizeWrite: auth.authorize}),
	} {
		if r := call(t, tool, owner, map[string]any{}); r["error"] != "disabled" {
			t.Errorf("%s disabled result = %v", tool.Name(), r)
		}
	}

	res = call(t, documents.NewSave(documents.Options{Store: svc}), owner, map[string]any{"title": secretTitle, "html": "<script>only()</script>"})
	if raw, _ := json.Marshal(res); strings.Contains(string(raw), secretTitle) || res["error"] != docs.CodeEmptyBody {
		t.Errorf("error result leaks title or wrong code: %s", raw)
	}
}
