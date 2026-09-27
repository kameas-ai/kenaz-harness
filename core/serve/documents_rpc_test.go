package serve_test

// documents_rpc_test.go drives the Documents_* family through the REAL served
// transport: real HTTP, real serve.dispatch, real rpc.API over core.New (real
// sqlite), and a real workspace directory. contracts/documents-rpc.md is the
// contract under test.

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	documentsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/documents"
	"github.com/kameas-ai/kenaz-harness/core/serve"
)

func newDocumentsHarness(t *testing.T) (baseURL, workspace string) {
	t.Helper()
	workspace = t.TempDir()
	c, err := core.New(core.Options{DataDir: t.TempDir(), WorkspaceDir: workspace})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := rpc.New(c)
	t.Cleanup(api.Shutdown)
	// The workspace override is probed at core.New; resolve what it actually
	// chose (macOS temp dirs are symlinked, so compare the resolved path).
	workspace = c.WorkspaceDir()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	srv := serve.New(api, addr, "tok", nil, nil)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	waitForListener(t, addr)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("server did not shut down in time")
		}
	})
	return "http://" + addr, workspace
}

func createSession(t *testing.T, baseURL, name string) string {
	t.Helper()
	var s struct {
		ID string `json:"id"`
	}
	rpcCall(t, baseURL, "Sessions_Create", map[string]any{"name": name}, &s)
	if s.ID == "" {
		t.Fatal("Sessions_Create returned no id")
	}
	return s.ID
}

func wantCode(t *testing.T, errString, code string) {
	t.Helper()
	if !strings.HasPrefix(errString, "documents: "+code+": ") {
		t.Fatalf("error = %q, want contract form %q", errString, "documents: "+code+": …")
	}
}

func TestServedDocuments_AuthorEditBuildEndToEnd(t *testing.T) {
	baseURL, workspace := newDocumentsHarness(t)
	sess := createSession(t, baseURL, "docs")

	var empty []documentsview.DocumentSummary
	rpcCall(t, baseURL, "Documents_List", map[string]any{"sessionId": sess}, &empty)
	if len(empty) != 0 {
		t.Fatalf("fresh session lists %d documents", len(empty))
	}

	var preview documentsview.PreviewResult
	rpcCall(t, baseURL, "Documents_Preview", map[string]any{
		"body": `<h1>Runbook</h1><p onclick="x()">Step</p><script>alert(1)</script>`,
	}, &preview)
	if !preview.Sanitized || strings.Contains(preview.HTML, "script") || strings.Contains(preview.HTML, "onclick") {
		t.Fatalf("preview = %+v", preview)
	}

	var doc documentsview.Document
	rpcCall(t, baseURL, "Documents_Create", map[string]any{
		"sessionId": sess, "title": "On-call runbook",
		"body": `<h1>Runbook</h1><p onclick="x()">Step</p><script>alert(1)</script>`,
	}, &doc)
	if doc.Version != 0 || doc.Body != preview.HTML {
		t.Fatalf("created %+v; preview must equal what is stored", doc)
	}

	var updated documentsview.Document
	rpcCall(t, baseURL, "Documents_Update", map[string]any{
		"sessionId": sess, "id": doc.ID, "baseVersion": 0, "body": "<h1>Runbook</h1><p>Step two</p>",
	}, &updated)
	if updated.Version != 1 || updated.ContentSHA256 == doc.ContentSHA256 {
		t.Fatalf("updated %+v", updated)
	}

	// A second editor still holding version 0 gets version_conflict, and the
	// stored body is untouched.
	wantCode(t, rpcCallErr(t, baseURL, "Documents_Update", map[string]any{
		"sessionId": sess, "id": doc.ID, "baseVersion": 0, "body": "<p>stale</p>",
	}, nil), "version_conflict")
	var got documentsview.Document
	rpcCall(t, baseURL, "Documents_Get", map[string]any{"sessionId": sess, "id": doc.ID}, &got)
	if got.Version != 1 || !strings.Contains(got.Body, "Step two") {
		t.Fatalf("after conflict: %+v", got)
	}

	// Omitting baseVersion is malformed, not "base 0".
	wantCode(t, rpcCallErr(t, baseURL, "Documents_Update", map[string]any{
		"sessionId": sess, "id": doc.ID, "body": "<p>x</p>",
	}, nil), "bad_params")

	var second documentsview.Document
	rpcCall(t, baseURL, "Documents_Create", map[string]any{
		"sessionId": sess, "title": "Launch brief", "body": "<p>GA Oct 1</p>",
	}, &second)

	var dir documentsview.ExportsDirResult
	rpcCall(t, baseURL, "Documents_ExportsDir", map[string]any{}, &dir)
	if dir.Dir != filepath.Join(workspace, "documents-exports") {
		t.Fatalf("exports dir = %q", dir.Dir)
	}

	var built documentsview.SiteBuildResult
	rpcCall(t, baseURL, "Documents_BuildSite", map[string]any{
		"sessionId": sess, "slug": "team-kb", "title": "Team KB", "documentIds": []string{doc.ID},
	}, &built)
	if built.Published || built.Documents != 1 || built.SiteDir != filepath.Join(dir.Dir, "team-kb") ||
		built.PublicDir != filepath.Join(built.SiteDir, "public") || built.Bundle != built.SiteDir+".tar.gz" || built.BundleSHA256 == "" {
		t.Fatalf("build result = %+v", built)
	}
	page, err := os.ReadFile(filepath.Join(built.PublicDir, "d", doc.ID+".html"))
	if err != nil {
		t.Fatalf("page missing: %v", err)
	}
	if !strings.Contains(string(page), "Step two") {
		t.Error("site does not carry the latest version")
	}
	if _, err := os.Stat(filepath.Join(built.PublicDir, "d", second.ID+".html")); !os.IsNotExist(err) {
		t.Error("unselected document was built into the site")
	}
	if _, err := os.Stat(built.Bundle); err != nil {
		t.Errorf("bundle missing: %v", err)
	}
}

