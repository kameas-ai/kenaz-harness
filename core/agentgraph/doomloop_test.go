package agentgraph

import (
	"sync"
	"testing"
)

// TestToolCallHistory_TouchCountsRepeats asserts touch returns an
// incrementing count per distinct key and 1 on first sighting.
func TestToolCallHistory_TouchCountsRepeats(t *testing.T) {
	t.Parallel()
	h := newToolCallHistory(4)
	if got := h.touch("a"); got != 1 {
		t.Fatalf("first touch(a) = %d, want 1", got)
	}
	if got := h.touch("b"); got != 1 {
		t.Fatalf("first touch(b) = %d, want 1", got)
	}
	if got := h.touch("a"); got != 2 {
		t.Fatalf("second touch(a) = %d, want 2", got)
	}
	if got := h.touch("a"); got != 3 {
		t.Fatalf("third touch(a) = %d, want 3", got)
	}
}

// TestToolCallHistory_BoundedEviction asserts the LRU never grows past
// its configured capacity — once full, the least-recently-touched key is
// evicted to make room for a new one, keeping memory flat across an
// arbitrarily long run.
func TestToolCallHistory_BoundedEviction(t *testing.T) {
	t.Parallel()
	h := newToolCallHistory(2)
	h.touch("a")
	h.touch("b")
	if got := h.order.Len(); got != 2 {
		t.Fatalf("len after 2 distinct keys = %d, want 2", got)
	}
	// "c" pushes the history over capacity; "a" is least-recently-seen
	// (touched before "b") so it should be evicted.
	h.touch("c")
	if got := h.order.Len(); got != 2 {
		t.Fatalf("len after eviction = %d, want capacity 2", got)
	}
	if _, ok := h.elems["a"]; ok {
		t.Errorf("key %q should have been evicted as least-recently-seen", "a")
	}
	if _, ok := h.elems["c"]; !ok {
		t.Errorf("key %q should be present after insertion", "c")
	}
	// "a" was evicted, so touching it again starts a fresh count.
	if got := h.touch("a"); got != 1 {
		t.Errorf("touch(a) after eviction = %d, want 1 (fresh count)", got)
	}
}

// TestToolCallHistory_ConcurrentTouchIsRaceSafe exercises touch from many
// goroutines simultaneously; go test -race should stay clean since the
// LRU guards all state behind its own mutex.
func TestToolCallHistory_ConcurrentTouchIsRaceSafe(t *testing.T) {
	t.Parallel()
	h := newToolCallHistory(16)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			h.touch("shared-key")
		}(i)
	}
	wg.Wait()
	if got := h.touch("shared-key"); got != 51 {
		t.Errorf("final touch count = %d, want 51", got)
	}
}

// TestDoomLoopKey_NormalizesKeyOrderAndNumericFormatting asserts
// trivially-varied encodings of the same logical arguments collapse onto
// the same key: map key order and insignificant numeric formatting
// (1 vs 1.0 vs 1.00) must not matter.
func TestDoomLoopKey_NormalizesKeyOrderAndNumericFormatting(t *testing.T) {
	t.Parallel()
	a := map[string]any{"path": "/tmp/x", "offset": 1.0}
	b := map[string]any{"offset": float64(1), "path": "/tmp/x"}
	if doomLoopKey("read_file", a) != doomLoopKey("read_file", b) {
		t.Errorf("key-order / numeric-formatting variants should hash identically")
	}
}

// TestDoomLoopKey_DistinctValuesStayDistinct asserts the conservative
// half of the normalization tradeoff: a genuinely different argument
// value (e.g. a paginated offset advancing) must never collapse onto the
// same key, or a legitimate paginating loop would be misdetected as a
// doom loop.
func TestDoomLoopKey_DistinctValuesStayDistinct(t *testing.T) {
	t.Parallel()
	page1 := map[string]any{"offset": float64(0), "limit": float64(50)}
	page2 := map[string]any{"offset": float64(50), "limit": float64(50)}
	if doomLoopKey("list_items", page1) == doomLoopKey("list_items", page2) {
		t.Errorf("distinct offsets must not collapse onto the same doom-loop key")
	}
}

// TestDoomLoopKey_DistinguishesToolName asserts the same argument shape
// dispatched to two different tools stays distinct.
func TestDoomLoopKey_DistinguishesToolName(t *testing.T) {
	t.Parallel()
	args := map[string]any{"x": float64(1)}
	if doomLoopKey("tool_a", args) == doomLoopKey("tool_b", args) {
		t.Errorf("identical args to different tools must hash differently")
	}
}

// TestRunState_RecordToolCall_NilSafe asserts a nil *RunState degrades to
// "guard disabled" (always returns 1) rather than panicking — some
// callers (mostly tests) fire executors without a run-scoped state.
func TestRunState_RecordToolCall_NilSafe(t *testing.T) {
	t.Parallel()
	var s *RunState
	if got := s.RecordToolCall("t", map[string]any{"a": 1}); got != 1 {
		t.Errorf("nil RunState.RecordToolCall = %d, want 1", got)
	}
}
