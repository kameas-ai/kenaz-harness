package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestForgetOutbox_CoalesceAndPersist: the coalescing rule (a chunk Fleet
// cannot know sends nothing), idempotent enqueue, HLC-stamped ops, ack, and
// a reopen from the real file.
func TestForgetOutbox_CoalesceAndPersist(t *testing.T) {
	dir := t.TempDir()
	path := ForgetOutboxPath(dir)
	clock, _ := NewHLC("dev", "")
	ob, err := OpenForgetOutbox(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	// Never sent, never pulled: coalesced away.
	if err := ob.RecordDelete(Chunk{ID: "local-only"}); err != nil {
		t.Fatal(err)
	}
	// Sent (maybe in flight) and synced: both must be forgotten.
	if err := ob.RecordDelete(Chunk{ID: "sent", SyncSentAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := ob.RecordDelete(Chunk{ID: "synced", SyncedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_ = ob.RecordDelete(Chunk{ID: "synced", SyncedAt: time.Now()}) // idempotent
	ob2, err := OpenForgetOutbox(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := ob2.Pending()
	if len(p) != 2 || p[0].ID != "sent" || p[1].ID != "synced" || p[0].HLC == "" {
		t.Fatalf("pending after reopen = %+v", p)
	}
	if ob2.Has("local-only") || !ob2.Has("sent") {
		t.Fatal("Has() disagrees with the queue")
	}
	if err := ob2.Ack("sent"); err != nil {
		t.Fatal(err)
	}
	ob3, _ := OpenForgetOutbox(path, nil)
	if p := ob3.Pending(); len(p) != 1 || p[0].ID != "synced" {
		t.Fatalf("pending after ack + reopen = %+v", p)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("outbox mode = %v", fi.Mode().Perm())
	}
	if err := os.WriteFile(path, []byte("[garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenForgetOutbox(path, nil); err == nil {
		t.Fatal("a corrupt outbox must be an error, never a silent reset")
	}
}

// TestRemoveForSync_ReportsFinalState: Remove returns the row as of the
// delete, so a chunk marked sent after the caller's earlier read is still
// forgotten.
func TestRemoveForSync_ReportsFinalState(t *testing.T) {
	dir := t.TempDir()
	st, err := NewChromemStore(filepath.Join(dir, "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.Add(ctx, Chunk{ID: "x", ScopeKind: ScopeKindGlobal, Content: "c", Embedding: []float32{1}, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	staleRead := get(t, st, "x") // caller reads: not yet sent
	if staleRead.FleetMayKnow() {
		t.Fatal("precondition")
	}
	// A push marks it sent after that read.
	cs := st.(*chromemStore)
	cs.mu.Lock()
	cs.chunks[0].SyncSentAt = time.Now()
	cs.mu.Unlock()
	ob, _ := OpenForgetOutbox("", nil)
	if err := RemoveForSync(ctx, st, ob, "x"); err != nil {
		t.Fatal(err)
	}
	if !ob.Has("x") {
		t.Fatal("a chunk sent before the delete must be forgotten")
	}
	if all, _ := st.List(ctx); len(all) != 0 {
		t.Fatal("chunk not removed")
	}
}
