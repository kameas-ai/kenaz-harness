package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// copyV091Gob places the v0.91.0-produced memory.gob (written by that
// release's own code; see testdata/upgrade/v0.91.0/PROVENANCE.md) in a
// fresh dir and returns its path.
func copyV091Gob(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "upgrade", "v0.91.0", "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "memory.gob")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestUpgrade_V091MemoryGob (memory-sync-01MEMSY01 WP-PI, AC-PI-1 for the
// gob surface): a memory.gob a PREVIOUS RELEASE wrote loads under HEAD,
// migrates its recall counter into the G-counter split, reads as unstamped
// and never-synced, takes a stamped mutation, and round-trips to disk.
func TestUpgrade_V091MemoryGob(t *testing.T) {
	path := copyV091Gob(t)
	st, err := NewChromemStore(path)
	if err != nil {
		t.Fatalf("v0.91.0 gob must load under HEAD: %v", err)
	}
	g := get(t, st, "mem-v091-global")
	if g.RecallOwn != 3 || g.RecallCount != 3 || !g.Pinned || g.Title != "Build uses make" {
		t.Fatalf("legacy global chunk = %+v", g)
	}
	for _, id := range []string{"mem-v091-global", "mem-v091-session", "mem-v091-longterm"} {
		c := get(t, st, id)
		if c.CreatedHLC != "" || c.PinnedHLC != "" || c.FleetMayKnow() || c.SyncBlocked != "" || c.EmbedPending || len(c.Embedding) != 3 {
			t.Fatalf("%s must read as unstamped, never-synced, embedded: %+v", id, c)
		}
	}
	clock, _ := NewHLC("dev", "")
	clock.SetNow(func() time.Time { return time.Now() })
	st.(ClockSetter).SetClock(clock)
	if err := st.(PruneCapable).SetPinned(context.Background(), "mem-v091-global", false); err != nil {
		t.Fatal(err)
	}
	re, err := NewChromemStore(path)
	if err != nil {
		t.Fatal(err)
	}
	g2 := get(t, re, "mem-v091-global")
	if g2.Pinned || g2.PinnedHLC == "" || g2.RecallOwn != 3 || !g2.SyncDirty {
		t.Fatalf("after a HEAD mutation + reopen: %+v", g2)
	}
}
