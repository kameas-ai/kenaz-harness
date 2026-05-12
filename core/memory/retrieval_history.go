// Package memory — retrieval history ring buffer (§2.1 active-session
// retrieval inspector, memory-inspection-ui-01KX5R8E WP02).
//
// The retriever pushes a RetrievalRecord after every successful call;
// the RPC surface reads the most recent record for a session to power
// the "Last turn retrieval" inspector panel.
//
// Design constraints:
//   - Bounded: per-session cap 200, global cap 10k (oldest entry pruned first).
//   - Process-scoped singleton via GlobalRetrievalHistory(); never persisted.
//   - Concurrency-safe: single RWMutex guards all state.
package memory

import (
	"sync"
	"time"
)

const (
	// HistoryPerSessionCap is the maximum number of RetrievalRecord entries
	// kept per session-ID key in the ring buffer.
	HistoryPerSessionCap = 200
	// HistoryGlobalCap is the hard upper bound on total RetrievalRecord rows
	// across all sessions. When reached, the oldest entry (by any session) is
	// evicted before the new one is inserted.
	HistoryGlobalCap = 10_000
)

// RetrievalRecord captures the outcome of one Retriever.retrieve call.
// It is the wire-agnostic internal shape; the rpc layer projects it into
// the frontend-visible RetrievalReport.
type RetrievalRecord struct {
	SessionID string
	Query     string
	// Results holds ALL k-NN hits, sorted by similarity descending.
	// The rpc layer partitions them into "injected" vs "below threshold"
	// based on the retriever's configured threshold.
	Results   []RetrievalResult
	Threshold float32
	At        time.Time
}

// RetrievalResult is one ranked chunk from a retrieval call.
type RetrievalResult struct {
	ChunkID   string
	Content   string
	Kind      string
	Pinned    bool
	Similarity float32
	Injected  bool // true when similarity >= threshold
}

// sessionEntry is the per-session slice of records in insertion order.
type sessionEntry struct {
	records []RetrievalRecord
	// oldest tracks the absolute insertion time of records[0] for global
	// eviction (LRU across sessions).
	oldest time.Time
}

// RetrievalHistory is the process-scoped ring buffer.
type RetrievalHistory struct {
	mu       sync.RWMutex
	bySess   map[string]*sessionEntry
	// total is len(all records across all sessions); used to enforce global cap.
	total int
}

var globalHistory = &RetrievalHistory{
	bySess: make(map[string]*sessionEntry),
}

// GlobalRetrievalHistory returns the process-scoped singleton.
func GlobalRetrievalHistory() *RetrievalHistory {
	return globalHistory
}

// Push records a retrieval call into the ring buffer.
// Enforces per-session and global caps, evicting oldest entries as needed.
func (h *RetrievalHistory) Push(rec RetrievalRecord) {
	if rec.SessionID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	// Enforce global cap: evict the oldest single entry across any session.
	if h.total >= HistoryGlobalCap {
		h.evictOldestLocked()
	}

	se := h.bySess[rec.SessionID]
	if se == nil {
		se = &sessionEntry{}
		h.bySess[rec.SessionID] = se
	}

	// Enforce per-session cap.
	if len(se.records) >= HistoryPerSessionCap {
		se.records = se.records[1:]
		h.total--
	}

	se.records = append(se.records, rec)
	h.total++
	if len(se.records) == 1 {
		se.oldest = rec.At
	}
}

// evictOldestLocked removes the single oldest record across all sessions.
// Must be called with h.mu held for writing.
func (h *RetrievalHistory) evictOldestLocked() {
	var (
		oldestSessID string
		oldestTime   time.Time
	)
	first := true
	for sid, se := range h.bySess {
		if len(se.records) == 0 {
			continue
		}
		t := se.records[0].At
		if first || t.Before(oldestTime) {
			oldestTime = t
			oldestSessID = sid
			first = false
		}
	}
	if oldestSessID == "" {
		return
	}
	se := h.bySess[oldestSessID]
	se.records = se.records[1:]
	h.total--
	if len(se.records) == 0 {
		delete(h.bySess, oldestSessID)
	} else {
		se.oldest = se.records[0].At
	}
}

// Last returns the most recent RetrievalRecord for the given session,
// and true when found. Returns zero-value, false when the session has
// no history.
func (h *RetrievalHistory) Last(sessionID string) (RetrievalRecord, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	se := h.bySess[sessionID]
	if se == nil || len(se.records) == 0 {
		return RetrievalRecord{}, false
	}
	return se.records[len(se.records)-1], true
}

// Total returns the current total number of records across all sessions.
// Used in tests.
func (h *RetrievalHistory) Total() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.total
}
