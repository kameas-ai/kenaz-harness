package fleet

// context_pulled_locations.go — where Fleet holds each of the signed-in
// member's OWN Curated nodes (layer + team_id), persisted so it survives a
// restart.
//
// Fleet sends team_id on every node from GET /api/v1/context/pull. The
// share dialogs use it for the node THIS install pushed for a library path
// (the same wire id; ids are salted per install, so a node pushed from
// another device is a different node and is never matched here), so a
// re-share starts on the team the node is on now even when it was moved
// from the dashboard or the plugin (kenaz-fleet spec 018 H-1).
//
// <dataDir>/fleet/context_pulled_locations.json (0600, tmp+rename) holds
// the account it belongs to and, per own wire node id, layer, team_id and
// Fleet's node version. Rows are ordered by that version only (Fleet's
// monotonic counter), never by a timestamp: a device clock that runs fast
// or slow must not hide a move made on the dashboard. Teammates' nodes are not recorded at all; no
// titles, bodies or paths are stored. A tombstone, a lane change or a node
// that stops being own removes its row; an accepted push updates its row;
// the full pull at each start prunes rows it no longer sees; an account
// change or sign-out empties it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	contextpack "github.com/kameas-ai/kenaz-harness/core/context/pack"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// PulledShareLocation is where Fleet holds one of the member's own nodes.
type PulledShareLocation struct {
	NodeID      string            `json:"-"`
	OwnerUserID string            `json:"-"`
	Layer       contextpack.Layer `json:"layer"`
	TeamID      string            `json:"team_id,omitempty"`
	// Version is Fleet's node version: from the pull, or the version of
	// this install's accepted push (Fleet stores the pushed version).
	Version int `json:"version"`
}

type pulledLocationsFile struct {
	Version int                            `json:"version"`
	UserID  string                         `json:"user_id"`
	Nodes   map[string]PulledShareLocation `json:"nodes"`
}

const pulledLocationsVersion = 3

func pulledLocationsPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "context_pulled_locations.json")
}

// PulledLocationsPath is the location index file under a profile data dir
// (removed on sign-out).
func PulledLocationsPath(dataDir string) string { return pulledLocationsPath(dataDir) }

func loadPulledLocations(dataDir string) (string, map[string]PulledShareLocation) {
	out := map[string]PulledShareLocation{}
	if dataDir == "" {
		return "", out
	}
	raw, err := os.ReadFile(pulledLocationsPath(dataDir))
	if err != nil {
		return "", out
	}
	var f pulledLocationsFile
	if json.Unmarshal(raw, &f) != nil || f.Version != pulledLocationsVersion || f.UserID == "" || f.Nodes == nil {
		// Unreadable or an older shape (which held teammates' rows): drop
		// it; the next full pull rebuilds it.
		_ = os.Remove(pulledLocationsPath(dataDir))
		return "", out
	}
	return f.UserID, f.Nodes
}

// savePulledLocationsLocked persists s.locations for self, the signed-in
// account at the caller. With no signed-in account (a late write after
// sign-out) nothing is written, so a removed file is not recreated. Caller
// holds s.mu.
func (s *ContextGraphSyncer) savePulledLocationsLocked(self string) {
	if s.dataDir == "" || self == "" || !strings.EqualFold(self, s.locationsFor) {
		return
	}
	if len(s.locations) == 0 || s.locationsFor == "" {
		_ = os.Remove(pulledLocationsPath(s.dataDir))
		return
	}
	if err := os.MkdirAll(filepath.Join(s.dataDir, "fleet"), 0o700); err != nil {
		logging.L().Warn("fleet.context.pulled_locations_save_failed", "err", err.Error())
		return
	}
	b, err := json.Marshal(pulledLocationsFile{Version: pulledLocationsVersion, UserID: s.locationsFor, Nodes: s.locations})
	if err != nil {
		return
	}
	if err := atomicWriteFile(pulledLocationsPath(s.dataDir), string(b)); err != nil {
		logging.L().Warn("fleet.context.pulled_locations_save_failed", "err", err.Error())
	}
}

// adoptLocationsOwnerLocked makes the index belong to self, dropping rows
// recorded for another account. Caller holds s.mu. Reports a change.
func (s *ContextGraphSyncer) adoptLocationsOwnerLocked(self string) bool {
	if self == "" || strings.EqualFold(s.locationsFor, self) {
		return false
	}
	changed := len(s.locations) > 0
	s.locations = map[string]PulledShareLocation{}
	s.locationsFor = self
	return changed
}

// recordPulledLocationLocked updates the index for one pulled node and
// reports whether it changed. Only the signed-in member's own live nodes
// are recorded. Caller holds s.mu.
func (s *ContextGraphSyncer) recordPulledLocationLocked(n ContextPulledNode, layer contextpack.Layer, self string) bool {
	if s.locations == nil {
		s.locations = map[string]PulledShareLocation{}
	}
	_, had := s.locations[n.ID]
	own := self != "" && strings.EqualFold(n.OwnerUserID, self)
	if n.DeletedAt != nil || !own {
		if had {
			delete(s.locations, n.ID)
			return true
		}
		return false
	}
	if prev, ok := s.locations[n.ID]; ok && n.Version < prev.Version {
		return false // an older copy of the node (an accepted push is newer)
	}
	return s.putLocationLocked(n.ID, PulledShareLocation{Layer: layer, TeamID: n.TeamID, Version: n.Version})
}

func (s *ContextGraphSyncer) putLocationLocked(id string, loc PulledShareLocation) bool {
	if s.locations == nil {
		s.locations = map[string]PulledShareLocation{}
	}
	if prev, had := s.locations[id]; had && prev == loc {
		return false
	}
	s.locations[id] = loc
	return true
}

// PulledShareLocations returns where Fleet holds each of the member's own
// live Curated nodes, including nodes pulled in earlier sessions. Each row
// names the account it belongs to (OwnerUserID).
func (s *ContextGraphSyncer) PulledShareLocations() []PulledShareLocation {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PulledShareLocation, 0, len(s.locations))
	for id, l := range s.locations {
		l.NodeID, l.OwnerUserID = id, s.locationsFor
		out = append(out, l)
	}
	return out
}

// selfID is the signed-in fleet user id ("" when unknown).
func (s *ContextGraphSyncer) selfID() string {
	if s.selfUserID != nil {
		return s.selfUserID()
	}
	if s.dataDir == "" {
		return ""
	}
	id, err := LoadIdentity(s.dataDir)
	if err != nil {
		return ""
	}
	return id.UserID
}

// WithSelfUserID overrides the signed-in user id source (default: the
// enrolled identity under the data dir).
func (s *ContextGraphSyncer) WithSelfUserID(f func() string) *ContextGraphSyncer {
	s.selfUserID = f
	return s
}
