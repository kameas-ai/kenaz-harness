package attachments_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/attachments"
)

func newManager(t *testing.T) *attachments.Manager {
	t.Helper()
	return attachments.NewManager(attachments.NewMemoryStore())
}

func TestManager_AddAndList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newManager(t)

	att := attachments.Attachment{
		ScopeKind:     attachments.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "inline:abc",
		Content:       "hello",
	}
	stored, err := m.Add(ctx, att)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if stored.ID == "" {
		t.Errorf("expected generated id, got empty")
	}
	if stored.Kind != attachments.KindSystem {
		t.Errorf("Kind defaulted to %q, want %q", stored.Kind, attachments.KindSystem)
	}
	if stored.CreatedAt.IsZero() {
		t.Errorf("CreatedAt not stamped")
	}

	got, err := m.List(ctx, attachments.ScopeFilter{ScopeKind: attachments.ScopeKindSession, ScopeID: "s1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Content != "hello" {
		t.Errorf("List = %+v", got)
	}
}

func TestManager_AddRejectsInvalidScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newManager(t)

	cases := []struct {
		name string
		att  attachments.Attachment
		want error
	}{
		{
			name: "unknown scope kind",
			att: attachments.Attachment{
				ScopeKind:     "garbage",
				ScopeID:       "x",
				ContentSource: "inline:a",
			},
			want: attachments.ErrInvalidScope,
		},
		{
			name: "session without scope id",
			att: attachments.Attachment{
				ScopeKind:     attachments.ScopeKindSession,
				ContentSource: "inline:a",
			},
			want: attachments.ErrInvalidScope,
		},
		{
			name: "global with scope id",
			att: attachments.Attachment{
				ScopeKind:     attachments.ScopeKindGlobal,
				ScopeID:       "stray",
				ContentSource: "inline:a",
			},
			want: attachments.ErrInvalidScope,
		},
		{
			name: "missing content source",
			att: attachments.Attachment{
				ScopeKind:     attachments.ScopeKindSession,
				ScopeID:       "s1",
				ContentSource: "",
			},
			want: attachments.ErrInvalidContentSource,
		},
		{
			name: "garbage content source scheme",
			att: attachments.Attachment{
				ScopeKind:     attachments.ScopeKindSession,
				ScopeID:       "s1",
				ContentSource: "ftp://foo",
			},
			want: attachments.ErrInvalidContentSource,
		},
		{
			name: "invalid kind",
			att: attachments.Attachment{
				ScopeKind:     attachments.ScopeKindSession,
				ScopeID:       "s1",
				ContentSource: "inline:a",
				Kind:          "robot",
			},
			want: attachments.ErrInvalidKind,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := m.Add(ctx, tc.att)
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestManager_RemoveAndNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newManager(t)
	stored, err := m.Add(ctx, attachments.Attachment{
		ScopeKind:     attachments.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "inline:abc",
		Content:       "x",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := m.Remove(ctx, stored.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := m.Remove(ctx, stored.ID); !errors.Is(err, attachments.ErrNotFound) {
		t.Errorf("Remove second time: %v, want ErrNotFound", err)
	}
}

func TestManager_Reorder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newManager(t)

	ids := make([]string, 0, 3)
	now := time.Now().UTC()
	for i, content := range []string{"first", "second", "third"} {
		stored, err := m.Add(ctx, attachments.Attachment{
			ScopeKind:     attachments.ScopeKindSession,
			ScopeID:       "s1",
			ContentSource: "inline:" + content,
			Content:       content,
			Position:      i,
			CreatedAt:     now.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
		ids = append(ids, stored.ID)
	}

	// Reverse the order.
	rev := []string{ids[2], ids[1], ids[0]}
	if err := m.Reorder(ctx, attachments.ScopeKindSession, "s1", rev); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	got, err := m.List(ctx, attachments.ScopeFilter{ScopeKind: attachments.ScopeKindSession, ScopeID: "s1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	wantContent := []string{"third", "second", "first"}
	for i, w := range wantContent {
		if got[i].Content != w {
			t.Errorf("after Reorder[%d].Content = %q, want %q", i, got[i].Content, w)
		}
		if got[i].Position != i {
			t.Errorf("after Reorder[%d].Position = %d, want %d", i, got[i].Position, i)
		}
	}

	// Partial permutation rejected.
	if err := m.Reorder(ctx, attachments.ScopeKindSession, "s1", []string{ids[0]}); err == nil {
		t.Errorf("expected error for partial permutation")
	}
}

func TestManager_ListResolved_GlobalProjectSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newManager(t)

	add := func(scopeKind, scopeID, content string, position int) {
		t.Helper()
		_, err := m.Add(ctx, attachments.Attachment{
			ScopeKind:     scopeKind,
			ScopeID:       scopeID,
			ContentSource: "inline:" + content,
			Content:       content,
			Position:      position,
		})
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	add(attachments.ScopeKindGlobal, "", "g0", 0)
	add(attachments.ScopeKindGlobal, "", "g1", 1)
	add(attachments.ScopeKindProject, "p1", "p0", 0)
	add(attachments.ScopeKindProject, "p2", "other", 0)
	add(attachments.ScopeKindSession, "s1", "s0", 0)
	add(attachments.ScopeKindSession, "s2", "other-session", 0)

	reader := attachments.NewStaticReader(map[string]string{
		"s1": "p1",
	})

	got, err := m.ListResolved(ctx, reader, "s1")
	if err != nil {
		t.Fatalf("ListResolved: %v", err)
	}
	want := []string{"g0", "g1", "p0", "s0"}
	if len(got) != len(want) {
		t.Fatalf("len(resolved) = %d, want %d (got %+v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Content != w {
			t.Errorf("resolved[%d].Content = %q, want %q", i, got[i].Content, w)
		}
	}

	// Loose session: project layer is empty.
	loose, err := m.ListResolved(ctx, reader, "s2")
	if err != nil {
		t.Fatalf("ListResolved loose: %v", err)
	}
	wantLoose := []string{"g0", "g1", "other-session"}
	if len(loose) != len(wantLoose) {
		t.Fatalf("loose: %+v", loose)
	}
	for i, w := range wantLoose {
		if loose[i].Content != w {
			t.Errorf("loose[%d].Content = %q, want %q", i, loose[i].Content, w)
		}
	}

	// Nil reader skips the project layer entirely.
	noReader, err := m.ListResolved(ctx, nil, "s1")
	if err != nil {
		t.Fatalf("ListResolved nil reader: %v", err)
	}
	wantNoReader := []string{"g0", "g1", "s0"}
	if len(noReader) != len(wantNoReader) {
		t.Fatalf("nil-reader resolved = %+v", noReader)
	}
}

type fakeLibrary struct {
	store map[string]string
	err   error
}

func (f *fakeLibrary) Get(_ context.Context, path string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.store[path]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func TestManager_RefreshLibrarySource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	lib := &fakeLibrary{store: map[string]string{"foo/bar.md": "fresh content"}}
	m := attachments.NewManager(attachments.NewMemoryStore(),
		attachments.WithLibrary(lib))

	stored, err := m.Add(ctx, attachments.Attachment{
		ScopeKind:     attachments.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "library:foo/bar.md",
		Content:       "stale snapshot",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	out, err := m.Refresh(ctx, stored.ID)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if out.Content != "fresh content" {
		t.Errorf("Content = %q, want %q", out.Content, "fresh content")
	}
}

func TestManager_RefreshInlineIsNoop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newManager(t)
	stored, err := m.Add(ctx, attachments.Attachment{
		ScopeKind:     attachments.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "inline:abcd",
		Content:       "snapshot",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	out, err := m.Refresh(ctx, stored.ID)
	if err != nil {
		t.Fatalf("Refresh inline: %v", err)
	}
	if out.Content != "snapshot" {
		t.Errorf("Refresh inline mutated content: %q", out.Content)
	}
}

func TestManager_RefreshLibraryWithoutReader(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newManager(t)
	stored, err := m.Add(ctx, attachments.Attachment{
		ScopeKind:     attachments.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "library:foo.md",
		Content:       "stale",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	_, err = m.Refresh(ctx, stored.ID)
	if !errors.Is(err, attachments.ErrSourceUnavailable) {
		t.Errorf("Refresh: %v, want ErrSourceUnavailable", err)
	}
}

func TestManager_AddPreservesInjectedID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newManager(t)
	stored, err := m.Add(ctx, attachments.Attachment{
		ID:            "abc123",
		ScopeKind:     attachments.ScopeKindGlobal,
		ContentSource: "inline:something",
		Content:       "x",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if stored.ID != "abc123" {
		t.Errorf("ID = %q, want abc123", stored.ID)
	}
}

func TestManager_NilStorePanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil store")
		}
	}()
	_ = attachments.NewManager(nil)
}

// TestManager_DefaultIDGenIsHex32 doesn't directly call defaultIDGen
// (unexported); instead it confirms the auto-assigned id matches the
// 32-char hex shape used by session/projects ids.
func TestManager_DefaultIDGenIsHex32(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newManager(t)
	stored, err := m.Add(ctx, attachments.Attachment{
		ScopeKind:     attachments.ScopeKindGlobal,
		ContentSource: "inline:a",
		Content:       "x",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(stored.ID) != 32 {
		t.Errorf("id len = %d, want 32 (id=%q)", len(stored.ID), stored.ID)
	}
	for _, c := range stored.ID {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("id contains non-hex char %q", c)
			break
		}
	}
}
