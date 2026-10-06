// Package fleet — wire_id.go
//
// WireIDs maps harness-local identifiers onto the UUIDs the fleet context
// graph requires on the wire (owner ruling 2026-10-06, WP01: "the HARNESS
// sends UUIDs; local ULIDs/paths stay local, mapped in the sync layer").
//
// Fleet decodes every node / edge id as uuid.UUID (kenaz-fleet
// service/lookups_context.go ContextNodeInput.ID, ContextEdgeInput.ID) and
// `unit_node_id` with uuid.Parse (service/handlers_unit_merge.go). Before
// this file the harness sent btoa(path), ULIDs and "ctxb-…" slugs, and fleet
// refused every push with 400 invalid_request.
//
// # Design: deterministic UUIDv5, no mapping table
//
//	install namespace = UUIDv5(harnessWireNamespace, "install:" + NodeID(dataDir))
//	wire id           = UUIDv5(install namespace, lane + "\x00" + localID)
//
// Why derivation instead of a persisted local→wire table:
//
//   - Stability. Republishing the same local id always yields the same wire
//     id, so a republish updates the existing fleet node instead of minting a
//     new one. The only input besides the local id is the per-install node id
//     persisted at <dataDir>/fleet/node_id.txt — the same durability class
//     as any table we would add next to it (losing either re-mints ids).
//     The unit lane is additionally anchored by the units sync sidecar: once
//     a unit has synced, its recorded NodeID wins over derivation, so a lost
//     node_id.txt cannot fork an already-synced unit.
//   - Cross-user collisions. Local ids are NOT globally unique — two users
//     both have "guidance/style.md". Hashing the bare path would give both
//     the same UUID and fleet would reject the second user's push as "owned
//     by another user". Salting with the per-install node id makes ids
//     unique per install. (Known limit: two fleet accounts signed in one
//     after the other on the SAME profile share a namespace; the second
//     account's push of a path the first already shared is rejected
//     per-item by fleet — loud, never silent.)
//   - Cross-lane collisions. The lane tag is part of the hashed name, so a
//     unit ULID and a library path can never map to the same wire id.
//   - Hash collisions. UUIDv5 keeps 122 bits of SHA-1; accidental collision
//     across any realistic id population is negligible.
//
// Pass-through: a local id that is already a canonical UUID (a node that
// arrived via pull), or a Curated "<layer>/_fleet/<uuid>" synthetic path, is
// returned unchanged — it already IS the wire id.
package fleet

import (
	"strings"

	"github.com/google/uuid"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// harnessWireNamespace is the root UUIDv5 namespace for every id the harness
// mints for the fleet wire. NEVER change it: every derived id would change
// and every republish would fork a new fleet node.
var harnessWireNamespace = uuid.MustParse("b0c81623-2a59-4c82-9156-90f626b05553")

// WireIDLane namespaces local ids by origin so ids from different local
// stores can never collide on the wire.
type WireIDLane string

const (
	// WireLaneCurated is Knowledge › Curated: the local id is the library path.
	WireLaneCurated WireIDLane = "curated"
	// WireLaneCuratedEdge is an edge between Curated entries.
	WireLaneCuratedEdge WireIDLane = "curated-edge"
	// WireLaneUnit is the unit store: the local id is the unit ULID.
	WireLaneUnit WireIDLane = "unit"
	// WireLaneUnitEdge is a unit lineage edge: the local id is the edge id.
	WireLaneUnitEdge WireIDLane = "unit-edge"
)

// WireIDs derives fleet wire UUIDs from harness-local ids. Safe for
// concurrent use (immutable after construction).
type WireIDs struct {
	ns uuid.UUID
}

// NewWireIDs builds the per-install deriver from <dataDir>/fleet/node_id.txt
// (created on first use). With an empty dataDir (tests) the namespace is a
// transient per-process value — stable for the life of the returned value,
// which is all a test needs.
func NewWireIDs(dataDir string) *WireIDs {
	nodeID, err := NodeID(dataDir)
	if err != nil {
		// NodeID still returns a usable (transient) id on a write failure;
		// ids derived from it will not survive a restart. Loud, not fatal.
		logging.L().Warn("fleet.wire_id.node_id_unpersisted", "err", err.Error())
	}
	return newWireIDsFromInstall(nodeID)
}

func newWireIDsFromInstall(installID string) *WireIDs {
	return &WireIDs{ns: uuid.NewSHA1(harnessWireNamespace, []byte("install:"+installID))}
}

// For returns the wire UUID for localID in lane. See the file comment for
// the derivation and the pass-through rules. An empty localID returns "".
func (w *WireIDs) For(lane WireIDLane, localID string) string {
	if localID == "" {
		return ""
	}
	if id, ok := passThroughWireID(localID); ok {
		return id
	}
	return uuid.NewSHA1(w.ns, []byte(string(lane)+"\x00"+localID)).String()
}

// passThroughWireID reports whether localID already names a fleet node: a
// canonical UUID, or a Curated synthetic path "<layer>/_fleet/<uuid>" (the
// shape contexts.mergePulledEntries lists pulled nodes under).
func passThroughWireID(localID string) (string, bool) {
	if IsWireUUID(localID) {
		return strings.ToLower(localID), true
	}
	if i := strings.LastIndex(localID, "/_fleet/"); i >= 0 {
		tail := localID[i+len("/_fleet/"):]
		if IsWireUUID(tail) {
			return strings.ToLower(tail), true
		}
	}
	return "", false
}

// IsWireUUID reports whether s is a canonical 36-character UUID — the only
// id form fleet's uuid.UUID decoder and uuid.Parse accept from the harness.
// (uuid.Parse also accepts urn:/braced forms; the harness never emits them.)
func IsWireUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	_, err := uuid.Parse(s)
	return err == nil
}
