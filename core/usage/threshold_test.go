package usage_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/usage"
)

// recordingPublisher captures every event the checker publishes.
type recordingPublisher struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	topic   string
	payload usage.ThresholdCrossedPayload
}

func (p *recordingPublisher) Publish(topic string, payload any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tcp, ok := payload.(usage.ThresholdCrossedPayload)
	if !ok {
		return
	}
	p.events = append(p.events, recordedEvent{topic: topic, payload: tcp})
}

func (p *recordingPublisher) snapshot() []recordedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]recordedEvent, len(p.events))
	copy(out, p.events)
	return out
}

// fakeFiredStore implements the FiredRecorder contract with an
// in-memory map keyed by (year_month, pct). Mirrors the SQL
// INSERT-OR-IGNORE semantics.
type fakeFiredStore struct {
	mu  sync.Mutex
	set map[string]map[int]bool
}

func newFakeFiredStore() *fakeFiredStore {
	return &fakeFiredStore{set: map[string]map[int]bool{}}
}

func (f *fakeFiredStore) record(_ context.Context, ym string, pct int, _ time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.set[ym]; !ok {
		f.set[ym] = map[int]bool{}
	}
	if f.set[ym][pct] {
		return false, nil
	}
	f.set[ym][pct] = true
	return true, nil
}

func TestChecker_DialDisabled_NoFires(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{}
	store := newFakeFiredStore()

	c, err := usage.NewChecker(usage.CheckerConfig{
		Threshold: func() (float64, error) { return 0, nil }, // disabled
		Monthly:   func(context.Context, time.Time) (float64, error) { return 100, nil },
		Fired:     store.record,
		Publisher: pub,
		NowLocal:  func() time.Time { return time.Date(2026, 5, 1, 12, 0, 0, 0, time.Local) },
	})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	fired, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(fired) != 0 {
		t.Errorf("dial=0 → fired=%v, want empty", fired)
	}
	if got := pub.snapshot(); len(got) != 0 {
		t.Errorf("dial=0 → published %d events, want 0", len(got))
	}
}

func TestChecker_FiresAscendingTiers(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{}
	store := newFakeFiredStore()

	month := time.Date(2026, 5, 1, 12, 0, 0, 0, time.Local)

	totals := []float64{4.99, 5.01, 8.01, 10.01, 15.01, 20.01}
	wantFired := [][]int{
		nil,            // < 50%
		{50},           // crosses 50
		{80},           // crosses 80
		{100},          // crosses 100
		{150},          // crosses 150
		{200},          // crosses 200
	}

	var totalIdx int
	c, err := usage.NewChecker(usage.CheckerConfig{
		Threshold: func() (float64, error) { return 10, nil },
		Monthly: func(context.Context, time.Time) (float64, error) {
			return totals[totalIdx], nil
		},
		Fired:    store.record,
		Publisher: pub,
		NowLocal: func() time.Time { return month },
	})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}

	for i := range totals {
		totalIdx = i
		got, err := c.Check(context.Background())
		if err != nil {
			t.Fatalf("Check[%d]: %v", i, err)
		}
		if len(got) != len(wantFired[i]) {
			t.Errorf("step %d: fired=%v, want %v", i, got, wantFired[i])
			continue
		}
		for j, tier := range wantFired[i] {
			if got[j] != tier {
				t.Errorf("step %d: fired[%d]=%d, want %d", i, j, got[j], tier)
			}
		}
	}

	// Each pct fires exactly once across the whole month.
	events := pub.snapshot()
	if len(events) != 5 {
		t.Fatalf("total events fired = %d, want 5 (one per tier)", len(events))
	}
}

