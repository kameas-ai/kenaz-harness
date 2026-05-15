package log

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"
)

// buildChain inserts n rows into backend using the canonical hash chain
// scheme and returns the inserted rows in order. prefix is prepended to
// the sequential event_id to allow multiple sessions with distinct IDs.
func buildChain(t *testing.T, b *MemoryBackend, n int, session string, prefix string) []Row {
	t.Helper()
	ctx := context.Background()
	rows := make([]Row, 0, n)
	var prev [32]byte

	for i := 0; i < n; i++ {
		payload := []byte(fmt.Sprintf(`{"seq":%d}`, i))
		r := Row{
			EventID:   fmt.Sprintf("%s%024d", prefix, i), // lexicographic order guaranteed
			SessionID: session,
			EmitterID: "test/chain",
			Kind:      "test.chain",
			EmittedAt: time.UnixMilli(1700000000000 + int64(i)),
			Payload:   payload,
			PrevHash:  prev,
		}
		// Compute payload_hash the same way chain.go does.
		r.PayloadHash = sha256.Sum256(canonicalSerialize(r))
		if err := b.AppendRow(ctx, r, prev); err != nil {
			t.Fatalf("AppendRow[%d]: %v", i, err)
		}
		prev = r.PayloadHash
		rows = append(rows, r)
	}
	return rows
}

func TestVerifyChain_Clean(t *testing.T) {
	b := NewMemoryBackend()
	buildChain(t, b, 100, "sess-clean", "01A")

	res, err := VerifyChain(context.Background(), b, "", "")
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.Verified {
		t.Errorf("expected Verified=true, broken at %q", res.BrokenAtID)
	}
	if res.RowsChecked != 100 {
		t.Errorf("expected 100 rows checked, got %d", res.RowsChecked)
	}
}

func TestVerifyChain_TamperedPayload(t *testing.T) {
	b := NewMemoryBackend()
	rows := buildChain(t, b, 50, "sess-tamper", "01B")

	// Tamper the 25th row's payload so hash recomputation diverges.
	target := rows[24].EventID
	if err := b.TamperPayloadForTest(target, func(p []byte) {
		if len(p) > 0 {
			p[0] = 'X'
		}
	}); err != nil {
		t.Fatalf("TamperPayloadForTest: %v", err)
	}

	res, err := VerifyChain(context.Background(), b, "", "")
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if res.Verified {
		t.Error("expected Verified=false after tamper")
	}
	if res.BrokenAtID != target {
		t.Errorf("BrokenAtID = %q, want %q", res.BrokenAtID, target)
	}
}

func TestVerifyChain_FromToFilter_FullChain(t *testing.T) {
	// Build a full chain and verify the entire range — this ensures the
	// from/to filter passes when the whole chain is selected.
	b := NewMemoryBackend()
	rows := buildChain(t, b, 20, "sess-filter", "01C")

	from := rows[0].EventID
	to := rows[19].EventID
	res, err := VerifyChain(context.Background(), b, from, to)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.Verified {
		t.Errorf("full chain should verify clean, broken at %q", res.BrokenAtID)
	}
	if res.RowsChecked != 20 {
		t.Errorf("expected 20 rows checked, got %d", res.RowsChecked)
	}
}

func TestVerifyChain_MigrationBoundary_ZeroPrevHash(t *testing.T) {
	b := NewMemoryBackend()

	// Insert two independent sessions with distinct event_id prefixes
	// so no collision occurs.
	buildChain(t, b, 10, "sessA", "01D")
	buildChain(t, b, 10, "sessB", "01E")

	res, err := VerifyChain(context.Background(), b, "", "")
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.Verified {
		t.Errorf("multi-session chain: expected Verified=true, broken at %q", res.BrokenAtID)
	}
	if res.RowsChecked != 20 {
		t.Errorf("expected 20 rows checked, got %d", res.RowsChecked)
	}
}

func TestVerifyChain_Empty(t *testing.T) {
	b := NewMemoryBackend()
	res, err := VerifyChain(context.Background(), b, "", "")
	if err != nil {
		t.Fatalf("VerifyChain on empty store: %v", err)
	}
	if !res.Verified {
		t.Error("empty store should verify as true")
	}
	if res.RowsChecked != 0 {
		t.Errorf("expected 0 rows checked, got %d", res.RowsChecked)
	}
}
