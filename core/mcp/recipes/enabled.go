package recipes

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EnabledRecipe is one row in recipes.enabled.json. The shape mirrors
// data-model.md.
type EnabledRecipe struct {
	// ID is a foreign key into Catalog.Recipes (matched by Recipe.ID).
	ID string `json:"id"`
	// EnabledAt is when the user toggled this recipe on. RFC3339.
	EnabledAt time.Time `json:"enabled_at"`
	// SamplingEnabled is the per-server sampling toggle (FR-015).
	// Initialised from Recipe.SamplingPolicy.Default at install time.
	SamplingEnabled bool `json:"sampling_enabled"`
	// EnvAuditHash is sha256 of the sorted keychain locator strings
	// (NOT values). It changes when env_keys add/rename — surfaces a
	// "configuration changed" hint without leaking credential
	// material.
	EnvAuditHash string `json:"env_audit_hash"`
	// Config is the per-install user config captured at install time
	// (e.g. {"allowed_directories": ["/tmp/foo"], "read_only": true}
	// for the filesystem recipe). nil and empty are treated as
	// identical: the omitempty tag drops the field from disk when
	// there is nothing to persist, and a missing field on load
	// unmarshals to a nil map. The value type is permissive (any) so
	// a future schema bump can add fields without breaking the
	// round-trip on already-saved files.
	Config map[string]any `json:"config,omitempty"`
}

// EnabledRecipes is the persisted list of toggled-on recipes.
//
// The struct is a thin wrapper around []EnabledRecipe. All mutating
// methods take the internal mutex; concurrent Save calls are
// serialised so that one tmp file -> rename ordering wins per call,
// rather than two callers racing on the same tmp path.
type EnabledRecipes struct {
	Entries []EnabledRecipe `json:"entries"`

	mu sync.Mutex
}

// enabledFilePath is the canonical relative path under DataDir.
const enabledFilePath = "mcp/recipes.enabled.json"

// LoadEnabled reads <dataDir>/mcp/recipes.enabled.json and returns
// the parsed list. Per spec.md §9 edge case 7:
//   - Missing file -> empty EnabledRecipes, nil error.
//   - Corrupt file (parse error) -> log a warn, return empty
//     EnabledRecipes, nil error. The harness MUST start, even with a
//     mangled enabled list; the user can re-enable from the UI.
//
// Any other I/O error (permissions, etc.) is returned to the caller.
func LoadEnabled(dataDir string) (*EnabledRecipes, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("recipes: LoadEnabled: dataDir is empty")
	}
	path := filepath.Join(dataDir, enabledFilePath)
	data, err := os.ReadFile(path) // #nosec G304 -- dataDir is harness-owned
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &EnabledRecipes{}, nil
		}
		return nil, fmt.Errorf("recipes: read %s: %w", path, err)
	}
	var enabled EnabledRecipes
	if err := json.Unmarshal(data, &enabled); err != nil {
		log.Printf("recipes: warn: corrupt %s (%v) — starting with empty enabled list", path, err)
		return &EnabledRecipes{}, nil
	}
	return &enabled, nil
}

// Save writes the enabled list atomically to <dataDir>/mcp/recipes.enabled.json.
//
// Atomic write protocol:
//  1. MkdirAll on the parent directory.
//  2. Marshal entries to JSON (deterministic, indented).
//  3. Create a sibling tmp file ("<name>.tmp.<pid>.<ts>"), write,
//     fsync, close.
//  4. os.Rename tmp -> final.
//  5. fsync the parent directory so the rename hits the disk on
//     filesystems that buffer dir entries (most ext4, xfs, etc.).
//
// Save is safe under concurrent invocation: the EnabledRecipes mutex
// serialises the whole sequence so the rename always commits a
// consistent snapshot of Entries at the moment Save was called.
func (e *EnabledRecipes) Save(dataDir string) error {
	if e == nil {
		return fmt.Errorf("recipes: Save: nil receiver")
	}
	if dataDir == "" {
		return fmt.Errorf("recipes: Save: dataDir is empty")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	finalPath := filepath.Join(dataDir, enabledFilePath)
	parent := filepath.Dir(finalPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("recipes: mkdir %s: %w", parent, err)
	}

	// Marshal the wire shape directly so we don't accidentally serialise
	// the internal mutex. Deterministic shape: a fresh struct with only
	// the persisted field copied.
	wire := struct {
		Entries []EnabledRecipe `json:"entries"`
	}{Entries: append([]EnabledRecipe(nil), e.Entries...)}
	data, err := json.MarshalIndent(wire, "", "  ")
	if err != nil {
		return fmt.Errorf("recipes: marshal: %w", err)
	}

	// Use a deterministic tmp name; CreateTemp adds randomness so
	// two concurrent saves on the same EnabledRecipes (mu serialises
	// them) and two saves on different EnabledRecipes pointing at the
	// same path (theoretical) don't clobber each other.
	tmp, err := os.CreateTemp(parent, filepath.Base(finalPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("recipes: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("recipes: write tmp: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("recipes: chmod tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("recipes: fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("recipes: close tmp: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		cleanup()
		return fmt.Errorf("recipes: rename %s -> %s: %w", tmpPath, finalPath, err)
	}
	// Reassert mode in case the platform clobbered it on rename.
	_ = os.Chmod(finalPath, 0o600)

	// fsync the parent directory. On Linux this is required for the
	// rename to be durable across a crash; on macOS it is a no-op for
	// regular dirs but harmless. Best-effort: a failure here means the
	// data is on disk but the directory entry might be lost on power
	// loss — log via the returned error so the caller can decide.
	if err := fsyncDir(parent); err != nil {
		return fmt.Errorf("recipes: fsync parent %s: %w", parent, err)
	}
	return nil
}

// fsyncDir opens dir read-only and calls Sync. It is split out for
// testability and to keep Save readable.
func fsyncDir(dir string) error {
	d, err := os.Open(dir) // #nosec G304 -- caller-provided dataDir
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Add inserts or replaces the entry for rec.ID. The replace-on-match
// shape means callers don't have to Remove + Add to update fields.
func (e *EnabledRecipes) Add(rec EnabledRecipe) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.Entries {
		if e.Entries[i].ID == rec.ID {
			e.Entries[i] = rec
			return
		}
	}
	e.Entries = append(e.Entries, rec)
}

// Remove drops the entry with id. Returns true if an entry was
// removed.
func (e *EnabledRecipes) Remove(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.Entries {
		if e.Entries[i].ID == id {
			e.Entries = append(e.Entries[:i], e.Entries[i+1:]...)
			return true
		}
	}
	return false
}

// Get looks up the entry for id.
func (e *EnabledRecipes) Get(id string) (EnabledRecipe, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.Entries {
		if e.Entries[i].ID == id {
			return e.Entries[i], true
		}
	}
	return EnabledRecipe{}, false
}

// List returns a copy of the entries slice. The copy means the caller
// may sort or filter without disturbing the persistent state.
func (e *EnabledRecipes) List() []EnabledRecipe {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]EnabledRecipe, len(e.Entries))
	copy(out, e.Entries)
	return out
}
