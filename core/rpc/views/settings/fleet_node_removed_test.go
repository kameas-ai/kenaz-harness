package settings

// fleet_node_removed_test.go — device-keys-handoff-01DEVKH01 WP02 (FR-2,
// AC-2; plan.md "TestEnrollNodeRemoved").
//
// A fake fleet answers enroll with fleet's real 403 node_removed envelope
// (kenaz-fleet service/device_keys.go writeKeyRegErr). Persistence is the
// REAL files under the rig's data dir: identity.json, node_id.txt and
// wire_id_salt — the F8 assertion is that clearing node_id.txt leaves every
// wire id unchanged.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

type removalFleet struct {
	srv *httptest.Server

	mu       sync.Mutex
	removed  map[string]bool // node ids an admin removed
	enrolled []string        // node ids enroll was called with
	deletes  []string        // DELETE /me/nodes/{id} paths
}

func newRemovalFleet(t *testing.T) *removalFleet {
	t.Helper()
	f := &removalFleet{removed: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/config.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"api_base_url": f.srv.URL})
	})
	mux.HandleFunc("POST /api/v1/enroll", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			NodeID string `json:"node_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.enrolled = append(f.enrolled, body.NodeID)
		gone := f.removed[body.NodeID]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if gone {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"node_removed","message":"this node was removed by an administrator; enroll under a new node_id after signing in again"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"org_id": "org-uuid", "team_id": "t1", "org_name": "Kameas", "role": "org_member", "tier": "team",
		})
	})
	mux.HandleFunc("DELETE /api/v1/me/nodes/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.deletes = append(f.deletes, r.PathValue("id"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *removalFleet) remove(node string) { f.mu.Lock(); f.removed[node] = true; f.mu.Unlock() }
func (f *removalFleet) enrolls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.enrolled...)
}
func (f *removalFleet) deleted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deletes...)
}

func newRemovalRig(t *testing.T) (*sessionRig, *removalFleet) {
	t.Helper()
	if fleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1")
	}
	f := newRemovalFleet(t)
	r := &sessionRig{dataDir: t.TempDir()}
	fleet.SetExternalTokenSource(r.token)
	t.Cleanup(func() { fleet.SetExternalTokenSource(nil) })
	r.api = &API{}
	r.api.SetFleetClient(fleet.NewClientForTestingWithDataDir(f.srv.URL, r.dataDir), r.dataDir)
	t.Cleanup(r.api.StopFleetBackground)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	return r, f
}

func TestEnrollNodeRemoved(t *testing.T) {
	r, f := newRemovalRig(t)
	ctx := context.Background()

	// A healthy first enroll persists node_id.txt + identity.json.
	if _, err := r.api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("first enroll: %v", err)
	}
	oldNode := fleet.ReadNodeID(r.dataDir)
	if oldNode == "" {
		t.Fatal("node_id.txt not written")
	}
	if _, err := os.Stat(fleet.IdentityFilePath(r.dataDir)); err != nil {
		t.Fatalf("identity.json: %v", err)
	}
	wire := fleet.NewWireIDs(r.dataDir)
	wireBefore := wire.For(fleet.WireLaneCurated, "guidance/style.md")
	saltPath := filepath.Join(r.dataDir, "fleet", "wire_id_salt")
	saltBefore, err := os.ReadFile(saltPath)
	if err != nil {
		t.Fatalf("wire_id_salt: %v", err)
	}

	// Admin removes the node; the next enroll is a terminal sign-out.
	f.remove(oldNode)
	_, err = r.api.FleetRefreshIdentity(ctx)
	if !errors.Is(err, fleet.ErrNodeRemoved) {
		t.Fatalf("err = %v, want ErrNodeRemoved", err)
	}
	v := snap(t, r.api)
	if v.State != FleetSessionSignedOut || v.Reason != FleetReasonNodeRemoved {
		t.Fatalf("snapshot = %s/%s, want signed_out/node_removed", v.State, v.Reason)
	}
	if !strings.Contains(v.Message, "removed") || v.Identity != nil || v.AutoRetry {
		t.Fatalf("snapshot = %+v", v)
	}
	if _, err := os.Stat(fleet.IdentityFilePath(r.dataDir)); !os.IsNotExist(err) {
		t.Fatalf("identity.json must be cleared, stat err = %v", err)
	}
	if got := fleet.ReadNodeID(r.dataDir); got != "" {
		t.Fatalf("node_id.txt must be cleared, still %q", got)
	}
	// F8: wire ids do not depend on node_id; the salt file is untouched.
	saltAfter, err := os.ReadFile(saltPath)
	if err != nil || string(saltAfter) != string(saltBefore) {
		t.Fatalf("wire_id_salt changed or removed (err=%v)", err)
	}
	if got := fleet.NewWireIDs(r.dataDir).For(fleet.WireLaneCurated, "guidance/style.md"); got != wireBefore {
		t.Fatalf("wire id changed across node_id regeneration: %s -> %s", wireBefore, got)
	}

	// "Stop enrolling": a refresh (e.g. the UI poll) makes NO request while
	// removed — even though the external token source still answers.
	n := len(f.enrolls())
	if _, err := r.api.FleetRefreshIdentity(ctx); !errors.Is(err, fleet.ErrNodeRemoved) {
		t.Fatalf("refresh while removed: %v", err)
	}
	if got := len(f.enrolls()); got != n {
		t.Fatalf("enroll requests while removed: %d -> %d, want none", n, got)
	}

	// A fresh sign-in enrolls under a NEW node id and succeeds.
	r.api.fleet.signInFlow = func(context.Context, fleet.EnvProfile) (fleet.TokenSet, error) {
		return fleet.TokenSet{AccessToken: jwtFor("sub-alice", "zitadel-org-1")}, nil
	}
	if _, err := r.api.FleetSignIn(ctx); err != nil {
		t.Fatalf("sign-in after removal: %v", err)
	}
	newNode := fleet.ReadNodeID(r.dataDir)
	if newNode == "" || newNode == oldNode {
		t.Fatalf("node id after re-sign-in = %q, want a fresh id (old %q)", newNode, oldNode)
	}
	enrolls := f.enrolls()
	if enrolls[len(enrolls)-1] != newNode {
		t.Fatalf("last enroll used %q, want %q", enrolls[len(enrolls)-1], newNode)
	}
	if v := snap(t, r.api); v.State != FleetSessionSignedIn {
		t.Fatalf("after re-sign-in: %s/%s", v.State, v.Reason)
	}
	if got := fleet.NewWireIDs(r.dataDir).For(fleet.WireLaneCurated, "guidance/style.md"); got != wireBefore {
		t.Fatal("wire id changed after re-enroll under a fresh node id")
	}
}

// Explicit sign-out self-unenrolls the node (fleet: dormant devices keep
// their keys and count toward the 16-key cap until unenrolled).
func TestFleetSignOut_SelfUnenrolls(t *testing.T) {
	r, f := newRemovalRig(t)
	ctx := context.Background()
	if _, err := r.api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	node := fleet.ReadNodeID(r.dataDir)
	_ = r.api.FleetSignOut(ctx)
	if got := f.deleted(); len(got) != 1 || got[0] != node {
		t.Fatalf("DELETE /me/nodes = %v, want [%s]", got, node)
	}
	// The node id survives a plain sign-out (self-unenroll is not sticky).
	if fleet.ReadNodeID(r.dataDir) != node {
		t.Fatal("sign-out must not clear node_id.txt")
	}
}

// No enrolled node → sign-out sends nothing and mints no node id.
func TestFleetSignOut_NoNode_NoUnenroll(t *testing.T) {
	r, f := newRemovalRig(t)
	_ = r.api.FleetSignOut(context.Background())
	if got := f.deleted(); len(got) != 0 {
		t.Fatalf("DELETE sent without an enrolled node: %v", got)
	}
	if fleet.ReadNodeID(r.dataDir) != "" {
		t.Fatal("sign-out minted a node id")
	}
}
