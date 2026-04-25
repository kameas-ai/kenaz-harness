package events

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

type recordingSink struct {
	mu     sync.Mutex
	emits  []emitCall
	failOn int // -1 = never; otherwise return error on the n-th call
	calls  int
}

type emitCall struct {
	Kind    string
	Payload map[string]any
}

func newRecordingSink() *recordingSink { return &recordingSink{failOn: -1} }

func (r *recordingSink) Emit(ctx context.Context, kind string, payload map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.failOn >= 0 && r.calls-1 == r.failOn {
		return errors.New("synthetic")
	}
	r.emits = append(r.emits, emitCall{Kind: kind, Payload: payload})
	return nil
}

func (r *recordingSink) Snapshot() []emitCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]emitCall, len(r.emits))
	copy(out, r.emits)
	return out
}

func TestBuffered_Emit_BelowCapacity(t *testing.T) {
	t.Parallel()
	b := NewBuffered(8)
	for i := 0; i < 5; i++ {
		if err := b.Emit(context.Background(), "k", map[string]any{"i": i}); err != nil {
			t.Fatalf("emit %d: %v", i, err)
		}
	}
	if got := b.Len(); got != 5 {
		t.Fatalf("Len = %d, want 5", got)
	}
	if got := b.Dropped(); got != 0 {
		t.Fatalf("Dropped = %d, want 0", got)
	}
}

func TestBuffered_DrainPreservesOrder(t *testing.T) {
	t.Parallel()
	b := NewBuffered(8)
	for i := 0; i < 5; i++ {
		if err := b.Emit(context.Background(), "k", map[string]any{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	target := newRecordingSink()
	if err := b.DrainTo(context.Background(), target); err != nil {
		t.Fatalf("drain: %v", err)
	}
	got := target.Snapshot()
	if len(got) != 5 {
		t.Fatalf("got %d emits, want 5", len(got))
	}
	for i, ev := range got {
		if v := ev.Payload["i"]; v != i {
			t.Errorf("emit %d payload[i]=%v, want %d", i, v, i)
		}
	}
	if !b.Released() {
		t.Fatal("expected sink released after empty drain")
	}
}

func TestBuffered_OverflowDropsOldestAndReportsCount(t *testing.T) {
	t.Parallel()
	b := NewBuffered(3)
	for i := 0; i < 10; i++ {
		if err := b.Emit(context.Background(), "k", map[string]any{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	if got := b.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}
	if got := b.Dropped(); got != 7 {
		t.Fatalf("Dropped = %d, want 7", got)
	}
	target := newRecordingSink()
	if err := b.DrainTo(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	got := target.Snapshot()
	// 3 surviving real events (indices 7, 8, 9) plus 1 synthetic
	// events_dropped with count=7.
	if len(got) != 4 {
		t.Fatalf("got %d emits, want 4", len(got))
	}
	for i, want := range []int{7, 8, 9} {
		if v := got[i].Payload["i"]; v != want {
			t.Errorf("emit[%d].Payload[i] = %v, want %d", i, v, want)
		}
	}
	if got[3].Kind != "events_dropped" {
		t.Errorf("synthetic kind = %q, want events_dropped", got[3].Kind)
	}
	if v := got[3].Payload["count"]; v != int64(7) {
		t.Errorf("dropped count = %v, want 7", v)
	}
}

func TestBuffered_DrainErrorRebuffersRemainder(t *testing.T) {
	t.Parallel()
	b := NewBuffered(8)
	for i := 0; i < 5; i++ {
		if err := b.Emit(context.Background(), "k", map[string]any{"i": i}); err != nil {
			t.Fatal(err)
		}
	}
	target := newRecordingSink()
	target.failOn = 2 // 0-indexed: third emit fails
	err := b.DrainTo(context.Background(), target)
	if err == nil {
		t.Fatal("expected drain error")
	}
	// Remaining 3 events (indices 2, 3, 4) should still be in the buffer.
	if got := b.Len(); got != 3 {
		t.Fatalf("Len after partial drain = %d, want 3", got)
	}
	// Successful retry: clear failOn and drain again.
	target.failOn = -1
	if err := b.DrainTo(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	got := target.Snapshot()
	// 2 successful before failure + 3 redrained = 5 total
	if len(got) != 5 {
		t.Fatalf("after retry got %d, want 5", len(got))
	}
	for i, ev := range got {
		if v := ev.Payload["i"]; v != i {
			t.Errorf("emit[%d].Payload[i] = %v, want %d", i, v, i)
		}
	}
}

func TestBuffered_EmitAfterRelease(t *testing.T) {
	t.Parallel()
	b := NewBuffered(8)
	if err := b.DrainTo(context.Background(), newRecordingSink()); err != nil {
		t.Fatal(err)
	}
	if !b.Released() {
		t.Fatal("expected released after empty drain")
	}
	err := b.Emit(context.Background(), "k", nil)
	if err == nil {
		t.Fatal("expected error emitting to released sink")
	}
}

func TestBuffered_ConcurrentEmits(t *testing.T) {
	t.Parallel()
	b := NewBuffered(10000)
	const writers = 16
	const each = 200
	var wg sync.WaitGroup
	var seq atomic.Int64
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				_ = b.Emit(context.Background(), "k", map[string]any{
					"seq": seq.Add(1),
				})
			}
		}()
	}
	wg.Wait()
	if got := b.Len(); got != writers*each {
		t.Fatalf("Len = %d, want %d", got, writers*each)
	}
	target := newRecordingSink()
	if err := b.DrainTo(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if len(target.Snapshot()) != writers*each {
		t.Fatalf("drained %d, want %d", len(target.Snapshot()), writers*each)
	}
}

func TestBuffered_ContextCancelled(t *testing.T) {
	t.Parallel()
	b := NewBuffered(8)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := b.Emit(ctx, "k", nil)
	if err == nil {
		t.Fatal("expected ctx error")
	}
}

func TestBuffered_DefensiveCopyOfPayload(t *testing.T) {
	t.Parallel()
	b := NewBuffered(8)
	p := map[string]any{"x": 1}
	if err := b.Emit(context.Background(), "k", p); err != nil {
		t.Fatal(err)
	}
	p["x"] = 999 // mutate after Emit
	target := newRecordingSink()
	if err := b.DrainTo(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	got := target.Snapshot()
	if got[0].Payload["x"] != 1 {
		t.Fatalf("expected defensive copy; got %v", got[0].Payload["x"])
	}
}
