package contexts_test

// pulled_entry_get_test.go — a pulled team/org entry listed under
// "<layer>/_fleet/<id>" opens (its body is served from the pulled cache)
// and is read-only (save / rename / delete refused).

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	corecontexts "github.com/kameas-ai/kenaz-harness/core/contexts"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	contextsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contexts"
)

const pulledID = "6b2f6f0e-0000-4000-8000-0000000000e1"

func TestGet_PulledEntryServesBodyReadOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/context/pull" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"nodes":[{"id":"`+pulledID+`","org_id":"o","owner_user_id":"u2","kind":"guidance","title":"Deploy","body":"Use the blue lane.","classification":"team_shared","version":2,"created_at":"2026-11-01T00:00:00Z","updated_at":"2026-11-01T00:00:00Z"}],"edges":[],"cursor":"c1"}`)
	}))
	defer srv.Close()
	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC(),
	})
	corefleet.SetExternalTokenSource(func() string { return "tok" })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })

	client := corefleet.NewClientForTesting(srv.URL)
	poller := corefleet.NewCapabilityPoller(client, t.TempDir())
	poller.ForceSetCurrentForTesting(corefleet.Capabilities{
		Tier: "enterprise", Enabled: map[corefleet.Capability]bool{corefleet.CapSharedTeamGraph: true}, FetchedAt: time.Now(),
	})
	syncer := corefleet.NewContextGraphSyncer(client, t.TempDir(), poller)
	ctx := context.Background()
	if n, err := syncer.PullDelta(ctx); err != nil || n != 1 {
		t.Fatalf("PullDelta: n=%d err=%v", n, err)
	}
	lib, err := corecontexts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	api := contextsview.New(lib).WithSyncer(syncer)

	path := "team/_fleet/" + pulledID
	body, err := api.Get(ctx, path)
	if err != nil || body != "Use the blue lane." {
		t.Fatalf("Get(%s) = %q, %v; want the pulled body", path, body, err)
	}
	if _, err := api.Get(ctx, "org/_fleet/"+pulledID); !errors.Is(err, contextsview.ErrPulledEntryNotFound) {
		t.Errorf("wrong layer: err = %v, want ErrPulledEntryNotFound", err)
	}
	if _, err := api.Get(ctx, "team/_fleet/nope"); !errors.Is(err, contextsview.ErrPulledEntryNotFound) {
		t.Errorf("unknown id: err = %v", err)
	}
	if err := api.Save(ctx, path, "edited"); !errors.Is(err, contextsview.ErrPulledEntryReadOnly) {
		t.Errorf("Save: err = %v, want read-only", err)
	}
	if err := api.Rename(ctx, path, "team/mine.md"); !errors.Is(err, contextsview.ErrPulledEntryReadOnly) {
		t.Errorf("Rename: err = %v, want read-only", err)
	}
	if err := api.Delete(ctx, path); !errors.Is(err, contextsview.ErrPulledEntryReadOnly) {
		t.Errorf("Delete: err = %v, want read-only", err)
	}
	// An ordinary local file still reads from disk.
	if err := api.Save(ctx, "team/notes.md", "local"); err != nil {
		t.Fatalf("Save local: %v", err)
	}
	if got, err := api.Get(ctx, "team/notes.md"); err != nil || got != "local" {
		t.Errorf("local Get = %q, %v", got, err)
	}
}
