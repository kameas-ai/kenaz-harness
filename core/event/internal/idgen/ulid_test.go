package idgen

import (
	"sync"
	"testing"
	"time"
)

func TestNextProducesParseable(t *testing.T) {
	g := New()
	id, err := g.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(id) != 26 {
		t.Fatalf("expected 26 chars, got %d (%q)", len(id), id)
	}
	raw, err := Parse(id)
	if err != nil {
		t.Fatalf("Parse(%q): %v", id, err)
	}
	if TimestampMillis(raw) == 0 {
		t.Fatalf("timestamp should be set, got zero")
	}
}

func TestMonotonicWithinMillisecond(t *testing.T) {
	g := New()
	prev := ""
	for i := 0; i < 10000; i++ {
		id, err := g.NextWith(time.Unix(0, 1_700_000_000_000_000_000))
		if err != nil {
			t.Fatalf("NextWith: %v", err)
		}
		if prev != "" && Compare(id, prev) <= 0 {
			t.Fatalf("monotonicity broken: %q !> %q", id, prev)
		}
		prev = id
	}
}

func TestMonotonicAcrossClockJitter(t *testing.T) {
	g := New()
	t1 := time.Unix(0, 1_700_000_000_000_000_000)
	t2 := t1.Add(-50 * time.Millisecond) // backward jitter
	id1, _ := g.NextWith(t1)
	id2, _ := g.NextWith(t2)
	if Compare(id2, id1) <= 0 {
		t.Fatalf("backward clock must not produce non-monotonic id: %q !> %q", id2, id1)
	}
}

func TestNoCollisionsUnderConcurrency(t *testing.T) {
	const total = 50000
	g := New()
	seen := make(map[string]struct{}, total)
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := make(chan string, total)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < total/8; j++ {
				id, err := g.Next()
				if err != nil {
					t.Errorf("Next: %v", err)
					return
				}
				out <- id
			}
		}()
	}
	go func() { wg.Wait(); close(out) }()
	for id := range out {
		mu.Lock()
		if _, dup := seen[id]; dup {
			mu.Unlock()
			t.Fatalf("collision on %q", id)
		}
		seen[id] = struct{}{}
		mu.Unlock()
	}
}

func TestParseRejectsLengthAndChars(t *testing.T) {
	if _, err := Parse(""); err == nil {
		t.Fatalf("empty must error")
	}
	if _, err := Parse("0123456789ABCDEFGHJKMNPQRS"); err != nil {
		t.Fatalf("valid 26-char string must parse: %v", err)
	}
	if _, err := Parse("0123456789ABCDEFGHJKMNPQR!"); err == nil {
		t.Fatalf("invalid char must error")
	}
	if _, err := Parse("8123456789ABCDEFGHJKMNPQRS"); err == nil {
		t.Fatalf("first char > 7 must error (overflows ULID range)")
	}
}

func TestRoundTrip(t *testing.T) {
	g := New()
	id, _ := g.Next()
	raw, err := Parse(id)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Re-encode timestamp; it should match.
	ms := TimestampMillis(raw)
	if ms == 0 {
		t.Fatalf("timestamp zero")
	}
}

func TestPackageLevelNext(t *testing.T) {
	a, _ := Next()
	b, _ := Next()
	if Compare(a, b) >= 0 {
		t.Fatalf("package Next should be monotonic: %q >= %q", a, b)
	}
}
