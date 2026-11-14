package contexts_test

// share_ledger_outcomes_test.go — the share ledger records only shares
// Fleet actually took, never for an unknown fleet user, and moves a corrupt
// file aside instead of overwriting it.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	contextsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contexts"
)

// scriptedPush answers /api/v1/context/push with the next scripted reply.
type scriptedPush struct {
	mu      sync.Mutex
	status  int
	reply   func(nodeID string) map[string]any
	calls   int
	lastIDs []string
}

func (s *scriptedPush) set(status int, reply func(nodeID string) map[string]any) {
	s.mu.Lock()
	s.status, s.reply = status, reply
	s.mu.Unlock()
}

func (s *scriptedPush) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/api/v1/context/push" {
		http.NotFound(w, r)
		return
	}
	var req pushedRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	id := ""
	if len(req.Nodes) > 0 {
		id = req.Nodes[0].ID
	}
	s.mu.Lock()
	s.calls++
	status, reply := s.status, s.reply
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(reply(id))
}

func accepted(string) map[string]any {
	return map[string]any{"accepted_nodes": 1, "accepted_edges": 0, "conflicts": []any{}}
}

// ledgerRig is a contexts API over a real syncer with an external token
// source (no keychain).
func ledgerRig(t *testing.T, user *string) (*contextsview.API, *scriptedPush, string) {
	t.Helper()
	push := &scriptedPush{status: http.StatusOK, reply: accepted}
	srv := httptest.NewServer(push)
	t.Cleanup(srv.Close)
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
	dataDir := t.TempDir()
	api := contextsview.New(nil).WithSyncer(syncer).
		WithSelfUserID(func() string { return *user }).WithShareLedgerDir(dataDir)
	return api, push, dataDir
}

func shareStates(t *testing.T, api *contextsview.API, ids ...string) []contextsview.ContextShareStateView {
	t.Helper()
	got, err := api.Context_ShareStates(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A conflicted publish and a rejected publish leave NO ledger record, and
// do not disturb an earlier one.
func TestShareLedger_ConflictAndRejectionRecordNothing(t *testing.T) {
	user := "user-a"
	api, push, _ := ledgerRig(t, &user)
	ctx := context.Background()

	push.set(http.StatusOK, func(id string) map[string]any {
		return map[string]any{"accepted_nodes": 0, "accepted_edges": 0,
			"conflicts": []any{map[string]any{"node_id": id, "server_version": 3, "client_version": 1}}}
	})
	if _, err := api.Context_Publish(ctx, publishReq("notes/conflict.md", "team", "team-backend")); err != nil {
		t.Fatalf("conflicted publish: %v", err)
	}
	if got := shareStates(t, api, "notes/conflict.md"); len(got) != 0 {
		t.Fatalf("conflicted publish recorded %+v", got)
	}

	// Per-item rejection of the node itself.
	push.set(http.StatusOK, func(id string) map[string]any {
		return map[string]any{"accepted_nodes": 0, "accepted_edges": 0, "conflicts": []any{},
			"rejected": []any{map[string]any{"kind": "node", "id": id, "reason": "not_owner"}}}
	})
	if _, err := api.Context_Publish(ctx, publishReq("notes/rejected.md", "team", "team-backend")); err == nil {
		t.Fatal("rejected publish: want an error")
	}
	// Whole-batch refusal.
	push.set(http.StatusForbidden, func(string) map[string]any {
		return map[string]any{"code": "not_team_member", "message": "no"}
	})
	if _, err := api.Context_Publish(ctx, publishReq("notes/refused.md", "team", "team-backend")); err == nil {
		t.Fatal("refused publish: want an error")
	}
	if got := shareStates(t, api, "notes/rejected.md", "notes/refused.md"); len(got) != 0 {
		t.Fatalf("rejected publishes recorded %+v", got)
	}

	// An earlier good share survives a later conflicted re-share elsewhere.
	push.set(http.StatusOK, accepted)
	if _, err := api.Context_Publish(ctx, publishReq("notes/ok.md", "team", "team-backend")); err != nil {
		t.Fatal(err)
	}
	push.set(http.StatusOK, func(id string) map[string]any {
		return map[string]any{"accepted_nodes": 0, "conflicts": []any{map[string]any{"node_id": id, "server_version": 2, "client_version": 1}}}
	})
	if _, err := api.Context_Publish(ctx, publishReq("notes/ok.md", "org", "")); err != nil {
		t.Fatal(err)
	}
	if got := shareStates(t, api, "notes/ok.md"); len(got) != 1 || got[0].Layer != "team" || got[0].TeamID != "team-backend" {
		t.Fatalf("conflicted re-share overwrote the record: %+v", got)
	}
}

// No fleet user id: nothing is recorded (and nothing is read back).
func TestShareLedger_EmptyUserRecordsNothing(t *testing.T) {
	user := ""
	api, push, dataDir := ledgerRig(t, &user)
	ctx := context.Background()
	if _, err := api.Context_Publish(ctx, publishReq("notes/a.md", "team", "team-backend")); err != nil {
		t.Fatal(err)
	}
	if push.calls != 1 {
		t.Fatalf("push calls = %d, want 1 (the share itself still goes)", push.calls)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "fleet", "context_shares.json")); !os.IsNotExist(err) {
		t.Fatalf("ledger written for an unknown user: %v", err)
	}
	if got := shareStates(t, api, "notes/a.md"); len(got) != 0 {
		t.Fatalf("share states for unknown user = %+v", got)
	}
}

// A corrupt ledger file is moved aside, not overwritten by the next share.
func TestShareLedger_CorruptFileMovedAside(t *testing.T) {
	user := "user-a"
	api, _, dataDir := ledgerRig(t, &user)
	dir := filepath.Join(dataDir, "fleet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(dir, "context_shares.json")
	corrupt := []byte(`{"version":1,"shares":{"x":` /* truncated */)
	if err := os.WriteFile(ledgerPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Context_Publish(context.Background(), publishReq("notes/a.md", "team", "team-backend")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var aside string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "context_shares.json.corrupt-") {
			aside = filepath.Join(dir, e.Name())
		}
	}
	if aside == "" {
		t.Fatalf("corrupt ledger not moved aside; dir = %v", entries)
	}
	if b, _ := os.ReadFile(aside); string(b) != string(corrupt) {
		t.Fatalf("moved-aside copy = %q, want the original bytes", b)
	}
	if got := shareStates(t, api, "notes/a.md"); len(got) != 1 || got[0].TeamID != "team-backend" {
		t.Fatalf("new ledger = %+v", got)
	}
}
