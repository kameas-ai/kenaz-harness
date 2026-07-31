package agentgraph

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
)

// doomLoopHistoryCapacity bounds the number of distinct (tool,
// normalized-arg-hash) keys tracked per run. A long-running session can
// accumulate an unbounded number of distinct tool calls; capping the
// tracked set to a bounded LRU keeps memory flat regardless of run
// length. When full, the least-recently-touched key is evicted to make
// room for a new one — losing a stale key only drops the repeat count of
// a call pattern that hasn't recurred in a while, which is exactly the
// behaviour we want: a doom loop is a *recent, sustained* thrash pattern,
// not a lifetime tally.
const doomLoopHistoryCapacity = 128

// DefaultDoomLoopThreshold is the repeat count (inclusive) that trips the
// doom-loop guard when Budget.DoomLoopThreshold is unset (zero).
const DefaultDoomLoopThreshold = 3

// toolCallHistoryEntry is the bookkeeping the LRU stores per distinct key.
type toolCallHistoryEntry struct {
	key   string
	count int
}

// toolCallHistory is a bounded LRU mapping (tool name, normalized-arg-
// hash) keys to repeat counts within a single run. It backs the
// tool-dispatch doom-loop guard (autonomy-recovery-runtime-01PMDL03
// WP02). Safe for concurrent use — the tool-dispatch executor fans calls
// out across a worker pool.
type toolCallHistory struct {
	mu       sync.Mutex
	order    *list.List
	elems    map[string]*list.Element
	capacity int
}

// newToolCallHistory returns an empty history bounded to capacity
// distinct keys. capacity <= 0 falls back to doomLoopHistoryCapacity.
func newToolCallHistory(capacity int) *toolCallHistory {
	if capacity <= 0 {
		capacity = doomLoopHistoryCapacity
	}
	return &toolCallHistory{
		order:    list.New(),
		elems:    make(map[string]*list.Element, capacity),
		capacity: capacity,
	}
}

// touch records one more occurrence of key, promotes it to
// most-recently-seen, and returns the updated repeat count (1 on first
// sighting this run). When the tracked-key set is already at capacity and
// key is new, the least-recently-seen key is evicted first so the
// history never grows unbounded.
func (h *toolCallHistory) touch(key string) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	if el, ok := h.elems[key]; ok {
		entry := el.Value.(*toolCallHistoryEntry)
		entry.count++
		h.order.MoveToFront(el)
		return entry.count
	}

	if h.order.Len() >= h.capacity {
		if oldest := h.order.Back(); oldest != nil {
			h.order.Remove(oldest)
			delete(h.elems, oldest.Value.(*toolCallHistoryEntry).key)
		}
	}

	entry := &toolCallHistoryEntry{key: key, count: 1}
	h.elems[key] = h.order.PushFront(entry)
	return 1
}

// doomLoopKey normalizes a tool call into a stable key so trivially-
// varied calls collapse onto the same repeat count while genuinely
// different calls (e.g. a paginated offset, a changing target path)
// remain distinct.
//
// Normalization strategy — deliberately conservative, because
// over-normalizing causes false positives that abort legitimate work
// (a model paginating with an incrementing offset is not a doom loop),
// while under-normalizing only delays detection by a call or two:
//
//   - args is already the decoded JSON object (map[string]any) the
//     tool-dispatch executor built from the model's raw argument
//     string. Re-encoding it with encoding/json canonicalizes key order
//     (Marshal always sorts map keys, at every nesting depth) and
//     insignificant numeric formatting (decoding JSON numbers into `any`
//     collapses them to float64, so "1", "1.0", and "1.00" all
//     serialize back out identically). Structural whitespace
//     (indentation, spacing) never survives the decode in the first
//     place.
//   - String content is never trimmed, case-folded, or otherwise
//     rewritten. A value with meaningful embedded whitespace (e.g. a
//     search query) — or a value that differs by one character (e.g. an
//     incrementing offset encoded as a string) — stays a distinct key.
//     This is the conservative half of the tradeoff: prefer
//     under-detecting a doom loop to false-flagging varied work.
func doomLoopKey(toolName string, args map[string]any) string {
	canon, err := json.Marshal(args)
	if err != nil {
		// Should not happen for a map[string]any built by
		// parseToolArgs, but degrade to a stable-enough representation
		// rather than skipping detection outright.
		canon = []byte(fmt.Sprintf("%v", args))
	}
	sum := sha256.Sum256(append([]byte(toolName+"\x00"), canon...))
	return toolName + ":" + hex.EncodeToString(sum[:])
}
