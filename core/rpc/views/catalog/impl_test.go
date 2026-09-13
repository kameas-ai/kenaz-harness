package catalog

// AC-020/AC-021 (fleet-enforcement-truth-01PMZ505 WP11) at the RPC-facing
// surface: Catalog_Unpublish must reach the fleet client, emit an audit
// event on success, and NOT emit on failure. The 403→ErrCatalogForbidden
// mapping itself is pinned exhaustively at the fleet.Client layer
// (core/fleet/catalog_test.go's TestCatalog_Unpublish_* — this file does
// not duplicate that; it proves the view wraps it correctly).

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

// fakeAuditEmitter is a race-safe auditEmitter double.
type fakeAuditEmitter struct {
	mu     sync.Mutex
	events []contextaudit.Kind
}

func (f *fakeAuditEmitter) EmitFleetEvent(_ context.Context, kind contextaudit.Kind, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, kind)
	return nil
}

func (f *fakeAuditEmitter) snapshot() []contextaudit.Kind {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]contextaudit.Kind, len(f.events))
	copy(out, f.events)
	return out
}

func setupUnpublishTest(t *testing.T, forbid bool) (*API, *fakeAuditEmitter) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.NotFound(w, r)
			return
		}
		if forbid {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer:     srv.URL,
		ClientID:   "test",
		APIBaseURL: srv.URL,
		FetchedAt:  time.Now().UTC(),
	})
	ts := corefleet.TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)}
	if err := corefleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: OS keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	client := corefleet.NewClientForTesting(srv.URL)
	emitter := &fakeAuditEmitter{}
	api := NewAPI(client, nil, t.TempDir()).WithEmitter(emitter)
	return api, emitter
}

func TestAPI_CatalogUnpublish_EmitsAuditOnSuccess(t *testing.T) {
	api, emitter := setupUnpublishTest(t, false)

	if err := api.Catalog_Unpublish(context.Background(), "item-1"); err != nil {
		t.Fatalf("Catalog_Unpublish: %v", err)
	}

	events := emitter.snapshot()
	if len(events) != 1 || events[0] != contextaudit.KindFleetCatalogUnpublished {
		t.Errorf("audit events = %v, want exactly [fleet.catalog_unpublished]", events)
	}
}

// TestAPI_CatalogUnpublish_NoAuditOnFailure guards against the
// over-correction: emitting the audit event unconditionally (before
// checking the client's error) would record a withdrawal that never
// happened — worse than the original gap, which recorded nothing.
func TestAPI_CatalogUnpublish_NoAuditOnFailure(t *testing.T) {
	api, emitter := setupUnpublishTest(t, true)

	err := api.Catalog_Unpublish(context.Background(), "someone-elses-item")
	if err == nil {
		t.Fatal("Catalog_Unpublish on a 403: want error, got nil")
	}
	if !errors.Is(err, corefleet.ErrCatalogForbidden) {
		t.Errorf("error = %v, want to wrap ErrCatalogForbidden", err)
	}
	if events := emitter.snapshot(); len(events) != 0 {
		t.Errorf("audit events = %v, want none — a failed withdrawal must not be recorded as one", events)
	}
}

func TestAPI_CatalogUnpublish_FleetDisabled(t *testing.T) {
	api := NewAPI(nil, nil, t.TempDir())
	err := api.Catalog_Unpublish(context.Background(), "item-1")
	if !errors.Is(err, corefleet.ErrFleetDisabled) {
		t.Errorf("Catalog_Unpublish with nil client = %v, want ErrFleetDisabled", err)
	}
}
