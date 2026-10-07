package rpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	corememory "github.com/kameas-ai/kenaz-harness/core/memory"
)

// TestMemorySyncWiring_RealBoot drives memory-sync-01MEMSY01 through the
// production constructor on a real data dir: the per-install HLC is keyed
// by the fleet node id and stamps the real store, the forget outbox and
// the sync lane are built (and the lane, with no capability, stays off and
// makes no request), and deleting a session cascades to its
// session-scoped memory (WP09, H10) while a chunk the user promoted out of
// the session survives. Every assertion reads files back from disk.
func TestMemorySyncWiring_RealBoot(t *testing.T) {
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	t.Cleanup(api.Shutdown)
	assertSettingsStoreIsSandboxed(t, api)

	if api.memClock == nil || api.memForgets == nil || api.memorySync == nil {
		t.Fatalf("memory sync not wired: clock=%v outbox=%v lane=%v", api.memClock != nil, api.memForgets != nil, api.memorySync != nil)
	}
	nodeID, err := corefleet.NodeID(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if api.memClock.NodeID() != nodeID {
		t.Fatalf("HLC node %q != fleet node id %q", api.memClock.NodeID(), nodeID)
	}

	ctx := context.Background()
	rec, err := api.Sessions().Create(ctx, "doomed")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	now := time.Now().UTC()
	for _, ch := range []corememory.Chunk{
		{ID: "mem-sess", SessionID: rec.ID, ScopeKind: corememory.ScopeKindSession, ScopeID: rec.ID,
			Content: "session-only", Embedding: []float32{1, 0}, CreatedAt: now},
		{ID: "mem-promoted", SessionID: rec.ID, ScopeKind: corememory.ScopeKindGlobal,
			Content: "promoted out of the session", Embedding: []float32{0, 1}, CreatedAt: now},
	} {
		if err := api.memStoreRef.Add(ctx, ch); err != nil {
			t.Fatal(err)
		}
	}
	// The boot clock stamped the real store and persisted its state.
	if _, err := os.Stat(corememory.HLCStatePath(dataDir)); err != nil {
		t.Fatalf("hlc state not persisted: %v", err)
	}

	if err := api.Sessions().Delete(ctx, rec.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	reopened, err := corememory.NewChromemStore(filepath.Join(dataDir, "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	all, _ := reopened.List(ctx)
	ids := map[string]corememory.Chunk{}
	for _, ch := range all {
		ids[ch.ID] = ch
	}
	if _, ok := ids["mem-sess"]; ok {
		t.Fatal("session delete must cascade to its session-scoped memory")
	}
	p, ok := ids["mem-promoted"]
	if !ok {
		t.Fatal("a chunk promoted out of the session must survive the session's delete")
	}
	if p.CreatedHLC == "" || p.ScopeHLC == "" {
		t.Fatalf("boot clock did not stamp the store: %+v", p)
	}

	// No capability ⇒ the lane is off and nothing was sent.
	api.memorySync.RunOnce(ctx)
	if lane := api.settingsImpl.FleetSyncLanes().Snapshot(corefleet.LaneMemorySync); lane.Status != corefleet.LaneOff {
		t.Fatalf("memory sync lane without the capability = %+v, want off", lane)
	}
	if len(api.memForgets.Pending()) != 0 {
		t.Fatal("no forget may be queued: nothing here ever reached Fleet")
	}
}
