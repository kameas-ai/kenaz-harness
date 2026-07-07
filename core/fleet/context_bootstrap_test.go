package fleet

// context_bootstrap_test.go — WP01: fleet client methods for the
// context-bootstrap API. Uses an httptest.Server replaying fixture responses,
// mirroring the onboarding + context-graph client test patterns.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// makeBootstrapCaps returns a CapabilityPoller with CapContextBootstrap set
// (or absent when enabled=false).
func makeBootstrapCaps(t *testing.T, enabled bool) *CapabilityPoller {
	t.Helper()
	m := map[Capability]bool{}
	if enabled {
		m[CapContextBootstrap] = true
	}
	return &CapabilityPoller{
		done: make(chan struct{}),
		current: Capabilities{
			Tier:      "pro",
			Enabled:   m,
			FetchedAt: time.Now(),
			Source:    "test",
		},
	}
}

// bootstrapTestServer replays canned responses for the bootstrap endpoints.
// It records the last PATCH body via patchSink (if non-nil).
func bootstrapTestServer(t *testing.T, patchSink *BootstrapRunPatch) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/context/bootstrap/recipe":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(BootstrapRecipeWire{
				Version: "1.2.0",
				ConnectorCatalog: []BootstrapConnectorWire{
					{ID: "gmail", Label: "Gmail", MCPRecipeID: "gmail", ReadOnlyTools: []string{"search"}, MaxItems: 200},
				},
				ExtractionPrompts: BootstrapExtractionPrompts{PatternExtraction: "extract"},
				ContextTaxonomy:   BootstrapTaxonomyWire{NodeKinds: []string{"person", "project"}, EdgeKinds: []string{"works_with"}},
				ConfidenceRules:   BootstrapConfidenceRulesWire{AssertThreshold: 0.8, TentativeThreshold: 0.4, TrustedPersonWeight: 3, MinCorroborations: 3},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/bootstrap":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(StartBootstrapResponse{RunID: "run-abc", Status: "running", RecipeVersion: "1.2.0"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/context/bootstrap/run-abc":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(BootstrapRunStatus{
				RunID: "run-abc", Status: "running", Phase: "extraction",
				Connectors:   []BootstrapConnectorStatus{{Name: "gmail", Status: "running", ItemsProcessed: 10, NodesCreated: 3}},
				NodesCreated: 3, ItemsProcessed: 10, RecipeVersion: "1.2.0",
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/context/bootstrap/run-abc":
			if patchSink != nil {
				_ = json.NewDecoder(r.Body).Decode(patchSink)
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/context/bootstrap/run-final":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"run_finalized"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/bootstrap/run-abc/resume":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(BootstrapRunStatus{RunID: "run-abc", Status: "running", Phase: "extraction"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me/context/health":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ContextHealth{
				TotalNodes:        42,
				NodesBySourceKind: map[string]int{"email": 30, "chat_message": 12},
				LastSync:          "2026-07-05T00:00:00Z",
				ConnectedSources:  []string{"gmail", "slack"},
				LatestRun:         &BootstrapLatestRun{RunID: "run-abc", Status: "completed", FinishedAt: "2026-07-05T00:00:00Z"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newBootstrapClient(t *testing.T, url string, enabled bool) *BootstrapClient {
	t.Helper()
	stubTokens(t, TokenSet{AccessToken: "at-b", RefreshToken: "rt-b", ExpiresAt: time.Now().Add(time.Hour)})
	c := makeTestClient(t, url)
	return NewBootstrapClient(c, makeBootstrapCaps(t, enabled))
}

func TestBootstrapClient_FetchRecipe(t *testing.T) {
	srv := bootstrapTestServer(t, nil)
	defer srv.Close()
	bc := newBootstrapClient(t, srv.URL, true)

	rec, err := bc.FetchBootstrapRecipe(context.Background())
	if err != nil {
		t.Fatalf("FetchBootstrapRecipe: %v", err)
	}
	if rec.Version != "1.2.0" {
		t.Errorf("version = %q, want 1.2.0", rec.Version)
	}
	if len(rec.ConnectorCatalog) != 1 || rec.ConnectorCatalog[0].ID != "gmail" {
		t.Errorf("connector catalog = %+v", rec.ConnectorCatalog)
	}
	if rec.ConfidenceRules.MinCorroborations != 3 {
		t.Errorf("min_corroborations = %d, want 3", rec.ConfidenceRules.MinCorroborations)
	}
}

func TestBootstrapClient_StartRun(t *testing.T) {
	srv := bootstrapTestServer(t, nil)
	defer srv.Close()
	bc := newBootstrapClient(t, srv.URL, true)

	resp, err := bc.StartBootstrapRun(context.Background(), StartBootstrapRequest{
		ConsentedSources: []string{"gmail"},
		RecipeVersion:    "1.2.0",
	})
	if err != nil {
		t.Fatalf("StartBootstrapRun: %v", err)
	}
	if resp.RunID != "run-abc" || resp.Status != "running" {
		t.Errorf("got %+v", resp)
	}
}

func TestBootstrapClient_GetRun(t *testing.T) {
	srv := bootstrapTestServer(t, nil)
	defer srv.Close()
	bc := newBootstrapClient(t, srv.URL, true)

	st, err := bc.GetBootstrapRun(context.Background(), "run-abc")
	if err != nil {
		t.Fatalf("GetBootstrapRun: %v", err)
	}
	if st.Phase != "extraction" || st.NodesCreated != 3 {
		t.Errorf("got %+v", st)
	}
}

func TestBootstrapClient_PatchRun(t *testing.T) {
	var sink BootstrapRunPatch
	srv := bootstrapTestServer(t, &sink)
	defer srv.Close()
	bc := newBootstrapClient(t, srv.URL, true)

	err := bc.PatchBootstrapRun(context.Background(), "run-abc", BootstrapRunPatch{
		Phase:        "extraction",
		NodesCreated: 5,
		Connectors:   []BootstrapConnectorStatus{{Name: "gmail", Status: "done", NodesCreated: 5}},
	})
	if err != nil {
		t.Fatalf("PatchBootstrapRun: %v", err)
	}
	if sink.NodesCreated != 5 || sink.Phase != "extraction" {
		t.Errorf("server received %+v", sink)
	}
}

func TestBootstrapClient_PatchRun_Finalized(t *testing.T) {
	srv := bootstrapTestServer(t, nil)
	defer srv.Close()
	bc := newBootstrapClient(t, srv.URL, true)

	err := bc.PatchBootstrapRun(context.Background(), "run-final", BootstrapRunPatch{Status: "completed"})
	if !errors.Is(err, ErrBootstrapRunFinalized) {
		t.Errorf("want ErrBootstrapRunFinalized, got %v", err)
	}
}

func TestBootstrapClient_ResumeRun(t *testing.T) {
	srv := bootstrapTestServer(t, nil)
	defer srv.Close()
	bc := newBootstrapClient(t, srv.URL, true)

	st, err := bc.ResumeBootstrapRun(context.Background(), "run-abc")
	if err != nil {
		t.Fatalf("ResumeBootstrapRun: %v", err)
	}
	if st.Status != "running" {
		t.Errorf("got status %q, want running", st.Status)
	}
}

func TestBootstrapClient_GetContextHealth(t *testing.T) {
	srv := bootstrapTestServer(t, nil)
	defer srv.Close()
	bc := newBootstrapClient(t, srv.URL, true)

	h, err := bc.GetContextHealth(context.Background())
	if err != nil {
		t.Fatalf("GetContextHealth: %v", err)
	}
	if h.TotalNodes != 42 || h.NodesBySourceKind["email"] != 30 {
		t.Errorf("got %+v", h)
	}
	if h.LatestRun == nil || h.LatestRun.RunID != "run-abc" {
		t.Errorf("latest run = %+v", h.LatestRun)
	}
}

func TestBootstrapClient_CapabilityGate(t *testing.T) {
	// No network should be hit when the capability is absent.
	var networkHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		networkHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	bc := newBootstrapClient(t, srv.URL, false) // cap absent

	if bc.Enabled() {
		t.Error("Enabled() should be false without CapContextBootstrap")
	}
	if _, err := bc.FetchBootstrapRecipe(context.Background()); !errors.Is(err, ErrCapabilityNotInTier) {
		t.Errorf("FetchBootstrapRecipe: want ErrCapabilityNotInTier, got %v", err)
	}
	if _, err := bc.StartBootstrapRun(context.Background(), StartBootstrapRequest{}); !errors.Is(err, ErrCapabilityNotInTier) {
		t.Errorf("StartBootstrapRun: want ErrCapabilityNotInTier, got %v", err)
	}
	if err := bc.PatchBootstrapRun(context.Background(), "x", BootstrapRunPatch{}); !errors.Is(err, ErrCapabilityNotInTier) {
		t.Errorf("PatchBootstrapRun: want ErrCapabilityNotInTier, got %v", err)
	}
	if networkHit {
		t.Error("capability gate should prevent any network call")
	}
}

func TestBootstrapClient_NilAndNop(t *testing.T) {
	// Nil client → ErrFleetDisabled, no panic.
	var nilBC *BootstrapClient
	if nilBC.Enabled() {
		t.Error("nil BootstrapClient Enabled() should be false")
	}
	if _, err := nilBC.FetchBootstrapRecipe(context.Background()); !errors.Is(err, ErrFleetDisabled) {
		t.Errorf("nil client: want ErrFleetDisabled, got %v", err)
	}

	// Nop underlying client → ErrFleetDisabled.
	bc := NewBootstrapClient(nopClientInstance(), makeBootstrapCaps(t, true))
	if bc.Enabled() {
		t.Error("nop-backed BootstrapClient Enabled() should be false")
	}
	if _, err := bc.GetContextHealth(context.Background()); !errors.Is(err, ErrFleetDisabled) {
		t.Errorf("nop client: want ErrFleetDisabled, got %v", err)
	}
}
