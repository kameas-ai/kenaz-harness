package narrative

import (
	"context"
	"sync"
	"time"
	"unicode/utf8"
)

// citationMinBytes is the minimum stable substring length that counts
// as a citation match (FR-008c). 50 bytes chosen to avoid false-positive
// matches on common short phrases.
const citationMinBytes = 50

// citationWindowCap is the maximum number of recently-retrieved chunk
// IDs the detector tracks per session. Older entries are evicted in
// FIFO order.
const citationWindowCap = 20

// RetrievalEntry is one entry in the per-session recent-retrieval ring
// buffer. The detector matches the assistant text against all entries
// in the window.
type RetrievalEntry struct {
	ChunkID string
	// Fingerprints is the set of 50-byte stable substrings extracted
	// from Content. Using string hashes keeps memory bounded; we store
	// the actual substrings (not hashes) so the match is exact.
	Fingerprints []string
}

// extractFingerprints returns every non-overlapping 50-byte UTF-8-safe
// substring from s as a set of candidate citation strings. Overlap
// stride = 25 bytes so dense text produces multiple candidates.
func extractFingerprints(s string) []string {
	const stride = 25
	var out []string
	seen := make(map[string]struct{})
	for start := 0; start+citationMinBytes <= len(s); start += stride {
		end := start + citationMinBytes
		// Snap end back to a rune boundary.
		for end > start && !utf8.ValidString(s[start:end]) {
			end--
		}
		fp := s[start:end]
		if len(fp) < citationMinBytes {
			continue
		}
		if _, already := seen[fp]; !already {
			seen[fp] = struct{}{}
			out = append(out, fp)
		}
	}
	return out
}

// citedSet tracks which (chunkID, turnID) pairs have been counted in
// the current turn to prevent double-counting (FR-008c).
type citedSet struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func newCitedSet() *citedSet {
	return &citedSet{seen: make(map[string]struct{})}
}

func (c *citedSet) tryAdd(chunkID, turnID string) bool {
	key := chunkID + "\x00" + turnID
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.seen[key]; exists {
		return false
	}
	c.seen[key] = struct{}{}
	return true
}

// CitationDetector maintains a per-session sliding window of recently
// retrieved chunks and detects when an assistant response cites them.
// It is safe for concurrent use.
type CitationDetector struct {
	mu      sync.Mutex
	window  []RetrievalEntry // ring buffer, max citationWindowCap entries
	metrics MetricsStore
	// cited tracks (chunkID, turnID) pairs already counted to prevent
	// double-counting across multiple DetectCitations calls for the same turn.
	cited *citedSet
}

// NewCitationDetector constructs a detector backed by metrics.
func NewCitationDetector(metrics MetricsStore) *CitationDetector {
	return &CitationDetector{
		metrics: metrics,
		window:  make([]RetrievalEntry, 0, citationWindowCap),
		cited:   newCitedSet(),
	}
}

// PushRetrieved adds a newly retrieved chunk to the window. When the
// window is full the oldest entry is evicted.
func (d *CitationDetector) PushRetrieved(chunkID, content string) {
	if chunkID == "" || len(content) < citationMinBytes {
		return
	}
	fps := extractFingerprints(content)
	if len(fps) == 0 {
		return
	}
	entry := RetrievalEntry{ChunkID: chunkID, Fingerprints: fps}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.window = append(d.window, entry)
	if len(d.window) > citationWindowCap {
		d.window = d.window[1:]
	}
}

// DetectCitations checks assistantText for verbatim substrings from
// recently-retrieved chunks and increments the citation counter for
// each match. Deduplicates per (chunkID, turnID). < 2ms at p95 for
// typical window sizes.
func (d *CitationDetector) DetectCitations(ctx context.Context, assistantText, turnID string, now time.Time) {
	if !Enabled() {
		return
	}
	if len(assistantText) < citationMinBytes || turnID == "" {
		return
	}
	d.mu.Lock()
	window := append([]RetrievalEntry(nil), d.window...) // snapshot
	d.mu.Unlock()

	for _, entry := range window {
		for _, fp := range entry.Fingerprints {
			// Simple substring containment — no regex, no hash.
			if containsSubstring(assistantText, fp) {
				if d.cited.tryAdd(entry.ChunkID, turnID) {
					_ = d.metrics.IncrementCitations(ctx, entry.ChunkID, now)
				}
				// One match per chunk per turn is enough; move to next entry.
				break
			}
		}
	}
}

// containsSubstring returns true when haystack contains needle as a
// verbatim substring. Uses a simple linear scan sufficient for typical
// memory-window sizes.
func containsSubstring(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	if len(haystack) < len(needle) {
		return false
	}
	// Boyer-Moore-Horspool would be faster at large scales; for a 20-entry
	// window of 50-byte fingerprints the naive scan is < 1ms.
	needleLen := len(needle)
	for i := 0; i <= len(haystack)-needleLen; i++ {
		if haystack[i:i+needleLen] == needle {
			return true
		}
	}
	return false
}

// Clear empties the recent-retrieval window. Called when a session ends
// to free memory.
func (d *CitationDetector) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.window = d.window[:0]
}
