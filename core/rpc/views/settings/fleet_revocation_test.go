package settings

// skill-library-01SKLIB01 WP03: the catalog revocation sweep is wired to the
// REAL config poller's after-poll hook (no timer of its own), uses the
// consumers wired into the fleet state, and reports its lane through the
// FleetSession sync view.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

type revocationAuditRecorder struct {
	mu    sync.Mutex
	kinds []contextaudit.Kind
}

func (r *revocationAuditRecorder) EmitFleetEvent(_ context.Context, k contextaudit.Kind, _ any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kinds = append(r.kinds, k)
	return nil
}

func (r *revocationAuditRecorder) snapshot() []contextaudit.Kind {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]contextaudit.Kind(nil), r.kinds...)
}

func TestRevocationSweep_RidesConfigPollerCadence(t *testing.T) {
	mux := http.NewServeMux()
	// No bundle change (304): the sweep must still run after the poll.
	mux.HandleFunc("/api/v1/configs", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})
	mux.HandleFunc("/api/v1/catalog/list", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("sweep list query = %q, want none", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"c-rev","owner_user_id":"u","kind":"skill","slug":"deploy","version":"1","visibility":"team","description":"","signature":"","mandated":false,"published_at":"2026-10-01T00:00:00Z","mandated_reviewed":false,"lifecycle":"revoked","revoked_at":"2026-10-05T00:00:00Z"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if err := fleet.SaveTokens(fleet.TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fleet.ClearTokens() }()
	fleet.SeedFleetConfigForTesting(srv.URL, fleet.FleetConfig{Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC()})

	store := slashcmd.NewSkillStore(t.TempDir())
	reg, _ := slashcmd.NewRegistry(slashcmd.Deps{})
	if err := slashcmd.LiveRegister(store, reg, slashcmd.Skill{ID: "deploy", Trigger: "deploy", Kind: slashcmd.KindText, Body: "x",
		Source: slashcmd.SkillSourceCatalog, CatalogID: "c-rev", Version: "1"}); err != nil {
		t.Fatal(err)
	}
	audit := &revocationAuditRecorder{}
	client := fleet.NewClientForTesting(srv.URL)
	state := &fleetState{client: client, dataDir: t.TempDir(), lanes: fleet.NewSyncLanes(),
		skillStore: store, skillRegistry: reg, auditEmitter: audit}
	api := &API{fleet: state}

	// Production wires exactly this in startFleetBackgroundLocked (the
	// poller itself is not started under go test).
	poller := fleet.NewConfigPoller(client, state.dataDir, &compositeConfigApplier{state: state})
	poller.SetAfterPoll(api.runRevocationSweep)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	poller.Start(ctx)
	defer poller.Stop()

	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := store.Get("deploy"); err != nil {
			break
		}
	}
	if _, err := store.Get("deploy"); err == nil {
		t.Fatalf("revoked user catalog skill was not uninstalled by the after-poll sweep; lane=%+v", state.lanes.Snapshot(fleet.LaneCatalogRevocation))
	}
	if got := audit.snapshot(); len(got) != 1 || got[0] != contextaudit.KindFleetCatalogRevokedUninstalled {
		t.Errorf("audit kinds = %v", got)
	}
	if v := syncViewFromLanes(state.lanes); v.CatalogRevocation.Status != string(fleet.LaneOK) {
		t.Errorf("FleetSession catalogRevocation lane = %+v, want ok", v.CatalogRevocation)
	}
}
