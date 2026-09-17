package documents_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/docs"
	documentsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/documents"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

func newView(t *testing.T, sessions map[string]bool, workspace string) documentsview.DocumentsAPI {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return documentsview.New(documentsview.Options{
		Store: docs.NewService(units.NewManager(units.NewSQLStore(db))),
		SessionExists: func(_ context.Context, id string) error {
			if sessions[id] {
				return nil
			}
			return errors.New("no such session")
		},
		WorkspaceDir: func() string { return workspace },
	})
}

func TestDocumentsView_ErrorsFollowTheContractForm(t *testing.T) {
	t.Parallel()
	api := newView(t, map[string]bool{"s": true}, "")
	ctx := context.Background()

	d, err := api.Create(ctx, "s", "Doc", "<p>v0</p>")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"negative base is malformed, never a skipped check", func() error { _, e := api.Update(ctx, "s", d.ID, -1, "<p>x</p>"); return e }(), documentsview.CodeBadParams},
		{"stale base", func() error {
			if _, e := api.Update(ctx, "s", d.ID, 0, "<p>v1</p>"); e != nil {
				return e
			}
			_, e := api.Update(ctx, "s", d.ID, 0, "<p>stale</p>")
			return e
		}(), docs.CodeVersionConflict},
		{"unknown session", func() error { _, e := api.List(ctx, "ghost"); return e }(), docs.CodeNoSession},
		{"blank session", func() error { _, e := api.Create(ctx, "  ", "t", "<p>x</p>"); return e }(), docs.CodeNoSession},
		{"no workspace", func() error { _, e := api.BuildSite(ctx, "s", "kb", "", nil); return e }(), documentsview.CodeUnavailable},
		{"exports dir without workspace", func() error { _, e := api.ExportsDir(ctx); return e }(), documentsview.CodeUnavailable},
		{"empty body", func() error { _, e := api.Create(ctx, "s", "t", "<script>x()</script>"); return e }(), docs.CodeEmptyBody},
	}
	for _, tc := range cases {
		if got := documentsview.ErrorCode(tc.err); got != tc.code {
			t.Errorf("%s: code = %q (%v), want %q", tc.name, got, tc.err, tc.code)
			continue
		}
		if !strings.HasPrefix(tc.err.Error(), "documents: "+tc.code+": ") {
			t.Errorf("%s: error string %q is not in contract form", tc.name, tc.err)
		}
	}
}

func TestDocumentsView_PreviewIsExactlyWhatIsStored(t *testing.T) {
	t.Parallel()
	api := newView(t, map[string]bool{"s": true}, "")
	ctx := context.Background()

	empty, err := api.Preview(ctx, "   ")
	if err != nil || empty.HTML != "" || empty.Sanitized {
		t.Fatalf("empty preview = %+v, %v", empty, err)
	}
	body := `<h2>Title</h2><p style="color:red" onmouseover="x()">hi</p><img src="https://t.example/p.gif">`
	p, err := api.Preview(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	d, err := api.Create(ctx, "s", "T", body)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Sanitized || p.HTML != d.Body || p.ByteSize != d.ByteSize {
		t.Fatalf("preview %+v differs from stored %+v", p, d)
	}
}

func TestDocumentsView_Unavailable(t *testing.T) {
	t.Parallel()
	api := documentsview.Unavailable()
	if _, err := api.List(context.Background(), "s"); documentsview.ErrorCode(err) != documentsview.CodeUnavailable {
		t.Fatalf("err = %v", err)
	}
}
