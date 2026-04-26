package sessions

import (
	"context"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// TestManagerAPI_RoundTrip exercises the rpc-side impl against a real
// Manager backed by an in-memory store. The intent is to pin the
// adapter's view-shape projection (Record -> Session) and to make
// sure the standard CRUD verbs surface through cleanly.
func TestManagerAPI_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mgr := session.NewManager(session.NewMemoryStore())
	api := NewManagerAPI(mgr)

	a, err := api.Create(ctx, "alpha")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.ID == "" || a.Name != "alpha" {
		t.Errorf("Create returned %+v, want non-empty ID + name=alpha", a)
	}

	b, _ := api.Create(ctx, "beta")
	c, _ := api.Create(ctx, "gamma")

	got, err := api.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("List len = %d, want 3", len(got))
	}

	if err := api.Rename(ctx, a.ID, "alpha-prime"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	got1, _ := api.Get(ctx, a.ID)
	if got1.Name != "alpha-prime" {
		t.Errorf("Get.Name = %q, want alpha-prime", got1.Name)
	}

	if err := api.Reorder(ctx, []string{c.ID, a.ID, b.ID}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	got2, _ := api.List(ctx)
	wantOrder := []string{c.ID, a.ID, b.ID}
	for i, want := range wantOrder {
		if got2[i].ID != want {
			t.Errorf("List[%d].ID = %s, want %s", i, got2[i].ID, want)
		}
	}

	if err := api.Delete(ctx, b.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := api.Get(ctx, b.ID); err == nil {
		t.Errorf("Get after Delete returned no error")
	}

	// StartStream + StopStream are no-ops today; just assert they don't error.
	if _, err := api.StartStream(ctx, a.ID); err != nil {
		t.Errorf("StartStream: %v", err)
	}
	if err := api.StopStream(ctx, "sub-1"); err != nil {
		t.Errorf("StopStream: %v", err)
	}
}

// TestManagerAPI_MoveToProject pins the projectId round-trip in the
// view shape. An empty projectID detaches (loose).
func TestManagerAPI_MoveToProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := session.NewManager(session.NewMemoryStore())
	api := NewManagerAPI(mgr)

	s, err := api.Create(ctx, "alpha")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if s.ProjectID != "" {
		t.Errorf("fresh session ProjectID = %q, want empty", s.ProjectID)
	}
	if err := api.MoveToProject(ctx, s.ID, "p-1"); err != nil {
		t.Fatalf("MoveToProject: %v", err)
	}
	got, _ := api.Get(ctx, s.ID)
	if got.ProjectID != "p-1" {
		t.Errorf("ProjectID after attach = %q, want p-1", got.ProjectID)
	}
	if err := api.MoveToProject(ctx, s.ID, ""); err != nil {
		t.Fatalf("MoveToProject(detach): %v", err)
	}
	got, _ = api.Get(ctx, s.ID)
	if got.ProjectID != "" {
		t.Errorf("ProjectID after detach = %q, want empty", got.ProjectID)
	}
}

// TestNewManagerAPI_NilManager pins the construction precondition.
func TestNewManagerAPI_NilManager(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("NewManagerAPI(nil) did not panic")
		}
	}()
	_ = NewManagerAPI(nil)
}
