package log

import (
	"context"
	"errors"
	"testing"
	"time"
)

func mkRow(id, sid, kind string, payload []byte, prev [32]byte) Row {
	var ph [32]byte
	for i := range payload {
		ph[i%32] ^= payload[i]
	}
	return Row{
		EventID:          id,
		SessionID:        sid,
		EmitterID:        "session/",
		Kind:             kind,
		EmittedAt:        time.UnixMilli(1700000000000),
		Payload:          payload,
		PayloadHash:      ph,
		PrevHash:         prev,
		RedactionSummary: `{"no_op":true}`,
	}
}

func TestMemoryBackend_AppendAndGet(t *testing.T) {
	b := NewMemoryBackend()
	ctx := context.Background()
	row := mkRow("01HFXY8B5VJ6T6T7AXJF9JT9F1", "01HFXY8B5VJ6T6T7AXJF9JT9F0", "test.k", []byte(`{}`), [32]byte{})
	if err := b.AppendRow(ctx, row, [32]byte{}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := b.GetRow(ctx, row.EventID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.EventID != row.EventID {
		t.Fatalf("event id mismatch: %s vs %s", got.EventID, row.EventID)
	}
}

func TestMemoryBackend_HeadEnforced(t *testing.T) {
	b := NewMemoryBackend()
	ctx := context.Background()
	row1 := mkRow("01HFXY8B5VJ6T6T7AXJF9JT9F1", "S", "test.k", []byte(`{"x":1}`), [32]byte{})
	if err := b.AppendRow(ctx, row1, [32]byte{}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	// Wrong expectedHead.
	row2 := mkRow("01HFXY8B5VJ6T6T7AXJF9JT9F2", "S", "test.k", []byte(`{"x":2}`), [32]byte{0xff})
	if err := b.AppendRow(ctx, row2, [32]byte{0xff}); !errors.Is(err, ErrChainHeadMismatch) {
		t.Fatalf("expected ErrChainHeadMismatch, got %v", err)
	}
	// Correct expectedHead.
	if err := b.AppendRow(ctx, row2, row1.PayloadHash); err != nil {
		t.Fatalf("ok append: %v", err)
	}
}

func TestMemoryBackend_BySessionOrdered(t *testing.T) {
	b := NewMemoryBackend()
	ctx := context.Background()
	ids := []string{
		"01HFXY8B5VJ6T6T7AXJF9JT9F1",
		"01HFXY8B5VJ6T6T7AXJF9JT9F2",
		"01HFXY8B5VJ6T6T7AXJF9JT9F3",
	}
	prev := [32]byte{}
	for _, id := range ids {
		r := mkRow(id, "S", "test.k", []byte(id), prev)
		if err := b.AppendRow(ctx, r, prev); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
		prev = r.PayloadHash
	}
	rows, err := b.SelectBySession(ctx, "S", "", 0, false)
	if err != nil {
		t.Fatalf("BySession: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	for i := 0; i < 3; i++ {
		if rows[i].EventID != ids[i] {
			t.Fatalf("order broken at %d: got %s want %s", i, rows[i].EventID, ids[i])
		}
	}
}

func TestMemoryBackend_AllSessions(t *testing.T) {
	b := NewMemoryBackend()
	ctx := context.Background()
	r1 := mkRow("01HFXY8B5VJ6T6T7AXJF9JT9F1", "A", "k.a", []byte(`{}`), [32]byte{})
	r2 := mkRow("01HFXY8B5VJ6T6T7AXJF9JT9F2", "B", "k.b", []byte(`{}`), [32]byte{})
	if err := b.AppendRow(ctx, r1, [32]byte{}); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := b.AppendRow(ctx, r2, [32]byte{}); err != nil {
		t.Fatalf("b: %v", err)
	}
	all, err := b.AllSessionIDs(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 sessions, got %v", all)
	}
}

func TestStoreCtor(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("nil backend should panic")
		}
	}()
	NewStore(nil)
}

func TestMigrationsList(t *testing.T) {
	got := Migrations()
	if len(got) != 4 {
		t.Fatalf("expected 4 migrations, got %d", len(got))
	}
	wantIDs := []string{"0001_events", "0002_event_chain_heads", "0003_redaction_rules", "0004_retention_config"}
	for i, m := range got {
		if m.ID != wantIDs[i] {
			t.Fatalf("migration[%d] = %q want %q", i, m.ID, wantIDs[i])
		}
		if m.SQL == "" {
			t.Fatalf("migration[%d] %q has empty SQL", i, m.ID)
		}
	}
}
