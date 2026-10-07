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
//	install namespace = UUIDv5(harnessWireNamespace, "install-salt:" + hex(salt))
//	wire id           = UUIDv5(install namespace, lane + "\x00" + localID)
//
// salt is 32 random bytes minted on first use and persisted at
// <dataDir>/fleet/wire_id_salt (0600, tmp+rename). It is a SECRET of this
// install: it is never transmitted (unlike node_id.txt, which goes to fleet
// as the enroll / ?machine= id), so nobody who knows a user's paths and
// machine id can predict — and pre-squat — their wire ids.
//
// Why derivation instead of a persisted local→wire table:
//
//   - Stability. Republishing the same local id always yields the same wire
//     id, so a republish updates the existing fleet node instead of minting a
//     new one. The only input besides the local id is the salt file —
//     the same durability class as any table we would add next to it.
//     Both-files semantics: losing or regenerating node_id.txt does NOT
//     change wire ids (it is not an input); losing wire_id_salt DOES
//     re-mint every un-anchored id (Curated republishes then create new
//     fleet nodes, the old ones are orphaned). The unit lane is additionally
//     anchored by the units sync sidecar: once a unit has synced, its
//     recorded NodeID wins over derivation, so a lost salt cannot fork an
//     already-synced unit.
//   - Cross-user collisions. Local ids are NOT globally unique — two users
//     both have "guidance/style.md". Hashing the bare path would give both
//     the same UUID and fleet would reject the second user's push as "owned
//     by another user". Salting with the per-install secret makes ids
//     unique per install (the salt, not the node id). (Known limit: two fleet accounts signed in one
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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// NewWireIDs builds the per-install deriver from the install secret at
// <dataDir>/fleet/wire_id_salt (minted on first use). With an empty dataDir
// (tests) the salt is transient — stable for the life of the returned value.
func NewWireIDs(dataDir string) *WireIDs {
	salt, err := loadOrCreateWireSalt(dataDir)
	if err != nil {
		// A usable transient salt is still returned; ids derived from it
		// will not survive a restart. Loud, not fatal.
		logging.L().Warn("fleet.wire_id.salt_unpersisted", "err", err.Error())
	}
	return newWireIDsFromSalt(salt)
}

func newWireIDsFromSalt(salt []byte) *WireIDs {
	return &WireIDs{ns: uuid.NewSHA1(harnessWireNamespace, []byte("install-salt:"+hex.EncodeToString(salt)))}
}

const wireSaltBytes = 32

func wireSaltPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "wire_id_salt")
}

// loadOrCreateWireSalt reads the persisted install secret, minting and
// persisting 32 random bytes (hex, 0600, tmp+rename) when absent. An
// unreadable / malformed file is NEVER overwritten (that would silently
// re-mint every id): a transient salt is returned with the error.
func loadOrCreateWireSalt(dataDir string) ([]byte, error) {
	fresh := make([]byte, wireSaltBytes)
	if _, err := rand.Read(fresh); err != nil {
		return nil, fmt.Errorf("fleet: wire id salt: %w", err)
	}
	if dataDir == "" {
		return fresh, nil
	}
	path := wireSaltPath(dataDir)
	raw, err := os.ReadFile(path)
	if err == nil {
		salt, derr := hex.DecodeString(strings.TrimSpace(string(raw)))
		if derr != nil || len(salt) != wireSaltBytes {
			return fresh, fmt.Errorf("fleet: wire id salt %s is malformed; refusing to overwrite it", path)
		}
		return salt, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fresh, fmt.Errorf("fleet: read wire id salt: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fresh, fmt.Errorf("fleet: mkdir for wire id salt: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(hex.EncodeToString(fresh)+"\n"), 0o600); err != nil {
		return fresh, fmt.Errorf("fleet: write wire id salt: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fresh, fmt.Errorf("fleet: persist wire id salt: %w", err)
	}
	return fresh, nil
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
