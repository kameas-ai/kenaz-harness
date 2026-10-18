package rpc

// memory-sync-01MEMSY01 WP-PI (AC-PI-1): the mission's boot wiring and the
// WP09 session-delete cascade on a PROFILE A PREVIOUS RELEASE PRODUCED —
// the v0.91.0 sqlite snapshot (testdata/upgrade/v0.91.0/dump.sql, newest
// committed) plus a memory.gob written by v0.91.0's own core/memory code
// (core/memory/testdata/upgrade/v0.91.0) — booted through the production
// core.New + New(c), not an empty directory.

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	corememory "github.com/kameas-ai/kenaz-harness/core/memory"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

func TestMemorySync_UpgradedProfileBoot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dump, err := os.ReadFile(filepath.Join("..", "storage", "sqlite", "testdata", "upgrade", "v0.91.0", "dump.sql"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.Join(dir, "data.db"))+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	if err := upgradesnap.Materialize(ctx, raw, string(dump)); err != nil {
		t.Fatalf("materialise v0.91.0: %v", err)
	}
	_ = raw.Close()
	gob, err := os.ReadFile(filepath.Join("..", "memory", "testdata", "upgrade", "v0.91.0", "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.gob"), gob, 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := core.New(core.Options{DataDir: dir})
	if err != nil {
		t.Fatalf("core.New on the v0.91.0 profile: %v", err)
	}
	api := New(c)
	t.Cleanup(api.Shutdown)
	assertSettingsStoreIsSandboxed(t, api)
	if api.memStoreRef == nil || api.memClock == nil || api.memForgets == nil || api.memorySync == nil {
		t.Fatal("memory sync wiring missing on an upgraded profile")
	}
	before, _ := api.memStoreRef.List(ctx)
	if len(before) != 3 {
		t.Fatalf("v0.91.0 gob chunks visible after boot = %d, want 3", len(before))
	}

	// seed-session-1 exists in the v0.91.0 snapshot; deleting it must take
	// its session-scoped legacy chunk with it and leave the rest.
	if err := api.Sessions().Delete(ctx, "seed-session-1"); err != nil {
		t.Fatalf("Delete seed-session-1: %v", err)
	}
	re, err := corememory.NewChromemStore(filepath.Join(dir, "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	after, _ := re.List(ctx)
	left := map[string]corememory.Chunk{}
	for _, ch := range after {
		left[ch.ID] = ch
	}
	if _, ok := left["mem-v091-session"]; ok {
		t.Fatal("legacy session-scoped chunk outlived its deleted session")
	}
	g, ok := left["mem-v091-global"]
	if !ok || g.RecallOwn != 3 || g.RecallCount != 3 {
		t.Fatalf("legacy global chunk after upgrade boot = %+v (present=%v)", g, ok)
	}
	if _, ok := left["mem-v091-longterm"]; !ok {
		t.Fatal("legacy long_term chunk lost")
	}
}
