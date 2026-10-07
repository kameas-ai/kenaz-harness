// Hybrid logical clock for learned-memory sync (memory-sync-01MEMSY01 WP02,
// fleet contract docs/contract-harness-memory.md §2).
//
// Wire format (fixed by Fleet): "<16-digit zero-padded unix ms>:<6-digit
// counter>:<device_id>". Plain string comparison is the total order, so
// every component is fixed-width and the device id is the tiebreak.
//
// One HLC exists per install/profile. Its node id is the fleet node id
// (core/fleet.NodeID) — INJECTED by the rpc wiring, because core/memory must
// not import core/fleet (the MemoryWriteGate precedent). The same value is
// the push device_id, so recall G-counter keys and HLC node ids agree.
//
// Persistence: <dataDir>/fleet/memory_hlc.json, {"wall_ms","counter"},
// written tmp+rename on every Tick/Observe (spec OQ-4: no batching — only
// title/pin/scope/create/forget tick, never a recall).
package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// HLCMaxCounter is the largest counter the 6-digit wire field can carry.
// A tick that would exceed it advances the wall part by 1 ms instead, so
// the order stays strictly monotone without widening the format.
const HLCMaxCounter = 999999

// HLCMaxSkew mirrors Fleet's acceptance bound: a field whose wall part is
// more than this far ahead of server time is rejected `clock_in_future`.
const HLCMaxSkew = 5 * time.Minute

var hlcRe = regexp.MustCompile(`^(\d{16}):(\d{6}):([A-Za-z0-9._-]{1,64})$`)

// ErrInvalidHLCNode is returned by NewHLC when the node id would not fit
// Fleet's device_id grammar [A-Za-z0-9._-]{1,64}.
var ErrInvalidHLCNode = errors.New("memory: hlc node id must match [A-Za-z0-9._-]{1,64}")

var nodeRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// FormatHLC renders the wire form.
func FormatHLC(wallMS int64, counter int, node string) string {
	return fmt.Sprintf("%016d:%06d:%s", wallMS, counter, node)
}

// ParseHLC splits a wire HLC. ok=false for anything malformed.
func ParseHLC(s string) (wallMS int64, counter int, node string, ok bool) {
	m := hlcRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, "", false
	}
	w, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, 0, "", false
	}
	c, err := strconv.Atoi(m[2])
	if err != nil {
		return 0, 0, "", false
	}
	return w, c, m[3], true
}

// HLCAfter reports whether a sorts strictly after b. An empty HLC (a legacy
// "unstamped" field) sorts before every stamped value.
func HLCAfter(a, b string) bool { return a > b }

// HLC is the per-install hybrid logical clock. Safe for concurrent use.
type HLC struct {
	mu      sync.Mutex
	node    string
	path    string // "" = not persisted (tests)
	now     func() time.Time
	wallMS  int64
	counter int
	// saveErr is the last persistence failure (nil after a good save). The
	// clock stays monotone in-process either way; the error is surfaced so
	// the sync lane can report it instead of a silent regression on restart.
	saveErr error
}

type hlcState struct {
	WallMS  int64 `json:"wall_ms"`
	Counter int   `json:"counter"`
}

// HLCStatePath is the canonical state-file location under dataDir.
func HLCStatePath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "memory_hlc.json")
}

// NewHLC builds a clock for node, restoring persisted state from path
// (empty path = in-memory only). A missing state file is a fresh clock; a
// corrupt one is an error — silently restarting at zero could hand out
// HLCs that sort BEFORE ones this install already pushed, so a lost edit
// would be indistinguishable from an old one.
func NewHLC(node, path string) (*HLC, error) {
	if !nodeRe.MatchString(node) {
		return nil, ErrInvalidHLCNode
	}
	h := &HLC{node: node, path: path, now: time.Now}
	if path == "" {
		return h, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return h, nil
		}
		return nil, fmt.Errorf("memory: read hlc state: %w", err)
	}
	var st hlcState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("memory: decode hlc state %s: %w", path, err)
	}
	if st.WallMS < 0 || st.Counter < 0 || st.Counter > HLCMaxCounter {
		return nil, fmt.Errorf("memory: hlc state %s out of range", path)
	}
	h.wallMS, h.counter = st.WallMS, st.Counter
	return h, nil
}

// SetNow overrides the wall clock (tests).
func (h *HLC) SetNow(fn func() time.Time) {
	h.mu.Lock()
	h.now = fn
	h.mu.Unlock()
}

// NodeID returns the injected node id (= the push device_id).
func (h *HLC) NodeID() string {
	if h == nil {
		return ""
	}
	return h.node
}

// Tick advances the clock for a local mutation and returns the new HLC.
// wall = max(now, last); same wall ⇒ counter+1, else counter=0. A wall
// clock stepped backwards (NTP) cannot regress the order: max() keeps the
// last wall and the counter absorbs the ordering.
func (h *HLC) Tick() string {
	if h == nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now().UnixMilli()
	if now > h.wallMS {
		h.wallMS, h.counter = now, 0
	} else {
		h.bumpLocked()
	}
	h.saveLocked()
	return FormatHLC(h.wallMS, h.counter, h.node)
}

// Observe applies the HLC receive rule to a remote stamp seen on pull, so
// the next local Tick sorts after everything this device has seen. A
// malformed or empty remote is ignored. It never lowers the wall below
// local now: a device whose OS clock is fast stays fast (Fleet rejects its
// writes `clock_in_future`) until the OS clock is fixed.
func (h *HLC) Observe(remote string) {
	if h == nil {
		return
	}
	rw, rc, _, ok := ParseHLC(remote)
	if !ok {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now().UnixMilli()
	last, lc := h.wallMS, h.counter
	wall := max(last, rw, now)
	switch {
	case wall == last && wall == rw:
		h.wallMS, h.counter = wall, max(lc, rc)
		h.bumpLocked()
	case wall == last:
		h.bumpLocked()
	case wall == rw:
		h.wallMS, h.counter = wall, rc
		h.bumpLocked()
	default:
		h.wallMS, h.counter = wall, 0
	}
	h.saveLocked()
}

// Snapshot returns the current (wall_ms, counter) without ticking.
func (h *HLC) Snapshot() (wallMS int64, counter int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.wallMS, h.counter
}

// SaveErr is the last persistence error (nil after a successful save).
func (h *HLC) SaveErr() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.saveErr
}

// AheadOf reports how far this clock's wall part is ahead of ref (0 when it
// is not ahead). The sync lane compares against Fleet's Date header to name
// a fast OS clock as the cause of clock_in_future rejections.
func (h *HLC) AheadOf(ref time.Time) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := time.Duration(h.wallMS-ref.UnixMilli()) * time.Millisecond
	if d < 0 {
		return 0
	}
	return d
}

func (h *HLC) bumpLocked() {
	h.counter++
	if h.counter > HLCMaxCounter {
		h.wallMS++
		h.counter = 0
	}
}

func (h *HLC) saveLocked() {
	if h.path == "" {
		return
	}
	h.saveErr = writeFileAtomic(h.path, mustJSON(hlcState{WallMS: h.wallMS, Counter: h.counter}))
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // only fixed-shape structs reach here
	}
	return b
}

// writeFileAtomic writes data to path via tmp+rename, mode 0600, creating
// the parent directory (0700).
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("memory: mkdir %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("memory: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("memory: rename %s: %w", path, err)
	}
	return nil
}
