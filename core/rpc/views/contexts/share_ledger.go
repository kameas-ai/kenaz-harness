package contexts

// share_ledger.go — where this device last shared each Curated entry
// (kenaz-fleet spec 018 H-1 review, finding 1).
//
// Fleet's context push upserts on the stable wire node id and overwrites
// team_id with the pushed one. Without a record of where an entry already
// lives, a re-share (or a folder share that includes it) started on the
// enrolled team — the org root — and one click moved a file shared with
// Backend to the whole organisation. The share dialogs now start on the
// team recorded here and warn when the chosen audience differs.
//
// The record is local, per profile, at <dataDir>/fleet/context_shares.json
// (0600, tmp+rename). It is keyed by WIRE node id, never by library path
// (a path is user content), and each record names the fleet user that
// made it so another account signed in on the same profile never inherits
// it. Nothing here is sent anywhere or put on telemetry.
//
// Spec 018 H-3: only a share Fleet reports `published` is recorded in
// Shares. An org-wide share that Fleet opened as a merge request is kept
// apart in Pending, by its merge_request_id: Fleet answers the same id for
// the same content and a new one when the content changes (the old request
// is then superseded), so the record is replaced and names the id it
// superseded. A later `published` (or a `previously_rejected` refusal)
// clears it.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	fleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// shareRecord is where one entry was last shared from this device.
type shareRecord struct {
	UserID   string    `json:"user_id"`
	Layer    string    `json:"layer"`
	TeamID   string    `json:"team_id,omitempty"`
	SharedAt time.Time `json:"shared_at"`
	// Version is Fleet's node version this record matches (the accepted
	// push's version, or the pulled row that corrected it). Compared with
	// Fleet's row by version only, never by time. 0 = unknown (older
	// records): Fleet's row wins.
	Version int `json:"version,omitempty"`
}

// pendingRecord is an org-wide share waiting for an admin's review.
type pendingRecord struct {
	UserID         string `json:"user_id"`
	MergeRequestID string `json:"merge_request_id"`
	// SupersededID is the request this one replaced (content changed).
	SupersededID string    `json:"superseded_id,omitempty"`
	RequestedAt  time.Time `json:"requested_at"`
}

type shareLedgerFile struct {
	Version int                      `json:"version"`
	Shares  map[string]shareRecord   `json:"shares"`
	Pending map[string]pendingRecord `json:"pending,omitempty"`
}

// shareLedger is the on-disk share record. Safe for concurrent use. With
// an empty path it is in-memory only (tests, no data dir).
type shareLedger struct {
	mu     sync.Mutex
	path   string
	loaded bool
	recs   map[string]shareRecord
	// pending holds org-wide shares waiting for review, by wire id.
	pending map[string]pendingRecord
	// stamp is the file's size and mtime when recs was read; a different
	// stamp (rewritten, or removed on sign-out) reloads.
	stamp string
}

func newShareLedger(path string) *shareLedger {
	return &shareLedger{path: path}
}

// shareLedgerPath is the ledger file under a profile data dir.
func shareLedgerPath(dataDir string) string {
	return fleet.ContextSharesPath(dataDir)
}

func fileStamp(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
}

// loadLocked reads the file, again whenever it changed on disk (so a
// sign-out that removes it really forgets it). A missing file is an empty
// ledger. A
// malformed one is treated as empty and moved aside to
// <file>.corrupt-<unix-nanos> so the next share does not overwrite what
// may still be recoverable by hand.
func (l *shareLedger) loadLocked() {
	if l.path == "" {
		if !l.loaded {
			l.loaded, l.recs, l.pending = true, map[string]shareRecord{}, map[string]pendingRecord{}
		}
		return
	}
	stamp := fileStamp(l.path)
	if l.loaded && stamp == l.stamp {
		return
	}
	l.loaded, l.stamp = true, stamp
	l.recs = map[string]shareRecord{}
	l.pending = map[string]pendingRecord{}
	raw, err := os.ReadFile(l.path)
	if err != nil {
		return
	}
	var f shareLedgerFile
	if json.Unmarshal(raw, &f) != nil {
		aside := fmt.Sprintf("%s.corrupt-%d", l.path, time.Now().UnixNano())
		if rerr := os.Rename(l.path, aside); rerr != nil {
			logging.L().Warn("contexts.share_ledger.corrupt_move_failed", "err", rerr.Error())
		} else {
			logging.L().Warn("contexts.share_ledger.corrupt_moved_aside", "file", filepath.Base(aside))
			l.stamp = ""
		}
		return
	}
	if f.Shares != nil {
		l.recs = f.Shares
	}
	if f.Pending != nil {
		l.pending = f.Pending
	}
}

// get returns the record for wireID made by userID.
func (l *shareLedger) get(wireID, userID string) (shareRecord, bool) {
	if l == nil || wireID == "" {
		return shareRecord{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadLocked()
	r, ok := l.recs[wireID]
	if !ok || userID == "" || r.UserID != userID {
		return shareRecord{}, false
	}
	return r, true
}

// put records a share and persists the ledger.
func (l *shareLedger) put(wireID string, r shareRecord) error {
	if l == nil || wireID == "" || r.UserID == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadLocked()
	l.recs[wireID] = r
	return l.persistLocked()
}

// getPending returns the pending review record for wireID made by userID.
func (l *shareLedger) getPending(wireID, userID string) (pendingRecord, bool) {
	if l == nil || wireID == "" {
		return pendingRecord{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadLocked()
	r, ok := l.pending[wireID]
	if !ok || userID == "" || r.UserID != userID {
		return pendingRecord{}, false
	}
	return r, true
}

// putPending records (or replaces) a pending review for wireID. A different
// merge request id than the stored one means Fleet superseded the old
// request; the new record names it.
func (l *shareLedger) putPending(wireID, userID, mergeRequestID string, now time.Time) error {
	if l == nil || wireID == "" || userID == "" || mergeRequestID == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadLocked()
	prev, had := l.pending[wireID]
	if had && prev.UserID == userID && prev.MergeRequestID == mergeRequestID {
		return nil // the same request: Fleet changed nothing
	}
	rec := pendingRecord{UserID: userID, MergeRequestID: mergeRequestID, RequestedAt: now}
	if had && prev.UserID == userID {
		rec.SupersededID = prev.MergeRequestID
	}
	l.pending[wireID] = rec
	return l.persistLocked()
}

// clearPending forgets the pending review for wireID.
func (l *shareLedger) clearPending(wireID string) error {
	if l == nil || wireID == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loadLocked()
	if _, had := l.pending[wireID]; !had {
		return nil
	}
	delete(l.pending, wireID)
	return l.persistLocked()
}

// persistLocked writes the ledger (tmp+rename, 0600). Caller holds l.mu.
func (l *shareLedger) persistLocked() error {
	if l.path == "" {
		return nil
	}
	data, err := json.Marshal(shareLedgerFile{Version: 1, Shares: l.recs, Pending: l.pending})
	if err != nil {
		return fmt.Errorf("contexts: share ledger encode: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return fmt.Errorf("contexts: share ledger mkdir: %w", err)
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("contexts: share ledger write: %w", err)
	}
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("contexts: share ledger persist: %w", err)
	}
	l.stamp = fileStamp(l.path)
	return nil
}
