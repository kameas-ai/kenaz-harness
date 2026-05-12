package narrative

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"
)

// PromotionWeights controls the per-signal contribution to the
// promotion score. Matches Settings.NarrativePromotionWeights.
type PromotionWeights struct {
	Retrieval float64 `json:"retrieval"`
	Citation  float64 `json:"citation"`
	Pin       float64 `json:"pin"`
}

// DefaultPromotionWeights returns the spec-locked default weights:
// retrieval×1, citation×3, pin×10.
func DefaultPromotionWeights() PromotionWeights {
	return PromotionWeights{Retrieval: 1, Citation: 3, Pin: 10}
}

// RollingWindowDays is the 90-day rolling window after which counters
// reset atomically (C-002).
const RollingWindowDays = 90

// ChunkMetrics holds the rolling counters for one chunk.
type ChunkMetrics struct {
	ChunkID        string
	Retrievals     int64
	Citations      int64
	UserPins       int64 // 0 or 1 — a "pin" signal is either present or absent
	LastRetrievedAt time.Time
	LastCitedAt    time.Time
	WindowStart    time.Time
}

// Score computes the weighted promotion score for the given weights.
// score = retrievals × Wr + citations × Wc + user_pins × Wp
func (m *ChunkMetrics) Score(w PromotionWeights) float64 {
	return float64(m.Retrievals)*w.Retrieval +
		float64(m.Citations)*w.Citation +
		float64(m.UserPins)*w.Pin
}

// MetricsStore tracks retrieval / citation / pin signals for narrative
// chunks. It is safe for concurrent use. An in-memory implementation is
// provided here; the production path would back it with the
// narrative_metrics SQL table (migration 0822).
type MetricsStore interface {
	IncrementRetrievals(ctx context.Context, chunkID string, at time.Time) error
	IncrementCitations(ctx context.Context, chunkID string, at time.Time) error
	SetUserPins(ctx context.Context, chunkID string, pinned bool) error
	Get(ctx context.Context, chunkID string) (ChunkMetrics, error)
	RollWindow(ctx context.Context, now time.Time) error
	PromotionCandidates(ctx context.Context, threshold float64, weights PromotionWeights) ([]string, error)
}

// ---- In-memory implementation ----

// MemMetricsStore is an in-memory MetricsStore. Safe for concurrent use.
// Used in tests and as the default when no SQL store is wired.
type MemMetricsStore struct {
	mu      sync.Mutex
	metrics map[string]*ChunkMetrics
	now     func() time.Time
}

// NewMemMetricsStore constructs an empty in-memory MetricsStore.
func NewMemMetricsStore() *MemMetricsStore {
	return &MemMetricsStore{
		metrics: make(map[string]*ChunkMetrics),
		now:     time.Now,
	}
}

// SetClock overrides the clock. Used in tests.
func (s *MemMetricsStore) SetClock(fn func() time.Time) {
	s.mu.Lock()
	s.now = fn
	s.mu.Unlock()
}

func (s *MemMetricsStore) getOrCreate(chunkID string) *ChunkMetrics {
	m, ok := s.metrics[chunkID]
	if !ok {
		m = &ChunkMetrics{
			ChunkID:     chunkID,
			WindowStart: s.now(),
		}
		s.metrics[chunkID] = m
	}
	return m
}

// IncrementRetrievals bumps the retrieval counter for chunkID.
func (s *MemMetricsStore) IncrementRetrievals(_ context.Context, chunkID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.getOrCreate(chunkID)
	m.Retrievals++
	m.LastRetrievedAt = at
	return nil
}

// IncrementCitations bumps the citation counter for chunkID.
func (s *MemMetricsStore) IncrementCitations(_ context.Context, chunkID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.getOrCreate(chunkID)
	m.Citations++
	m.LastCitedAt = at
	return nil
}

// SetUserPins sets or clears the pin signal for chunkID. A pin adds 10
// to the promotion score (FR-008). Pinned=false clears user_pins to 0.
func (s *MemMetricsStore) SetUserPins(_ context.Context, chunkID string, pinned bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.getOrCreate(chunkID)
	if pinned {
		m.UserPins = 1
	} else {
		m.UserPins = 0
	}
	return nil
}

// Get returns the metrics for chunkID. Returns a zero-value ChunkMetrics
// if no metrics have been recorded for chunkID yet.
func (s *MemMetricsStore) Get(_ context.Context, chunkID string) (ChunkMetrics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.metrics[chunkID]
	if !ok {
		return ChunkMetrics{ChunkID: chunkID, WindowStart: s.now()}, nil
	}
	return *m, nil
}

// RollWindow resets counters for any chunk whose WindowStart is more
// than RollingWindowDays old. Atomic per-chunk: the counters are
// zeroed and WindowStart is advanced to now.
func (s *MemMetricsStore) RollWindow(_ context.Context, now time.Time) error {
	threshold := now.AddDate(0, 0, -RollingWindowDays)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.metrics {
		if m.WindowStart.Before(threshold) {
			m.Retrievals = 0
			m.Citations = 0
			m.UserPins = 0
			m.WindowStart = now
		}
	}
	return nil
}

// PromotionCandidates returns chunk IDs whose score(weights) ≥ threshold,
// ordered descending by score.
func (s *MemMetricsStore) PromotionCandidates(_ context.Context, threshold float64, weights PromotionWeights) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type scored struct {
		id    string
		score float64
	}
	var out []scored
	for id, m := range s.metrics {
		sc := m.Score(weights)
		if sc >= threshold {
			out = append(out, scored{id, sc})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].score > out[j].score
	})
	ids := make([]string, len(out))
	for i, s := range out {
		ids[i] = s.id
	}
	return ids, nil
}

// PreludeScore computes the prelude ordering score for a long-term chunk.
// Order: recency_norm × log(retrievals+1). recency_norm is in [0,1],
// 1 meaning "now" and 0 meaning "oldest in the set". Ties broken by
// chunkID (lexicographic ascending) for determinism.
//
// Note: this is a pure arithmetic helper; the actual sorting lives in
// prelude.go.
func PreludeScore(m ChunkMetrics, windowStart time.Time, now time.Time) float64 {
	elapsed := now.Sub(windowStart).Seconds()
	var recency float64
	if elapsed > 0 {
		sinceLastAccess := now.Sub(m.LastRetrievedAt).Seconds()
		if sinceLastAccess < 0 {
			sinceLastAccess = 0
		}
		recency = 1.0 - sinceLastAccess/elapsed
		if recency < 0 {
			recency = 0
		}
	} else {
		recency = 1.0
	}
	freq := math.Log(float64(m.Retrievals+1))
	return recency * freq
}
