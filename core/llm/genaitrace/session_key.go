package genaitrace

// session_key.go — the per-install key behind kenaz.session.hash
// (llm-call-telemetry-01LLMTEL01 WP02).
//
// WP01 keyed the hash per process, so one session hashed differently after
// a restart and the local model-call store could not find a session's
// earlier calls. The key is now minted once per install at
// <dataDir>/telemetry/session_hash_key (32 random bytes, hex, 0600,
// tmp+rename — the core/fleet/wire_id.go loadOrCreateWireSalt pattern) and
// is never transmitted: a hash leaving the machine (opt-in Fleet export)
// still cannot be reversed or recomputed by anyone without this file.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const sessionHashKeyBytes = 32

// SessionHashKeyPath is where the per-install key lives under dataDir.
func SessionHashKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "telemetry", "session_hash_key")
}

// LoadOrCreateSessionHashKey reads the per-install key, minting and
// persisting one when absent. An unreadable or malformed file is NEVER
// overwritten (that would silently re-key every stored hash): the error is
// returned and the caller keeps the per-process key.
func LoadOrCreateSessionHashKey(dataDir string) ([]byte, error) {
	if dataDir == "" {
		return nil, errors.New("genaitrace: session hash key: empty data dir")
	}
	path := SessionHashKeyPath(dataDir)
	raw, err := os.ReadFile(path)
	if err == nil {
		key, derr := hex.DecodeString(strings.TrimSpace(string(raw)))
		if derr != nil || len(key) != sessionHashKeyBytes {
			return nil, fmt.Errorf("genaitrace: session hash key %s is malformed; refusing to overwrite it", path)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("genaitrace: read session hash key: %w", err)
	}
	key := make([]byte, sessionHashKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("genaitrace: session hash key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("genaitrace: mkdir for session hash key: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("genaitrace: write session hash key: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, fmt.Errorf("genaitrace: persist session hash key: %w", err)
	}
	return key, nil
}

// SetSessionHashKey replaces the key SessionHash uses. key must be 32
// bytes; anything else is refused and the current key is kept.
func SetSessionHashKey(key []byte) error {
	if len(key) != sessionHashKeyBytes {
		return fmt.Errorf("genaitrace: session hash key must be %d bytes, got %d", sessionHashKeyBytes, len(key))
	}
	k := append([]byte(nil), key...)
	sessionKey.Store(&k)
	return nil
}

// UseInstallSessionHashKey loads (or mints) the per-install key under
// dataDir and installs it. On error the per-process key stays in force.
func UseInstallSessionHashKey(dataDir string) error {
	key, err := LoadOrCreateSessionHashKey(dataDir)
	if err != nil {
		return err
	}
	return SetSessionHashKey(key)
}
