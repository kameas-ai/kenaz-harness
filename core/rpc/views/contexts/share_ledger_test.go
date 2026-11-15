package contexts_test

// share_ledger_test.go — kenaz-fleet spec 018 H-1 review finding 1: the
// harness remembers where it last shared each entry, per fleet user, so a
// re-share starts on the entry's current team instead of the org root.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	contextsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contexts"
)

func publishReq(nodeID, layer, team string) contextsview.ContextPublishRequest {
	return contextsview.ContextPublishRequest{
		NodeID: nodeID, Layer: layer, Kind: "guidance", Title: "t", Body: "b", Version: 1, TeamID: team,
	}
}

func TestContextShareStates_RecordsLastShare(t *testing.T) {
	syncer, _ := setupPublishSyncer(t)
	api := contextsview.New(nil).WithSyncer(syncer)
	dataDir := t.TempDir()
	user := "user-a"
	api.WithSelfUserID(func() string { return user }).WithShareLedgerDir(dataDir)
	ctx := context.Background()

	got, err := api.Context_ShareStates(ctx, []string{"notes/a.md"})
	if err != nil || len(got) != 0 {
		t.Fatalf("before any share = %+v, %v; want empty", got, err)
	}

	if _, err := api.Context_Publish(ctx, publishReq("notes/a.md", "team", "team-backend")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := api.Context_Publish(ctx, publishReq("notes/b.md", "org", "")); err != nil {
		t.Fatalf("publish org: %v", err)
	}
	got, err = api.Context_ShareStates(ctx, []string{"notes/a.md", "notes/b.md", "notes/never.md", ""})
	if err != nil {
		t.Fatal(err)
	}
	want := []contextsview.ContextShareStateView{
		{NodeID: "notes/a.md", Layer: "team", TeamID: "team-backend"},
		{NodeID: "notes/b.md", Layer: "org"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("share states = %+v, want %+v", got, want)
	}

	// The record is persisted, and a fresh ledger on the same data dir reads it.
	fresh := contextsview.New(nil).WithSyncer(syncer).
		WithSelfUserID(func() string { return user }).WithShareLedgerDir(dataDir)
	if again, _ := fresh.Context_ShareStates(ctx, []string{"notes/a.md"}); len(again) != 1 || again[0].TeamID != "team-backend" {
		t.Fatalf("after reload = %+v", again)
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, "fleet", "context_shares.json"))
	if err != nil {
		t.Fatalf("ledger file: %v", err)
	}
	// Keyed by wire id, never by library path (a path is user content).
	if strings.Contains(string(raw), "notes/a.md") {
		t.Fatalf("ledger stores the library path: %s", raw)
	}
	if fi, err := os.Stat(filepath.Join(dataDir, "fleet", "context_shares.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("ledger mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}

	// Another account on the same profile never inherits the record.
	user = "user-b"
	got, _ = api.Context_ShareStates(ctx, []string{"notes/a.md"})
	if len(got) != 0 {
		t.Fatalf("other account sees %+v", got)
	}
}

func TestContextShareStates_NoSyncer(t *testing.T) {
	got, err := contextsview.New(nil).Context_ShareStates(context.Background(), []string{"a"})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("= %#v, %v; want empty non-nil, nil", got, err)
	}
}
