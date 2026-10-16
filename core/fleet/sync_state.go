package fleet

// sync_state.go — persisted per-category LWW state for settings sync.
//
// CategoryState.lwwTS is the stamp of the last state this device and Fleet
// agreed on (a successful PUT's echo, or an applied pull). It used to live
// in memory only, so after every restart the device had forgotten what it
// had last seen: ?since= was dropped, nextLWW lost its monotonic floor, and
// a pull-before-push could not tell "Fleet changed since I last synced"
// from "I have never synced". It is now saved per category in a small JSON
// file in the Fleet data dir (FileSyncStateStore), loaded at boot by
// SetStateStore.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// syncStateFileName is FileSyncStateStore's file in the Fleet data dir.
const syncStateFileName = "settings_sync_state.json"

// PersistedSyncState is what survives a restart for one category.
type PersistedSyncState struct {
	// LWWTs is CategoryState.lwwTS as RFC 3339 (empty: nothing seen).
	LWWTs string `json:"lww_ts,omitempty"`
	// Conflict is an unresolved 409 (sync_conflict.go).
	Conflict bool `json:"conflict,omitempty"`
}

// SyncStateStore persists per-category LWW state.
type SyncStateStore interface {
	LoadSyncState() (map[SyncCategory]PersistedSyncState, error)
	SaveSyncState(map[SyncCategory]PersistedSyncState) error
}

// FileSyncStateStore is a SyncStateStore backed by one JSON file, written
// atomically (temp file + rename) with owner-only permissions.
type FileSyncStateStore struct {
	path string
	mu   sync.Mutex
}

// NewFileSyncStateStore returns a store at <dataDir>/settings_sync_state.json.
func NewFileSyncStateStore(dataDir string) *FileSyncStateStore {
	return &FileSyncStateStore{path: filepath.Join(dataDir, syncStateFileName)}
}

// LoadSyncState reads the file. A missing file is an empty state.
func (f *FileSyncStateStore) LoadSyncState() (map[SyncCategory]PersistedSyncState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[SyncCategory]PersistedSyncState{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fleet/sync: read sync state: %w", err)
	}
	out := map[SyncCategory]PersistedSyncState{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("fleet/sync: parse sync state: %w", err)
	}
	return out, nil
}

// SaveSyncState replaces the file's contents.
func (f *FileSyncStateStore) SaveSyncState(state map[SyncCategory]PersistedSyncState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("fleet/sync: write sync state: %w", err)
	}
	tmp, err := os.CreateTemp(dir, syncStateFileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("fleet/sync: write sync state: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("fleet/sync: write sync state: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("fleet/sync: write sync state: %w", err)
	}
	if err := os.Rename(tmpName, f.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("fleet/sync: write sync state: %w", err)
	}
	return nil
}

// SetStateStore installs persistence and loads what it holds into every
// category. rpc.New calls it right after NewSyncer, before anything pulls
// or pushes. Entries for a category not registered yet are kept and applied
// when RegisterCategory creates its state (slash_commands registers late),
// and are never dropped from the file.
func (s *Syncer) SetStateStore(store SyncStateStore) error {
	if store == nil {
		return nil
	}
	loaded, err := store.LoadSyncState()
	if err != nil {
		// Leave the store unset: saving over a file we could not read would
		// erase it.
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateStore = store
	s.persistedLate = map[SyncCategory]PersistedSyncState{}
	for cat, p := range loaded {
		if st, ok := s.categories[cat]; ok {
			applyPersisted(st, p)
		} else {
			s.persistedLate[cat] = p
		}
	}
	return nil
}

// adoptPersistedLocked applies a held entry to a newly created category.
// Caller holds s.mu.
func (s *Syncer) adoptPersistedLocked(cat SyncCategory, st *CategoryState) {
	if p, ok := s.persistedLate[cat]; ok {
		applyPersisted(st, p)
		delete(s.persistedLate, cat)
	}
}

// applyPersisted copies a persisted entry into live state.
func applyPersisted(st *CategoryState, p PersistedSyncState) {
	var ts int64
	if p.LWWTs != "" {
		if v, err := parseLWW(p.LWWTs); err == nil {
			ts = v
		}
	}
	st.mu.Lock()
	if ts > st.lwwTS {
		st.lwwTS = ts
	}
	st.conflict = p.Conflict
	st.mu.Unlock()
}

// persistedFor renders one category's live state for the file.
func persistedFor(st *CategoryState) (PersistedSyncState, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	p := PersistedSyncState{Conflict: st.conflict}
	if st.lwwTS > 0 {
		p.LWWTs = formatLWW(st.lwwTS)
	}
	return p, p != PersistedSyncState{}
}

// persist saves every category's LWW state. Failures are logged, not
// returned: the in-memory state is still right for this run, and the next
// change saves again.
func (s *Syncer) persist() {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.mu.RLock()
	store := s.stateStore
	if store == nil {
		s.mu.RUnlock()
		return
	}
	out := make(map[SyncCategory]PersistedSyncState, len(s.categories)+len(s.persistedLate))
	for cat, p := range s.persistedLate {
		out[cat] = p
	}
	for cat, st := range s.categories {
		if p, ok := persistedFor(st); ok {
			out[cat] = p
		}
	}
	s.mu.RUnlock()
	if err := store.SaveSyncState(out); err != nil {
		logging.L().Warn("fleet.sync.state_save_failed", "err", err.Error())
	}
}
