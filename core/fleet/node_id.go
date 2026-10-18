package fleet

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	nodeIDOnce  sync.Once
	cachedNodeID string
)

// nodeIDFilePath returns the path to the persistent node-id file.
func nodeIDFilePath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "node_id.txt")
}

// NodeID returns a stable per-install node identifier stored in
// <dataDir>/fleet/node_id.txt. If the file does not exist it is created
// atomically with a newly-generated ULID-like value. The result is
// memoized in-process.
//
// When dataDir is empty a transient node-id is generated and returned
// (not persisted). This path is used in tests.
func NodeID(dataDir string) (string, error) {
	if dataDir == "" {
		return generateNodeID(), nil
	}

	// Try to read the existing file first (no once.Do so we can test the
	// stable-across-restarts property without global state).
	path := nodeIDFilePath(dataDir)
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id, nil
		}
	}

	// Generate and persist a new node-id.
	id := generateNodeID()
	dir := filepath.Join(dataDir, "fleet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return id, fmt.Errorf("fleet: mkdir for node_id: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(id+"\n"), 0o600); err != nil {
		return id, fmt.Errorf("fleet: write node_id tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return id, fmt.Errorf("fleet: rename node_id: %w", err)
	}
	return id, nil
}

// generateNodeID creates a ULID-like identifier: 10 bytes of timestamp +
// 16 bytes of random, encoded in Crockford base32.
func generateNodeID() string {
	now := time.Now().UnixMilli()
	ts := make([]byte, 6)
	for i := 5; i >= 0; i-- {
		ts[i] = byte(now & 0xff)
		now >>= 8
	}
	rnd := make([]byte, 10)
	_, _ = rand.Read(rnd)
	combined := append(ts, rnd...)
	// Crockford base32 (no padding, uppercase).
	enc := base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)
	return enc.EncodeToString(combined)
}

// ClearNodeID removes <dataDir>/fleet/node_id.txt so the next NodeID call
// mints a fresh id. Used when fleet answers 403 node_removed: an admin
// removal blocks that (user, node_id) pair, and re-enroll only works under
// a NEW node id (fleet contract §10.1). Wire-id-safe: wire ids derive from
// wire_id_salt, never node_id (wire_id.go, F8) — that file is NOT touched.
// A missing file is not an error.
func ClearNodeID(dataDir string) error {
	if dataDir == "" {
		return nil
	}
	if err := os.Remove(nodeIDFilePath(dataDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("fleet: clear node_id: %w", err)
	}
	return nil
}

// ReadNodeID returns the persisted node id under dataDir, or "" when none
// exists. Unlike NodeID it never mints one — used where creating an id
// would be wrong (self-unenroll on sign-out names the node that enrolled).
func ReadNodeID(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	data, err := os.ReadFile(nodeIDFilePath(dataDir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// nodeRemovedMarkerPath is the durable "an org admin removed this device"
// marker (device-keys-handoff-01DEVKH01 review fix #5).
func nodeRemovedMarkerPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "node_removed")
}

// MarkNodeRemoved persists the node_removed terminal state so that a
// restart, a served-mode supervisor tick or a partially-failed ClearTokens
// cannot re-enroll (under a freshly minted node id) without an explicit
// sign-in. identity is the TokenIdentityKey of the account that was
// signed in when the node was removed ("" when unknown); it is recorded so
// a served guest — which has no sign-in of its own — can clear the marker
// when its host presents a DIFFERENT identity (ClearNodeRemovedForNewIdentity).
// Otherwise cleared only by ClearNodeRemoved from the sign-in flow.
func MarkNodeRemoved(dataDir, identity string) error {
	if dataDir == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "fleet"), 0o700); err != nil {
		return fmt.Errorf("fleet: mark node removed: %w", err)
	}
	body := time.Now().UTC().Format(time.RFC3339) + "\n"
	if identity != "" {
		body += nodeRemovedIdentityPrefix + identity + "\n"
	}
	if err := os.WriteFile(nodeRemovedMarkerPath(dataDir), []byte(body), 0o600); err != nil {
		return fmt.Errorf("fleet: mark node removed: %w", err)
	}
	return nil
}

// nodeRemovedIdentityPrefix introduces the recorded identity line in the
// node_removed marker (line 1 is the RFC 3339 stamp).
const nodeRemovedIdentityPrefix = "identity="

// NodeRemovedIdentity returns the identity recorded in the node_removed
// marker, or "" when there is no marker or it predates identity recording.
func NodeRemovedIdentity(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	raw, err := os.ReadFile(nodeRemovedMarkerPath(dataDir))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), nodeRemovedIdentityPrefix); ok {
			return v
		}
	}
	return ""
}

// ClearNodeRemovedForNewIdentity clears the node_removed marker when
// current is a known identity that differs from the one recorded at
// removal — the served-mode "re-authorized sign-in": the guest has no
// sign-in of its own, so a host presenting a different account is the
// only re-authorization it can observe. It never clears when either side
// is unknown ("" current, or a marker without a recorded identity): the
// same account re-presenting the same token must stay blocked (fleet
// contract §10.1). Reports whether the marker was cleared.
func ClearNodeRemovedForNewIdentity(dataDir, current string) (bool, error) {
	if dataDir == "" || current == "" || !NodeRemovedMarked(dataDir) {
		return false, nil
	}
	recorded := NodeRemovedIdentity(dataDir)
	if recorded == "" || recorded == current {
		return false, nil
	}
	if err := ClearNodeRemoved(dataDir); err != nil {
		return false, err
	}
	return true, nil
}

// NodeRemovedMarked reports whether the node_removed marker is present.
func NodeRemovedMarked(dataDir string) bool {
	if dataDir == "" {
		return false
	}
	_, err := os.Stat(nodeRemovedMarkerPath(dataDir))
	return err == nil
}

// ClearNodeRemoved removes the marker (explicit sign-in only).
func ClearNodeRemoved(dataDir string) error {
	if dataDir == "" {
		return nil
	}
	if err := os.Remove(nodeRemovedMarkerPath(dataDir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("fleet: clear node removed: %w", err)
	}
	return nil
}
