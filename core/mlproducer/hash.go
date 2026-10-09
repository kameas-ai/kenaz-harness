// Package mlproducer is the harness ML producer (ml-producer-01MLPRD01):
// it turns what the AGENT does inside a harness session — tool dispatch,
// turn ends, session deletion — into minimised events and task
// rows in a durable outbox (core/mlproducer/mlstore) that WP03's shipper
// drains to Fleet's ML lane.
//
// Boundaries (spec §1, §2, §12):
//   - It never observes the person: its only inputs are the agentgraph
//     ToolCallObserver seam, the chat TurnUsageObserver.TurnEnded seam and
//     the session delete observer.
//   - It emits only the six kinds in KindTable (spec §12 A-10).
//   - It is fleet-free (scripts/ci/check-no-fleet-imports.sh): consent
//     arrives through the Gate interface, which WP03 implements over
//     core/fleet in core/rpc.
//   - Nothing it records may be logged: record bodies, raw arguments and
//     tool output never reach a log line.
package mlproducer

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const hashKeyBytes = 32

// HashKeyPath is where the per-install HMAC key lives:
// <dataDir>/fleet/ml_hash_key (0600, never transmitted, never synced).
func HashKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "ml_hash_key")
}

// Hasher computes h(x) = first 16 hex chars of HMAC-SHA256(key, x) under
// the per-install key (spec §7). The key is loaded — or minted — on first
// use, not at construction, so an install that never records or spawns an
// agent process never writes one.
type Hasher struct {
	dataDir string

	once sync.Once
	key  []byte
	err  error
}

// NewHasher returns a lazy Hasher keyed by <dataDir>/fleet/ml_hash_key.
func NewHasher(dataDir string) *Hasher {
	return &Hasher{dataDir: dataDir}
}

// NewHasherWithKey returns a Hasher over a fixed key (tests).
func NewHasherWithKey(key []byte) *Hasher {
	h := &Hasher{key: append([]byte(nil), key...)}
	h.once.Do(func() {})
	return h
}

// H returns h(x). An error means the key could not be loaded; callers
// must then record nothing (an unstable key would mint new ids).
func (h *Hasher) H(x string) (string, error) {
	if h == nil {
		return "", errors.New("mlproducer: nil hasher")
	}
	h.once.Do(func() { h.key, h.err = loadOrCreateHashKey(h.dataDir) })
	if h.err != nil {
		return "", h.err
	}
	mac := hmac.New(sha256.New, h.key)
	_, _ = mac.Write([]byte(x))
	return hex.EncodeToString(mac.Sum(nil))[:16], nil
}

// loadOrCreateHashKey mirrors core/fleet/wire_id.go loadOrCreateWireSalt
// (copied, not imported — this package is fleet-free): read the persisted
// key, minting 32 random bytes (hex, 0600, tmp+rename) when absent. A
// malformed or unreadable file is NEVER overwritten. Unlike the wire salt
// there is no transient fallback: an ephemeral key would hand Fleet a new
// identity for every task after each restart, so the caller fails closed.
func loadOrCreateHashKey(dataDir string) ([]byte, error) {
	if dataDir == "" {
		return nil, errors.New("mlproducer: hash key: no data dir")
	}
	path := HashKeyPath(dataDir)
	raw, err := os.ReadFile(path)
	if err == nil {
		key, derr := hex.DecodeString(strings.TrimSpace(string(raw)))
		if derr != nil || len(key) != hashKeyBytes {
			return nil, errors.New("mlproducer: hash key file is malformed; refusing to overwrite it")
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("mlproducer: read hash key: %w", err)
	}
	fresh := make([]byte, hashKeyBytes)
	if _, err := rand.Read(fresh); err != nil {
		return nil, fmt.Errorf("mlproducer: hash key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mlproducer: mkdir for hash key: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(hex.EncodeToString(fresh)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("mlproducer: write hash key: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, fmt.Errorf("mlproducer: persist hash key: %w", err)
	}
	return fresh, nil
}
