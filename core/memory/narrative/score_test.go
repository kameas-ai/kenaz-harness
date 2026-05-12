package narrative

import (
	"context"
	"testing"
	"time"
)

func TestMemMetricsStore_ScoreArithmetic(t *testing.T) {
	t.Parallel()
	s := NewMemMetricsStore()
	ctx := context.Background()
	now := time.Now()
	id := "chunk-x"

	// 2 retrievals × 1 + 1 citation × 3 + 1 pin × 10 = 15
	_ = s.IncrementRetrievals(ctx, id, now)
	_ = s.IncrementRetrievals(ctx, id, now)
	_ = s.IncrementCitations(ctx, id, now)
	_ = s.SetUserPins(ctx, id, true)

	m, err := s.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	w := DefaultPromotionWeights()
	score := m.Score(w)
	want := 2.0*1 + 1.0*3 + 1.0*10
	if score != want {
		t.Errorf("Score = %v, want %v", score, want)
	}
}

func TestMemMetricsStore_PromotionCandidates(t *testing.T) {
	t.Parallel()
	s := NewMemMetricsStore()
	ctx := context.Background()
	now := time.Now()

	// chunk-a: score = 10 (1 pin × 10)
	_ = s.SetUserPins(ctx, "chunk-a", true)
	// chunk-b: score = 3 (1 citation × 3)
	_ = s.IncrementCitations(ctx, "chunk-b", now)
	// chunk-c: score = 1 (1 retrieval)
	_ = s.IncrementRetrievals(ctx, "chunk-c", now)

	w := DefaultPromotionWeights()
	// threshold = 5: only chunk-a qualifies
	candidates, err := s.PromotionCandidates(ctx, 5, w)
	if err != nil {
		t.Fatalf("PromotionCandidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0] != "chunk-a" {
		t.Errorf("candidates = %v, want [chunk-a]", candidates)
	}

	// threshold = 1: chunk-a and chunk-b and chunk-c
	candidates, _ = s.PromotionCandidates(ctx, 1, w)
	if len(candidates) != 3 {
		t.Errorf("candidates len = %d, want 3", len(candidates))
	}
	// Ordered descending: chunk-a (10), chunk-b (3), chunk-c (1).
	if candidates[0] != "chunk-a" {
		t.Errorf("candidates[0] = %q, want chunk-a", candidates[0])
	}
}

func TestMemMetricsStore_RollWindow(t *testing.T) {
	t.Parallel()
	s := NewMemMetricsStore()
	ctx := context.Background()

	// Set window start to 91 days ago.
	old := time.Now().AddDate(0, 0, -(RollingWindowDays + 1))
	s.mu.Lock()
	m := s.getOrCreate("chunk-roll")
	m.WindowStart = old
	m.Retrievals = 99
	m.Citations = 5
	m.UserPins = 1
	s.mu.Unlock()

	_ = s.RollWindow(ctx, time.Now())

	got, _ := s.Get(ctx, "chunk-roll")
	if got.Retrievals != 0 {
		t.Errorf("Retrievals after roll = %d, want 0", got.Retrievals)
	}
	if got.Citations != 0 {
		t.Errorf("Citations after roll = %d, want 0", got.Citations)
	}
	if got.UserPins != 0 {
		t.Errorf("UserPins after roll = %d, want 0", got.UserPins)
	}
}

func TestMemMetricsStore_SetUserPins_Toggle(t *testing.T) {
	t.Parallel()
	s := NewMemMetricsStore()
	ctx := context.Background()
	id := "pin-toggle"

	_ = s.SetUserPins(ctx, id, true)
	m, _ := s.Get(ctx, id)
	if m.UserPins != 1 {
		t.Errorf("UserPins after pin=true: got %d, want 1", m.UserPins)
	}

	_ = s.SetUserPins(ctx, id, false)
	m, _ = s.Get(ctx, id)
	if m.UserPins != 0 {
		t.Errorf("UserPins after pin=false: got %d, want 0", m.UserPins)
	}
}

func TestDefaultPromotionWeights(t *testing.T) {
	t.Parallel()
	w := DefaultPromotionWeights()
	if w.Retrieval != 1 || w.Citation != 3 || w.Pin != 10 {
		t.Errorf("DefaultPromotionWeights = %+v, want {1 3 10}", w)
	}
}
