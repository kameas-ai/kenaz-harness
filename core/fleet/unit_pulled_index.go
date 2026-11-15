package fleet

// unit_pulled_index.go — what Fleet last said about each Unit-lane node:
// its team, its audience labels and its author (kenaz-fleet spec 018 H-2).
//
// The units store keeps no team, audience or author, and its sync sidecar
// is SQL with migrations of its own. This small index lets the Unit lane
//
//   - drop the copies of the teams a reset names (contract §2 "Loss of
//     access"), keeping the member's OWN units as personal, as a tombstone
//     of their own node does;
//   - label each shared unit's audience for the model framing and the UI.
//
// <dataDir>/fleet/unit_pulled_index.json (0600, tmp+rename), keyed by wire
// node id. It holds ids and labels only, never titles or bodies. Spec 018
// rule: none of it reaches telemetry. A row is written on every pull of a
// live node and on every accepted push, and removed with its unit.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// UnitNodeLabels is what Fleet last served for one Unit-lane node.
type UnitNodeLabels struct {
	TeamID      string `json:"team_id,omitempty"`
	Audience    string `json:"audience,omitempty"`
	Governed    bool   `json:"governed,omitempty"`
	OwnerUserID string `json:"owner_user_id,omitempty"`
	// PulledBy is the signed-in fleet user the row was recorded for. A
	// unit recorded for another account is never removed by this one's
	// reset (several accounts can sign in on one profile).
	PulledBy string `json:"pulled_by,omitempty"`
}

type unitIndexFile struct {
	Version int                       `json:"version"`
	Nodes   map[string]UnitNodeLabels `json:"nodes"`
}

const unitIndexVersion = 1

// unitIndexPath is the index file under a profile data dir. It lives as
// long as the units it describes (the units store is not cleared on
// sign-out either).
func unitIndexPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "unit_pulled_index.json")
}

// unitIndex is the in-memory index with write-through persistence. Safe
// for concurrent use. An empty dataDir keeps it in memory only.
type unitIndex struct {
	mu      sync.Mutex
	dataDir string
	loaded  bool
	dirty   bool
	nodes   map[string]UnitNodeLabels
}

func newUnitIndex(dataDir string) *unitIndex { return &unitIndex{dataDir: dataDir} }

func (x *unitIndex) loadLocked() {
	if x.loaded {
		return
	}
	x.loaded = true
	x.nodes = map[string]UnitNodeLabels{}
	if x.dataDir == "" {
		return
	}
	raw, err := os.ReadFile(unitIndexPath(x.dataDir))
	if err != nil {
		return
	}
	var f unitIndexFile
	if json.Unmarshal(raw, &f) != nil || f.Version != unitIndexVersion || f.Nodes == nil {
		_ = os.Remove(unitIndexPath(x.dataDir)) // rebuilt by the next pull
		return
	}
	x.nodes = f.Nodes
}

func (x *unitIndex) saveLocked() {
	if x.dataDir == "" {
		return
	}
	if len(x.nodes) == 0 {
		_ = os.Remove(unitIndexPath(x.dataDir))
		return
	}
	if err := os.MkdirAll(filepath.Join(x.dataDir, "fleet"), 0o700); err != nil {
		logging.L().Warn("fleet.unit.index_save_failed", "err", err.Error())
		return
	}
	b, err := json.Marshal(unitIndexFile{Version: unitIndexVersion, Nodes: x.nodes})
	if err != nil {
		return
	}
	if err := atomicWriteFile(unitIndexPath(x.dataDir), string(b)); err != nil {
		logging.L().Warn("fleet.unit.index_save_failed", "err", err.Error())
	}
}

func (x *unitIndex) get(nodeID string) (UnitNodeLabels, bool) {
	if x == nil || nodeID == "" {
		return UnitNodeLabels{}, false
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.loadLocked()
	l, ok := x.nodes[nodeID]
	return l, ok
}

// put records labels for nodeID. An empty field keeps the stored one only
// for OwnerUserID (a push knows the author but no audience; a withdrawn row
// never carries an author).
func (x *unitIndex) put(nodeID string, l UnitNodeLabels) {
	if x == nil || nodeID == "" {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.loadLocked()
	prev, had := x.nodes[nodeID]
	if l.OwnerUserID == "" {
		l.OwnerUserID = prev.OwnerUserID
	}
	if l.PulledBy == "" {
		l.PulledBy = prev.PulledBy
	}
	if had && prev == l {
		return
	}
	x.nodes[nodeID] = l
	x.dirty = true
}

func (x *unitIndex) remove(nodeID string) {
	if x == nil || nodeID == "" {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.loadLocked()
	if _, had := x.nodes[nodeID]; !had {
		return
	}
	delete(x.nodes, nodeID)
	x.dirty = true
}

// flush persists pending changes (one write per page or push, not per node).
func (x *unitIndex) flush() {
	if x == nil {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.dirty {
		return
	}
	x.dirty = false
	x.saveLocked()
}
