package narrative

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// ---- fake LLMCaller ----

type fakeCaller struct {
	mu       sync.Mutex
	response string
	err      error
	calls    int
}

func (f *fakeCaller) Complete(_ context.Context, _, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

func (f *fakeCaller) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// ---- fake NarrativeWriter ----

type fakeWriter struct {
	mu        sync.Mutex
	written   []NarrativeWriteReq
	deleted   []string // turn IDs
	writeErr  error
}

func (w *fakeWriter) WriteNarrative(_ context.Context, req NarrativeWriteReq) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writeErr != nil {
		return "", w.writeErr
	}
	w.written = append(w.written, req)
	return "chunk-" + req.TurnID, nil
}

func (w *fakeWriter) DeleteByTurnFallback(_ context.Context, _, turnID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deleted = append(w.deleted, turnID)
	return nil
}

func (w *fakeWriter) snapshot() (written []NarrativeWriteReq, deleted []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]NarrativeWriteReq(nil), w.written...), append([]string(nil), w.deleted...)
}

// ---- helpers ----

func goodSynthJSON() string {
	return `{"request":"fix bug","investigated":"looked at file","learned":"nil pointer","outcome":"patched","next_steps":"write test"}`
}

// ---- tests ----

func TestMemJobQueue_Enqueue_Dedup(t *testing.T) {
	t.Parallel()
	q := NewMemJobQueue()
	ctx := context.Background()
	j := Job{
		ID: "j1", SessionID: "s1", TurnID: "t1",
		Status: JobStatusPending, RetryAt: time.Now(),
	}
	if err := q.Enqueue(ctx, j); err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	// Second enqueue for same turn — should be no-op.
	j2 := j
	j2.ID = "j2-dup"
	if err := q.Enqueue(ctx, j2); err != nil {
		t.Fatalf("second Enqueue: %v", err)
	}
	jobs, err := q.Pop(ctx, time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if len(jobs) != 1 {
		t.Errorf("Pop len = %d, want 1 (dedup should prevent second enqueue)", len(jobs))
	}
	if jobs[0].ID != "j1" {
		t.Errorf("job ID = %q, want j1", jobs[0].ID)
	}
}

func TestMemJobQueue_Pop_RespectsRetryAt(t *testing.T) {
	t.Parallel()
	q := NewMemJobQueue()
	ctx := context.Background()
	future := time.Now().Add(10 * time.Minute)
	past := time.Now().Add(-time.Second)

	jFuture := Job{ID: "jf", SessionID: "s1", TurnID: "t1", Status: JobStatusPending, RetryAt: future}
	jPast := Job{ID: "jp", SessionID: "s1", TurnID: "t2", Status: JobStatusPending, RetryAt: past}

	_ = q.Enqueue(ctx, jFuture)
	_ = q.Enqueue(ctx, jPast)

	jobs, _ := q.Pop(ctx, time.Now(), 10)
	if len(jobs) != 1 || jobs[0].ID != "jp" {
		t.Errorf("Pop should return only past-RetryAt job, got %d jobs", len(jobs))
	}
}

func TestPromoter_SuccessPath(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{response: goodSynthJSON()}
	writer := &fakeWriter{}
	queue := NewMemJobQueue()
	builder := NewSyntheticBuilder(caller)

	cfg := PromoterConfig{
		Parallelism:  1,
		PollInterval: 10 * time.Millisecond,
	}
	p := NewPromoter(cfg, queue, writer, builder)
	p.SetClock(func() time.Time { return time.Now() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	j := Job{
		ID:        "j-ok",
		SessionID: "sess-1",
		TurnID:    "turn-1",
		Status:    JobStatusPending,
		RetryAt:   time.Now().Add(-time.Second), // immediately ready
		Payload:   JobPayload{LastUserMsg: "hello", LastAssistantMsg: "world"},
	}
	if err := queue.Enqueue(ctx, j); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	p.Start(ctx)

	// Wait for worker to process.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		written, _ := writer.snapshot()
		if len(written) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	written, deleted := writer.snapshot()
	if len(written) == 0 {
		t.Fatal("expected a synthesised chunk to be written")
	}
	if written[0].Kind != ChunkKindNarrativeSynthesised {
		t.Errorf("Kind = %v, want narrative_synthesised", written[0].Kind)
	}
	if len(deleted) == 0 {
		t.Error("expected DeleteByTurnFallback to be called")
	}
}

func TestPromoter_FallbackOnFirstFailure(t *testing.T) {
	t.Parallel()
	caller := &fakeCaller{err: errors.New("service unavailable")}
	writer := &fakeWriter{}
	queue := NewMemJobQueue()
	builder := NewSyntheticBuilder(caller)

	cfg := PromoterConfig{
		Parallelism:  1,
		PollInterval: 10 * time.Millisecond,
	}
	p := NewPromoter(cfg, queue, writer, builder)
	p.SetClock(func() time.Time { return time.Now() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	j := Job{
		ID:        "j-fail",
		SessionID: "sess-2",
		TurnID:    "turn-2",
		Status:    JobStatusPending,
		RetryAt:   time.Now().Add(-time.Second),
		Payload:   JobPayload{LastUserMsg: "request"},
	}
	_ = queue.Enqueue(ctx, j)
	p.Start(ctx)

	// Wait for fallback write.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		written, _ := writer.snapshot()
		if len(written) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	written, _ := writer.snapshot()
	if len(written) == 0 {
		t.Fatal("expected extractive fallback chunk to be written on first failure")
	}
	if written[0].Kind != ChunkKindNarrativeExtractiveFallback {
		t.Errorf("Kind = %v, want narrative_extractive_fallback", written[0].Kind)
	}
}

func TestRetrySchedule(t *testing.T) {
	t.Parallel()
	now := time.Now()
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Minute},
		{1, 5 * time.Minute},
		{2, 30 * time.Minute},
		{3, 2 * time.Hour},
		{4, 24 * time.Hour},
	}
	for _, tc := range cases {
		got := NextRetryAt(tc.attempt, now)
		diff := got.Sub(now)
		if diff < tc.want-time.Second || diff > tc.want+time.Second {
			t.Errorf("attempt %d: NextRetryAt = %v (diff %v), want ~%v", tc.attempt, got, diff, tc.want)
		}
	}
}

func TestRetrySchedule_Exhausted(t *testing.T) {
	t.Parallel()
	if !Exhausted(MaxAttempts) {
		t.Errorf("Exhausted(%d) = false, want true", MaxAttempts)
	}
	if Exhausted(MaxAttempts - 1) {
		t.Errorf("Exhausted(%d) = true, want false", MaxAttempts-1)
	}
	zero := NextRetryAt(MaxAttempts, time.Now())
	if !zero.IsZero() {
		t.Errorf("NextRetryAt(MaxAttempts) should be zero, got %v", zero)
	}
}
