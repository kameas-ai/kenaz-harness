package narrative

import (
	"context"
	"strings"
	"testing"
	"time"
)

// enableNarrativeForTest sets the env var so Enabled() returns true
// for the duration of the test. Uses t.Setenv which marks the test as
// non-parallel-compatible (Go's race detector will enforce it).
func enableNarrativeForTest(t *testing.T) {
	t.Helper()
	t.Setenv("HARNESS_MEMORY_NARRATIVE_LAYER", "on")
}

func TestCitationDetector_BasicMatch(t *testing.T) {
	enableNarrativeForTest(t)

	store := NewMemMetricsStore()
	det := NewCitationDetector(store)
	ctx := context.Background()
	now := time.Now()

	// Push a chunk with enough content to produce fingerprints.
	content := strings.Repeat("the quick brown fox jumps over the lazy dog ", 5)
	det.PushRetrieved("chunk-fox", content)

	// Assistant text quotes the first 60 chars — above citationMinBytes.
	assistantText := content[:60] + " and then some more text"
	det.DetectCitations(ctx, assistantText, "turn-1", now)

	m, err := store.Get(ctx, "chunk-fox")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.Citations == 0 {
		t.Error("expected Citations > 0 after verbatim match")
	}
}

func TestCitationDetector_NoMatch(t *testing.T) {
	enableNarrativeForTest(t)

	store := NewMemMetricsStore()
	det := NewCitationDetector(store)
	ctx := context.Background()
	now := time.Now()

	det.PushRetrieved("chunk-a", strings.Repeat("x", 100))
	// Assistant text is completely different.
	det.DetectCitations(ctx, strings.Repeat("y", 100), "turn-2", now)

	m, _ := store.Get(ctx, "chunk-a")
	if m.Citations != 0 {
		t.Errorf("Citations = %d, want 0 for non-matching text", m.Citations)
	}
}

func TestCitationDetector_Dedup(t *testing.T) {
	enableNarrativeForTest(t)

	store := NewMemMetricsStore()
	det := NewCitationDetector(store)
	ctx := context.Background()
	now := time.Now()

	content := strings.Repeat("narrative layer content: ", 10)
	det.PushRetrieved("chunk-dd", content)

	assistantText := content[:80] + " extra words "
	// Call twice with same turn ID — should only increment citations once.
	det.DetectCitations(ctx, assistantText, "turn-dedup", now)
	det.DetectCitations(ctx, assistantText, "turn-dedup", now)

	m, _ := store.Get(ctx, "chunk-dd")
	if m.Citations != 1 {
		t.Errorf("Citations = %d after two calls same turn, want 1 (dedup)", m.Citations)
	}
}

func TestCitationDetector_WindowCap(t *testing.T) {
	enableNarrativeForTest(t)

	store := NewMemMetricsStore()
	det := NewCitationDetector(store)

	// Push citationWindowCap+5 entries; oldest should be evicted.
	for i := 0; i < citationWindowCap+5; i++ {
		det.PushRetrieved("chunk-"+string(rune('a'+i%26)), strings.Repeat("z", 100))
	}
	det.mu.Lock()
	wLen := len(det.window)
	det.mu.Unlock()
	if wLen > citationWindowCap {
		t.Errorf("window len = %d, want ≤ %d", wLen, citationWindowCap)
	}
}

func TestCitationDetector_Disabled(t *testing.T) {
	t.Setenv("HARNESS_MEMORY_NARRATIVE_LAYER", "off")

	store := NewMemMetricsStore()
	det := NewCitationDetector(store)
	ctx := context.Background()
	now := time.Now()

	content := strings.Repeat("secret content ", 10)
	det.PushRetrieved("chunk-dis", content)
	det.DetectCitations(ctx, content[:60]+" more", "turn-dis", now)

	m, _ := store.Get(ctx, "chunk-dis")
	if m.Citations != 0 {
		t.Errorf("Citations = %d when disabled, want 0", m.Citations)
	}
}

func TestExtractFingerprints_UTF8(t *testing.T) {
	t.Parallel()
	// Multi-byte runes should not be split.
	s := strings.Repeat("€", 30) // 30 × 3 bytes = 90 bytes total
	fps := extractFingerprints(s)
	for _, fp := range fps {
		if !isValidUTF8(fp) {
			t.Errorf("fingerprint is not valid UTF-8: %q", fp)
		}
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

func TestContainsSubstring(t *testing.T) {
	t.Parallel()
	cases := []struct {
		haystack, needle string
		want             bool
	}{
		{"hello world", "world", true},
		{"hello world", "xyz", false},
		{"", "a", false},
		{"a", "", true},
		{"abc", "abc", true},
		{"abc", "abcd", false},
	}
	for _, tc := range cases {
		got := containsSubstring(tc.haystack, tc.needle)
		if got != tc.want {
			t.Errorf("containsSubstring(%q, %q) = %v, want %v", tc.haystack, tc.needle, got, tc.want)
		}
	}
}
