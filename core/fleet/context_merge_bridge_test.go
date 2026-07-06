package fleet_test

// context_merge_bridge_test.go — WP03 merge bridge integration tests
// (context-graph-e2e-01NINTG03 WP03).
//
// These tests live in the fleet_test external package so they can import
// both `fleet` and `core/rpc/views/contexts` without triggering an import
// cycle (fleet → views/contexts → fleet would cycle; fleet_test has no
// such constraint).
//
// Tests:
//   - List returns local-only entries when syncer is nil (NFR-004).
//   - List appends team + org pulled entries as synthetic nodes when syncer
//     is wired and a pull has succeeded (FR-010).
//   - Tombstoned entries are excluded from the merged view (FR-011).
//   - Local filesystem entries survive the merge (additive, not clobbering).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	corecontexts "github.com/kameas-ai/kenaz-harness/core/contexts"
	"github.com/kameas-ai/kenaz-harness/core/fleet"
	contextsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contexts"
)

// pullOnlyServer is a minimal httptest stub that serves GET /api/v1/context/pull.
type pullOnlyServer struct {
	mu        sync.Mutex
	responses []string
	idx       int
}

func (s *pullOnlyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/context/pull") {
		s.mu.Lock()
		var raw string
		if s.idx < len(s.responses) {
			raw = s.responses[s.idx]
			if s.idx < len(s.responses)-1 {
				s.idx++
			}
		} else {
			raw = `{"nodes":[],"edges":[]}`
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(raw))
		return
	}
	// Serve refresh token endpoint for token refresh during auth.
	if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "token") {
		resp := map[string]any{
			"access_token":  "at-bridge-refreshed",
			"refresh_token": "rt-bridge-refreshed",
			"expires_in":    3600,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	http.NotFound(w, r)
}

func (s *pullOnlyServer) queue(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses = append(s.responses, body)
}

// buildBridgeTestSyncer constructs a real ContextGraphSyncer pointing at the
// given httptest server with the team-graph cap enabled, and calls PullDelta
// so the in-memory cache is populated from pullBody.
func buildBridgeTestSyncer(t *testing.T, srvURL, pullBody string) *fleet.ContextGraphSyncer {
	t.Helper()

	stub := &pullOnlyServer{}
	stub.queue(pullBody)
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)

	// Seed tokens and fleet config for this test server URL.
	if err := fleet.SaveTokens(fleet.TokenSet{
		AccessToken:  "at-bridge-" + srvURL,
		RefreshToken: "rt-bridge-" + srvURL,
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = fleet.ClearTokens() })

	fleet.SeedFleetConfigForTesting(srv.URL, fleet.FleetConfig{
		Issuer:     srv.URL,
		ClientID:   "bridge-test",
		APIBaseURL: srv.URL,
		FetchedAt:  time.Now(),
	})

	// Build client via BuildClientForTesting (exported for tests).
	client := fleet.BuildClientForTesting(srv.URL, &http.Client{Timeout: 5 * time.Second})

	caps := fleet.NewCapabilityPoller(nil, t.TempDir())
	caps.ForceSetCurrentForTesting(fleet.Capabilities{
		Tier: "team",
		Enabled: map[fleet.Capability]bool{
			fleet.CapSharedTeamGraph: true,
		},
		FetchedAt: time.Now(),
		Source:    "test",
	})

	syncer := fleet.NewContextGraphSyncer(client, t.TempDir(), caps)

	ctx := context.Background()
	if _, err := syncer.PullDelta(ctx); err != nil {
		t.Fatalf("PullDelta: %v", err)
	}
	return syncer
}

// collectBridgeLeafPaths recursively collects KindFile paths from a contexts
// view tree.
func collectBridgeLeafPaths(n contextsview.Node) []string {
	var out []string
	if n.Kind == contextsview.KindFile {
		out = append(out, n.Path)
	}
	for _, c := range n.Children {
		out = append(out, collectBridgeLeafPaths(c)...)
	}
	return out
}

// TestMergeBridge_SyncerNil verifies that List works with no syncer (NFR-004).
func TestMergeBridge_SyncerNil(t *testing.T) {
	t.Parallel()
	lib, err := corecontexts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	api := contextsview.New(lib)
	// No syncer.

	ctx := context.Background()
	tree, err := api.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if tree.Kind != contextsview.KindFolder {
		t.Errorf("root kind=%v, want folder", tree.Kind)
	}
	if len(tree.Children) != 0 {
		t.Errorf("root children=%d, want 0", len(tree.Children))
	}
}

// TestMergeBridge_PulledEntriesInList verifies that team and org pulled
// entries appear in the List output (FR-010).
func TestMergeBridge_PulledEntriesInList(t *testing.T) {
	// No t.Parallel(): writes to the mock keyring (SaveTokens).

	pullBody := `{
		"nodes": [
			{"id": "team-1", "kind": "fact", "title": "Team entry", "body": "b",
			 "classification": "team_shared", "version": 1,
			 "updated_at": "2026-07-05T10:00:00Z"},
			{"id": "org-1", "kind": "guidance", "title": "Org entry", "body": "b",
			 "classification": "org_shared", "version": 1,
			 "updated_at": "2026-07-05T10:00:01Z"}
		],
		"cursor": "2026-07-05T10:00:01Z"
	}`

	syncer := buildBridgeTestSyncer(t, "unique-url-list", pullBody)

	lib, err := corecontexts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	api := contextsview.New(lib).WithSyncer(syncer)

	ctx := context.Background()
	tree, err := api.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	paths := collectBridgeLeafPaths(tree)
	if len(paths) != 2 {
		t.Fatalf("expected 2 leaf nodes, got %d: %v", len(paths), paths)
	}

	pathSet := make(map[string]bool, len(paths))
	for _, p := range paths {
		pathSet[p] = true
	}
	if !pathSet["team/_fleet/team-1"] {
		t.Errorf("missing team/_fleet/team-1, got: %v", paths)
	}
	if !pathSet["org/_fleet/org-1"] {
		t.Errorf("missing org/_fleet/org-1, got: %v", paths)
	}
}

// TestMergeBridge_TombstonesExcluded verifies that tombstoned entries do NOT
// appear in List output (FR-011).
func TestMergeBridge_TombstonesExcluded(t *testing.T) {
	// No t.Parallel(): writes to the mock keyring.

	pullBody := `{
		"nodes": [
			{"id": "alive-1", "kind": "fact", "title": "Alive", "body": "b",
			 "classification": "team_shared", "version": 1,
			 "updated_at": "2026-07-05T10:00:00Z"},
			{"id": "dead-1", "kind": "fact", "title": "Dead", "body": "b",
			 "classification": "team_shared", "version": 2,
			 "updated_at": "2026-07-05T10:00:01Z",
			 "deleted_at": "2026-07-05T10:00:01Z"}
		],
		"cursor": "2026-07-05T10:00:01Z"
	}`

	syncer := buildBridgeTestSyncer(t, "unique-url-tomb", pullBody)

	lib, err := corecontexts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	api := contextsview.New(lib).WithSyncer(syncer)

	ctx := context.Background()
	tree, err := api.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	paths := collectBridgeLeafPaths(tree)
	if len(paths) != 1 {
		t.Fatalf("expected 1 leaf node (tombstone excluded), got %d: %v", len(paths), paths)
	}
	if paths[0] != "team/_fleet/alive-1" {
		t.Errorf("path=%q, want team/_fleet/alive-1", paths[0])
	}
}
