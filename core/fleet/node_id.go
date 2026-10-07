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
