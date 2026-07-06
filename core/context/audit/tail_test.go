package audit

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"
)

func makeEvent(id string, prevHash [32]byte) TailEvent {
	payload := []byte(`{"kind":"test"}`)
	ph := sha256.Sum256([]byte(id + "|test|" + string(payload)))
	return TailEvent{
		ID:          id,
		Kind:        "test.kind",
		EmittedAt:   time.Now(),
		Payload:     payload,
		PayloadHash: ph,
		PrevHash:    prevHash,
	}
}

func TestMemoryTailReader_RoundTrip(t *testing.T) {
	ctx := context.Background()
	r := &MemoryTailReader{}

	e1 := makeEvent("01A", [32]byte{})
	e2 := makeEvent("01B", e1.PayloadHash)
	e3 := makeEvent("01C", e2.PayloadHash)
	r.Append(e1)
	r.Append(e2)
	r.Append(e3)

	// Since "" returns all events.
	all, err := r.Since(ctx, "", 0)
	if err != nil {
		t.Fatalf("Since error: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 events, got %d", len(all))
	}

	// Since "01A" returns [e2, e3].
	after, err := r.Since(ctx, "01A", 0)
	if err != nil {
		t.Fatalf("Since error: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("want 2 events after 01A, got %d", len(after))
	}
	if after[0].ID != "01B" {
		t.Errorf("first event after 01A: want 01B, got %s", after[0].ID)
	}

	// Limit works.
	limited, err := r.Since(ctx, "", 2)
	if err != nil {
		t.Fatalf("Since limit error: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("want 2 events with limit=2, got %d", len(limited))
	}
}

func TestMemoryTailReader_HighWater(t *testing.T) {
	ctx := context.Background()
	r := &MemoryTailReader{}

	hw, err := r.HighWater(ctx)
	if err != nil {
		t.Fatalf("HighWater empty: %v", err)
	}
	if hw != "" {
		t.Errorf("empty reader HighWater: want '', got %q", hw)
	}

	e1 := makeEvent("01X", [32]byte{})
	e2 := makeEvent("01Y", e1.PayloadHash)
	r.Append(e1)
	r.Append(e2)

	hw, err = r.HighWater(ctx)
	if err != nil {
		t.Fatalf("HighWater error: %v", err)
	}
	if hw != "01Y" {
		t.Errorf("HighWater: want 01Y, got %q", hw)
	}
}

func TestStoreTailReader_Delegates(t *testing.T) {
	ctx := context.Background()
	events := []TailEvent{
		makeEvent("AAA", [32]byte{}),
		makeEvent("BBB", [32]byte{}),
	}
	r := NewStoreTailReader(
		func(_ context.Context, afterID string, limit int) ([]TailEvent, error) {
			var out []TailEvent
			for _, e := range events {
				if afterID != "" && e.ID <= afterID {
					continue
				}
				out = append(out, e)
				if limit > 0 && len(out) >= limit {
					break
				}
			}
			return out, nil
		},
		func(_ context.Context) (string, error) {
			if len(events) == 0 {
				return "", nil
			}
			return events[len(events)-1].ID, nil
		},
	)

	all, err := r.Since(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2, got %d", len(all))
	}

	hw, err := r.HighWater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hw != "BBB" {
		t.Errorf("HighWater: want BBB, got %q", hw)
	}
}