func TestChecker_DedupesAcrossCalls(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{}
	store := newFakeFiredStore()

	c, err := usage.NewChecker(usage.CheckerConfig{
		Threshold: func() (float64, error) { return 10, nil },
		Monthly:   func(context.Context, time.Time) (float64, error) { return 5.5, nil },
		Fired:     store.record,
		Publisher: pub,
		NowLocal:  func() time.Time { return time.Date(2026, 5, 15, 0, 0, 0, 0, time.Local) },
	})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := c.Check(context.Background()); err != nil {
			t.Fatalf("Check[%d]: %v", i, err)
		}
	}
	events := pub.snapshot()
	if len(events) != 1 {
		t.Errorf("dedup failed — published %d events, want 1", len(events))
	}
	if events[0].payload.Pct != 50 {
		t.Errorf("payload pct = %d, want 50", events[0].payload.Pct)
	}
	if events[0].payload.YearMonth != "2026-05" {
		t.Errorf("payload yearMonth = %q, want 2026-05", events[0].payload.YearMonth)
	}
	if events[0].payload.ThresholdUSD != 10 {
		t.Errorf("payload threshold = %v, want 10", events[0].payload.ThresholdUSD)
	}
}

func TestChecker_MonthRollover_FiresFresh(t *testing.T) {
	t.Parallel()
	pub := &recordingPublisher{}
	store := newFakeFiredStore()

	now := time.Date(2026, 4, 30, 23, 0, 0, 0, time.Local)
	monthlyByMonth := map[string]float64{
		"2026-04": 9.0,  // 90% used in April
		"2026-05": 5.5,  // 55% used in May
	}
	c, err := usage.NewChecker(usage.CheckerConfig{
		Threshold: func() (float64, error) { return 10, nil },
		Monthly: func(_ context.Context, t time.Time) (float64, error) {
			return monthlyByMonth[t.Format("2006-01")], nil
		},
		Fired:    store.record,
		Publisher: pub,
		NowLocal: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}

	// April: 90% → fires 50 + 80.
	if _, err := c.Check(context.Background()); err != nil {
		t.Fatalf("Check April: %v", err)
	}

	// Roll the clock to May 1.
	now = time.Date(2026, 5, 1, 0, 0, 5, 0, time.Local)

	// May: 55% → fires 50 again (under a fresh year_month key).
	if _, err := c.Check(context.Background()); err != nil {
		t.Fatalf("Check May: %v", err)
	}

	events := pub.snapshot()
	// Expect 3 events total: April[50, 80] + May[50].
	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3 (Apr 50, Apr 80, May 50)", len(events))
	}

	wantYM := []string{"2026-04", "2026-04", "2026-05"}
	wantPct := []int{50, 80, 50}
	for i, want := range wantYM {
		if events[i].payload.YearMonth != want {
			t.Errorf("event[%d] yearMonth = %q, want %q", i, events[i].payload.YearMonth, want)
		}
		if events[i].payload.Pct != wantPct[i] {
			t.Errorf("event[%d] pct = %d, want %d", i, events[i].payload.Pct, wantPct[i])
		}
	}
}

func TestChecker_RequiresAllCallbacks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  usage.CheckerConfig
	}{
		{
			name: "no threshold",
			cfg: usage.CheckerConfig{
				Monthly: func(context.Context, time.Time) (float64, error) { return 0, nil },
				Fired:   func(context.Context, string, int, time.Time) (bool, error) { return false, nil },
			},
		},
		{
			name: "no monthly",
			cfg: usage.CheckerConfig{
				Threshold: func() (float64, error) { return 0, nil },
				Fired:     func(context.Context, string, int, time.Time) (bool, error) { return false, nil },
			},
		},
		{
			name: "no fired",
			cfg: usage.CheckerConfig{
				Threshold: func() (float64, error) { return 0, nil },
				Monthly:   func(context.Context, time.Time) (float64, error) { return 0, nil },
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := usage.NewChecker(tc.cfg); err == nil {
				t.Errorf("NewChecker(%s) returned nil error, want validation failure", tc.name)
			}
		})
	}
}

func TestMonthBoundsLocal(t *testing.T) {
	t.Parallel()
	mid := time.Date(2026, 5, 15, 13, 30, 0, 0, time.Local)
	start, end := usage.MonthBoundsLocal(mid)

	if start.Year() != 2026 || start.Month() != 5 || start.Day() != 1 {
		t.Errorf("start = %v, want 2026-05-01 00:00 local", start)
	}
	if end.Year() != 2026 || end.Month() != 6 || end.Day() != 1 {
		t.Errorf("end = %v, want 2026-06-01 00:00 local", end)
	}
	if !end.After(start) {
		t.Errorf("end %v not after start %v", end, start)
	}
}
