package fleet

// context_pulled_locations.go — where Fleet holds each pulled Curated node
// (layer + team_id), persisted so it survives a restart.
//
// Fleet sends team_id on every node from GET /api/v1/context/pull. The
// Curated pulled cache keeps it, but only in memory, and the pull resumes
// from a persisted cursor — so after a restart, nodes pulled in an earlier
// session are gone from the cache. The share dialogs need the server's
// answer for the member's OWN nodes (shared from another device, or the
// plugin) to start on the right team and warn before widening (kenaz-fleet
// spec 018 H-1), so this small index is kept on disk.
//
// <dataDir>/fleet/context_pulled_locations.json (0600, tmp+rename) holds,
// per wire node id: owner_user_id, layer, team_id and a SHA-256 key of the
// title (for matching an own node from another install to a local file by
// title; the title text itself is not stored). No bodies, no paths. A
// tombstone removes its row; ResetCursor (full backfill) empties it.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	contextpack "github.com/kameas-ai/kenaz-harness/core/context/pack"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// PulledShareLocation is where Fleet holds one pulled Curated node.
type PulledShareLocation struct {
	NodeID      string            `json:"-"`
	OwnerUserID string            `json:"owner_user_id"`
	Layer       contextpack.Layer `json:"layer"`
	TeamID      string            `json:"team_id,omitempty"`
	// TitleKey is CuratedTitleKey(title).
	TitleKey string `json:"title_key"`
}

type pulledLocationsFile struct {
	Version int                            `json:"version"`
	Nodes   map[string]PulledShareLocation `json:"nodes"`
}

// CuratedTitleKey is the match key for a Curated node title.
func CuratedTitleKey(title string) string {
	sum := sha256.Sum256([]byte("kenaz-curated-title\x00" + title))
	return hex.EncodeToString(sum[:])
}

func pulledLocationsPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "context_pulled_locations.json")
}

func loadPulledLocations(dataDir string) map[string]PulledShareLocation {
	out := map[string]PulledShareLocation{}
	if dataDir == "" {
		return out
	}
	raw, err := os.ReadFile(pulledLocationsPath(dataDir))
	if err != nil {
		return out
	}
	var f pulledLocationsFile
	if json.Unmarshal(raw, &f) != nil || f.Nodes == nil {
		return out // rebuilt from the next pulls; it only refines a default
	}
	return f.Nodes
}

// savePulledLocationsLocked persists s.locations. Caller holds s.mu.
func (s *ContextGraphSyncer) savePulledLocationsLocked() {
	if s.dataDir == "" {
		return
	}
	if err := os.MkdirAll(filepath.Join(s.dataDir, "fleet"), 0o700); err != nil {
		logging.L().Warn("fleet.context.pulled_locations_save_failed", "err", err.Error())
		return
	}
	b, err := json.Marshal(pulledLocationsFile{Version: 1, Nodes: s.locations})
	if err != nil {
		return
	}
	if err := atomicWriteFile(pulledLocationsPath(s.dataDir), string(b)); err != nil {
		logging.L().Warn("fleet.context.pulled_locations_save_failed", "err", err.Error())
	}
}

// recordPulledLocationLocked updates the index for one pulled node and
// reports whether it changed. Caller holds s.mu.
func (s *ContextGraphSyncer) recordPulledLocationLocked(n ContextPulledNode, layer contextpack.Layer) bool {
	if s.locations == nil {
		s.locations = map[string]PulledShareLocation{}
	}
	prev, had := s.locations[n.ID]
	if n.DeletedAt != nil {
		if had {
			delete(s.locations, n.ID)
			return true
		}
		return false
	}
	loc := PulledShareLocation{OwnerUserID: n.OwnerUserID, Layer: layer, TeamID: n.TeamID, TitleKey: CuratedTitleKey(n.Title)}
	if had && prev == loc {
		return false
	}
	s.locations[n.ID] = loc
	return true
}

// PulledShareLocations returns where Fleet holds each live pulled Curated
// node, including nodes pulled in earlier sessions.
func (s *ContextGraphSyncer) PulledShareLocations() []PulledShareLocation {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PulledShareLocation, 0, len(s.locations))
	for id, l := range s.locations {
		l.NodeID = id
		out = append(out, l)
	}
	return out
}
