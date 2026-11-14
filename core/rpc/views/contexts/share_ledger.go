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

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// shareRecord is where one entry was last shared from this device.
type shareRecord struct {
	UserID   string    `json:"user_id"`
	Layer    string    `json:"layer"`
	TeamID   string    `json:"team_id,omitempty"`
	SharedAt time.Time `json:"shared_at"`
}

type shareLedgerFile struct {
	Version int                    `json:"version"`
	Shares  map[string]shareRecord `json:"shares"`
}

// shareLedger is the on-disk share record. Safe for concurrent use. With
// an empty path it is in-memory only (tests, no data dir).
type shareLedger struct {
	mu     sync.Mutex
	path   string
	loaded bool
	recs   map[string]shareRecord
}

func newShareLedger(path string) *shareLedger {
	return &shareLedger{path: path}
}

// shareLedgerPath is the ledger file under a profile data dir.
func shareLedgerPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "context_shares.json")
}

// loadLocked reads the file once. A missing file is an empty ledger. A
// malformed one is treated as empty and moved aside to
// <file>.corrupt-<unix-nanos> so the next share does not overwrite what
// may still be recoverable by hand.
func (l *shareLedger) loadLocked() {
	if l.loaded {
		return
	}
	l.loaded = true
	l.recs = map[string]shareRecord{}
	if l.path == "" {
		return
	}
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
		}
		return
	}
	if f.Shares != nil {
		l.recs = f.Shares
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
	if l.path == "" {
		return nil
	}
	data, err := json.Marshal(shareLedgerFile{Version: 1, Shares: l.recs})
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
	return nil
}