func TestServedDocuments_SessionVisibilityCannotBeBypassed(t *testing.T) {
	baseURL, workspace := newDocumentsHarness(t)
	owner := createSession(t, baseURL, "owner")
	other := createSession(t, baseURL, "other")

	var doc documentsview.Document
	rpcCall(t, baseURL, "Documents_Create", map[string]any{
		"sessionId": owner, "title": "Private plan", "body": "<p>confidential</p>",
	}, &doc)

	var list []documentsview.DocumentSummary
	rpcCall(t, baseURL, "Documents_List", map[string]any{"sessionId": other}, &list)
	if len(list) != 0 {
		t.Fatalf("other session lists %+v", list)
	}
	for _, call := range []struct {
		method string
		params map[string]any
	}{
		{"Documents_Get", map[string]any{"sessionId": other, "id": doc.ID}},
		{"Documents_Update", map[string]any{"sessionId": other, "id": doc.ID, "baseVersion": 0, "body": "<p>overwrite</p>"}},
		{"Documents_BuildSite", map[string]any{"sessionId": other, "slug": "leak", "documentIds": []string{doc.ID}}},
	} {
		e := rpcCallErr(t, baseURL, call.method, call.params, nil)
		wantCode(t, e, "document_not_found")
		if strings.Contains(e, "Private plan") || strings.Contains(e, "confidential") {
			t.Errorf("%s error leaks content: %q", call.method, e)
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, "documents-exports", "leak")); !os.IsNotExist(err) {
		t.Error("a refused build wrote output")
	}

	// A session id nobody created is rejected before the store is touched,
	// so a caller cannot mint ids to create orphan documents.
	for _, method := range []string{"Documents_List", "Documents_Create", "Documents_Get", "Documents_BuildSite"} {
		wantCode(t, rpcCallErr(t, baseURL, method, map[string]any{
			"sessionId": "not-a-session", "id": doc.ID, "title": "t", "body": "<p>x</p>", "slug": "kb",
		}, nil), "no_session")
	}
	wantCode(t, rpcCallErr(t, baseURL, "Documents_List", map[string]any{}, nil), "no_session")

	// Serve_ListMethods advertises the whole family.
	var methods struct {
		RPC []string `json:"rpc"`
	}
	rpcCall(t, baseURL, "Serve_ListMethods", nil, &methods)
	joined := strings.Join(methods.RPC, ",")
	for _, m := range []string{"Documents_BuildSite", "Documents_Create", "Documents_ExportsDir", "Documents_Get", "Documents_List", "Documents_Preview", "Documents_Update"} {
		if !strings.Contains(joined, m) {
			t.Errorf("Serve_ListMethods missing %s", m)
		}
	}
}
