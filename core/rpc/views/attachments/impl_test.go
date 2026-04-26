package attachments_test

import (
	"context"
	"errors"
	"testing"

	coreatt "github.com/sigil-tech/kaneaz-harness/core/attachments"
	attview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/attachments"
)

func newAPI(t *testing.T, reader attview.SessionProjectReader) *attview.API {
	t.Helper()
	mgr := coreatt.NewManager(coreatt.NewMemoryStore())
	return attview.New(mgr, reader)
}

func mustAdd(t *testing.T, api *attview.API, in attview.AddInput) attview.Attachment {
	t.Helper()
	a, err := api.Add(context.Background(), in)
	if err != nil {
		t.Fatalf("Add(%+v): %v", in, err)
	}
	return a
}

func TestAPI_AddListRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newAPI(t, nil)

	a := mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "inline:abc",
		Content:       "snapshot",
	})
	if a.ID == "" {
		t.Errorf("Add returned empty id")
	}
	if a.CreatedAt == "" {
		t.Errorf("Add returned empty CreatedAt")
	}

	got, err := api.List(ctx, coreatt.ScopeKindSession, "s1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Content != "snapshot" {
		t.Errorf("List = %+v", got)
	}

	if err := api.Remove(ctx, a.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got, err = api.List(ctx, coreatt.ScopeKindSession, "s1")
	if err != nil {
		t.Fatalf("List after Remove: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List after Remove = %+v", got)
	}
}

func TestAPI_AddRejectsInvalidScope(t *testing.T) {
	t.Parallel()
	api := newAPI(t, nil)
	_, err := api.Add(context.Background(), attview.AddInput{
		ScopeKind:     "garbage",
		ContentSource: "inline:x",
	})
	if !errors.Is(err, coreatt.ErrInvalidScope) {
		t.Errorf("got %v, want ErrInvalidScope", err)
	}
}

type stubReader struct {
	m map[string]string
}

func (s *stubReader) ProjectID(_ context.Context, sessionID string) (*string, error) {
	v, ok := s.m[sessionID]
	if !ok || v == "" {
		return nil, nil
	}
	return &v, nil
}

func TestAPI_ListResolved_GlobalProjectSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newAPI(t, &stubReader{m: map[string]string{"s1": "p1"}})

	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindGlobal,
		ContentSource: "inline:g",
		Content:       "global",
	})
	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindProject,
		ScopeID:       "p1",
		ContentSource: "inline:p",
		Content:       "project",
	})
	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "inline:s",
		Content:       "session",
	})
	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindProject,
		ScopeID:       "other",
		ContentSource: "inline:other",
		Content:       "other-project",
	})

	got, err := api.ListResolved(ctx, "s1")
	if err != nil {
		t.Fatalf("ListResolved: %v", err)
	}
	want := []string{"global", "project", "session"}
	if len(got) != len(want) {
		t.Fatalf("ListResolved = %+v", got)
	}
	for i, w := range want {
		if got[i].Content != w {
			t.Errorf("got[%d].Content = %q, want %q", i, got[i].Content, w)
		}
	}
}

func TestAPI_ListResolved_NilReaderSkipsProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newAPI(t, nil)

	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindGlobal,
		ContentSource: "inline:g",
		Content:       "global",
	})
	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindProject,
		ScopeID:       "p1",
		ContentSource: "inline:p",
		Content:       "project",
	})
	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "inline:s",
		Content:       "session",
	})

	got, err := api.ListResolved(ctx, "s1")
	if err != nil {
		t.Fatalf("ListResolved: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListResolved (no reader) = %+v", got)
	}
	if got[0].Content != "global" || got[1].Content != "session" {
		t.Errorf("order: %+v", got)
	}
}

func TestAPI_Reorder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newAPI(t, nil)

	a := mustAdd(t, api, attview.AddInput{
		ScopeKind: coreatt.ScopeKindGlobal, ContentSource: "inline:a", Content: "a", Position: 0,
	})
	b := mustAdd(t, api, attview.AddInput{
		ScopeKind: coreatt.ScopeKindGlobal, ContentSource: "inline:b", Content: "b", Position: 1,
	})
	if err := api.Reorder(ctx, coreatt.ScopeKindGlobal, "", []string{b.ID, a.ID}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	got, _ := api.List(ctx, coreatt.ScopeKindGlobal, "")
	if got[0].Content != "b" || got[1].Content != "a" {
		t.Errorf("after Reorder = %+v", got)
	}
}

type fakeLib struct {
	store map[string]string
}

func (f *fakeLib) Get(_ context.Context, p string) (string, error) {
	v, ok := f.store[p]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func TestAPI_Refresh_LibrarySource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	lib := &fakeLib{store: map[string]string{"foo.md": "fresh"}}
	mgr := coreatt.NewManager(coreatt.NewMemoryStore(), coreatt.WithLibrary(lib))
	api := attview.New(mgr, nil)
	stored, err := api.Add(ctx, attview.AddInput{
		ScopeKind: coreatt.ScopeKindSession, ScopeID: "s1",
		ContentSource: "library:foo.md", Content: "stale",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	out, err := api.Refresh(ctx, stored.ID)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if out.Content != "fresh" {
		t.Errorf("Refresh content = %q, want fresh", out.Content)
	}
}

func TestAPI_NilManagerPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic")
		}
	}()
	_ = attview.New(nil, nil)
}
