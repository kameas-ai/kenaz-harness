package rpc

// handoff_wp_pi_test.go — device-keys-handoff-01DEVKH01 WP-PI.
//
// AC-PI-1: the accepted-session persistence tests boot the v0.91.0 upgrade
// snapshot through production storagesqlite.Open (handoff_accept_test.go,
// handoff_share_test.go). Falsified 2026-10-07: replacing
// handoffAcceptStore.Persist's ReplayTranscript call with a no-op fails
// TestHandoffAccept_PersistsNewSession_OnUpgradedDB ("accepted rows = 0,
// want 7"); skipping ClearNodeID in handleNodeRemoved fails
// TestEnrollNodeRemoved ("node_id.txt must be cleared").
//
// This file covers the one persistence surface those do not: the
// provenance/dedupe ledger is a REAL file, and a corrupted or truncated
// one must degrade to "not yet accepted" (an accept re-creates a session
// and rewrites the ledger) rather than failing every future accept.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHandoffAcceptLedger_CorruptFileDegradesAndHeals(t *testing.T) {
	ctx := context.Background()
	mgr, closeDB := openHandoffSnapshot(t, t.TempDir())
	defer closeDB()
	dataDir := t.TempDir()
	ledger := filepath.Join(dataDir, "fleet", "handoff_accepted.json")
	if err := os.MkdirAll(filepath.Dir(ledger), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledger, []byte(`{"inbox-1": {"local_sess`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newHandoffAcceptStore(mgr, dataDir)
	if _, ok := store.Lookup(ctx, "inbox-1"); ok {
		t.Fatal("a truncated ledger must not resolve to a session")
	}
	v, err := store.Persist(ctx, shareRecordFor(t, mgr, "seed-session-1", "inbox-1"))
	if err != nil {
		t.Fatalf("Persist over a corrupt ledger: %v", err)
	}
	fi, err := os.Stat(ledger)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("ledger rewritten with mode %v (err %v), want 0600", fi.Mode().Perm(), err)
	}
	if got, ok := newHandoffAcceptStore(mgr, dataDir).Lookup(ctx, "inbox-1"); !ok || got.LocalSessionID != v.LocalSessionID {
		t.Fatalf("healed ledger lookup = %+v %v", got, ok)
	}
}
