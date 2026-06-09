// Package slashcmd — store.go
//
// SkillStore is the persistent store for fleet-installed skills
// (fleet-skills-sync-01NDFSEX18 WP01).
//
// A "skill" in this context is a user slash-command that originated from the
// fleet catalog (kind="skill") or was pushed down by an org admin as a
// mandated skill. Unlike regular user commands (stored in SQLite + markdown
// by store_user.go), fleet skills are stored as standalone JSON files at:
//
//	<DataDir>/slashcmds/<id>.json
//
// This keeps the store filesystem-only, independent of the SQLite migration
// schedule, and easy to delete/reload atomically — which is exactly what the
// fleet install/uninstall path needs.
//
// Fork recipe (OSS-standalone): delete core/fleet/skills_sync.go and the
// Publish button and skill kind from the Marketplace. This file stays in OSS
// because it is useful standalone (local-only multi-device sync, future
// community marketplaces).
package slashcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SkillSource enumerates where a skill was installed from.
type SkillSource string

const (
	// SkillSourceCatalog indicates the skill was installed from the fleet
	// catalog by the user.
	SkillSourceCatalog SkillSource = "catalog"
	// SkillSourceMandated indicates the skill was pushed down by an org
	// admin via the config-pull bundle (FR-301). The user cannot delete it,
	// only disable it if org policy permits.
	SkillSourceMandated SkillSource = "mandated"
)

// Skill is the on-disk JSON representation of a fleet-installed skill.
// It contains everything needed to reconstruct a UserCommand and register
// it in the Registry at boot.
//
// JSON tags use snake_case to match the catalog wire format.
type Skill struct {
	// ID is the catalog_id for catalog-installed skills, or a stable slug
	// for mandated skills (e.g. "org/security-review").
	ID string `json:"id"`

	// CatalogID is the fleet catalog_id (empty for mandated skills that
	// have no catalog entry yet).
	CatalogID string `json:"catalog_id,omitempty"`

	// Version is the SemVer version of the installed skill.
	Version string `json:"version,omitempty"`

	// Source is "catalog" or "mandated".
	Source SkillSource `json:"source"`

	// Trigger is the slash-command trigger (no leading slash).
	// Must match the name regex: ^[a-z][a-z0-9_-]{0,30}$.
	Trigger string `json:"trigger"`

	// LocalTrigger is the user's local rename, if any. When non-empty,
	// the command is registered under this trigger instead of Trigger
	// (FR-401 collision resolution). Empty means use Trigger.
	LocalTrigger string `json:"local_trigger,omitempty"`

	// Kind is "text", "prompt", or "tool".
	Kind UserCommandKind `json:"kind"`

	// Description is a one-line summary rendered in autocomplete and /help.
	Description string `json:"description,omitempty"`

	// Body is the command body (text expansion / prompt template).
	Body string `json:"body,omitempty"`

	// Tool is the tool name for kind:tool skills.
	Tool string `json:"tool,omitempty"`

	// ToolArgsTemplate is the args template for kind:tool skills.
	ToolArgsTemplate string `json:"tool_args_template,omitempty"`

	// Inputs is the ordered list of user-fillable parameters.
	Inputs []UserCommandInput `json:"inputs,omitempty"`

	// Disabled, when true, prevents the skill from being registered in the
	// Registry. The user can disable a mandated skill if org policy permits.
	Disabled bool `json:"disabled,omitempty"`

	// OrgManaged mirrors Source==SkillSourceMandated for frontend display
	// convenience (FR-302). Derived on load; not stored separately.
	OrgManaged bool `json:"org_managed,omitempty"`

	// InstalledAt is the Unix timestamp (seconds) when the skill was first
	// installed on this device.
	InstalledAt int64 `json:"installed_at,omitempty"`

	// UpdatedAt is the Unix timestamp (seconds) of the last metadata update.
	UpdatedAt int64 `json:"updated_at,omitempty"`
}

// EffectiveTrigger returns the trigger actually used for Registry
// registration: LocalTrigger if non-empty, otherwise Trigger.
func (s *Skill) EffectiveTrigger() string {
	if s.LocalTrigger != "" {
		return s.LocalTrigger
	}
	return s.Trigger
}

// ToUserCommand converts the Skill to a UserCommand suitable for Registry
// registration. The returned command is always global-scoped.
func (s *Skill) ToUserCommand() UserCommand {
	return UserCommand{
		Name:             s.EffectiveTrigger(),
		Scope:            ScopeGlobal,
		Kind:             s.Kind,
		Description:      s.Description,
		Body:             s.Body,
		Tool:             s.Tool,
		ToolArgsTemplate: s.ToolArgsTemplate,
		Inputs:           s.Inputs,
	}
}

// ErrSkillNotFound is returned when a skill lookup yields no file.
var ErrSkillNotFound = errors.New("slashcmd: skill not found")

// ErrSkillPayloadTooLarge is returned when a skill's JSON exceeds 256KB.
var ErrSkillPayloadTooLarge = errors.New("slashcmd: skill payload exceeds 256KB")

// maxSkillPayloadBytes is the 256KB cap from NFR-003.
const maxSkillPayloadBytes = 256 * 1024

// SkillStore persists fleet-installed skills to <DataDir>/slashcmds/*.json.
// Each skill is one JSON file named <id>.json.
//
// SkillStore is goroutine-safe for reads and writes (guarded by mu).
type SkillStore struct {
	mu      sync.RWMutex
	dataDir string
}

// NewSkillStore constructs a SkillStore rooted at dataDir.
// dataDir is the harness data directory (e.g. ~/.kenaz).
func NewSkillStore(dataDir string) *SkillStore {
	return &SkillStore{dataDir: dataDir}
}

// skillsDir returns the path to the skills directory.
func (s *SkillStore) skillsDir() string {
	return filepath.Join(s.dataDir, "slashcmds")
}

// skillPath returns the path to the JSON file for the given skill ID.
func (s *SkillStore) skillPath(id string) string {
	// Sanitise the id to prevent path traversal: replace path separators.
	safe := strings.ReplaceAll(id, "/", "_")
	safe = strings.ReplaceAll(safe, string(filepath.Separator), "_")
	return filepath.Join(s.skillsDir(), safe+".json")
}

// Save writes (or overwrites) the skill to disk atomically (tmp + rename).
// Returns ErrSkillPayloadTooLarge when the JSON exceeds 256KB.
func (s *SkillStore) Save(skill Skill) error {
	if skill.ID == "" {
		return fmt.Errorf("slashcmd: Save skill: ID must not be empty")
	}
	now := time.Now().Unix()
	if skill.InstalledAt == 0 {
		skill.InstalledAt = now
	}
	skill.UpdatedAt = now

	data, err := json.MarshalIndent(skill, "", "  ")
	if err != nil {
		return fmt.Errorf("slashcmd: Save skill: marshal: %w", err)
	}
	if len(data) > maxSkillPayloadBytes {
		return ErrSkillPayloadTooLarge
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.skillsDir(), 0o750); err != nil {
		return fmt.Errorf("slashcmd: Save skill: mkdir: %w", err)
	}
	path := s.skillPath(skill.ID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return fmt.Errorf("slashcmd: Save skill: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("slashcmd: Save skill: rename: %w", err)
	}
	return nil
}

// Delete removes the skill with the given ID from disk.
// Returns nil when the file does not exist (idempotent).
func (s *SkillStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.skillPath(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("slashcmd: Delete skill %q: %w", id, err)
	}
	return nil
}

// Get loads a single skill by ID. Returns ErrSkillNotFound when absent.
func (s *SkillStore) Get(id string) (Skill, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.load(s.skillPath(id))
}

// List returns all skills persisted to disk. Corrupted files are skipped
// (logged to stderr as a best-effort warning).
func (s *SkillStore) List() ([]Skill, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir := s.skillsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("slashcmd: List skills: readdir: %w", err)
	}

	var out []Skill
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// Skip temp files.
		if strings.HasSuffix(e.Name(), ".json.tmp") {
			continue
		}
		sk, err := s.load(filepath.Join(dir, e.Name()))
		if err != nil {
			// Best-effort: skip corrupt files.
			fmt.Fprintf(os.Stderr, "slashcmd: List skills: skip %s: %v\n", e.Name(), err)
			continue
		}
		out = append(out, sk)
	}
	return out, nil
}

// load reads and unmarshals a skill from the given path.
// Callers must hold at least s.mu.RLock.
func (s *SkillStore) load(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Skill{}, fmt.Errorf("%w: %s", ErrSkillNotFound, filepath.Base(path))
		}
		return Skill{}, fmt.Errorf("slashcmd: load skill: read %s: %w", path, err)
	}
	var sk Skill
	if err := json.Unmarshal(data, &sk); err != nil {
		return Skill{}, fmt.Errorf("slashcmd: load skill: unmarshal %s: %w", path, err)
	}
	// Derive OrgManaged from Source.
	sk.OrgManaged = sk.Source == SkillSourceMandated
	return sk, nil
}

// BootLoad reads all skills from disk and registers non-disabled ones
// into the registry. Existing registry entries with colliding names are
// NOT overwritten (built-ins and already-registered user commands take
// precedence, matching FR-401 personal > team > org ordering).
//
// Returns the number of skills successfully registered.
func BootLoad(store *SkillStore, registry *Registry) (int, error) {
	skills, err := store.List()
	if err != nil {
		return 0, fmt.Errorf("slashcmd: BootLoad: list: %w", err)
	}
	count := 0
	for _, sk := range skills {
		if sk.Disabled {
			continue
		}
		cmd := skillCommand{skill: sk}
		if regErr := registry.RegisterSkill(cmd); regErr != nil {
			// Collision with a higher-precedence command — skip silently
			// (the shadow indicator in the UI handles user notification).
			continue
		}
		count++
	}
	return count, nil
}

// LiveRegister registers a skill in the registry immediately.
// If the trigger is already occupied by a higher-priority command, the
// skill is persisted but returns ErrTriggerShadowed so the caller can
// surface the shadow indicator.
//
// ErrTriggerShadowed is informational: the skill was saved successfully;
// it just won't be dispatched until the conflict is resolved.
func LiveRegister(store *SkillStore, registry *Registry, skill Skill) error {
	if err := store.Save(skill); err != nil {
		return err
	}
	cmd := skillCommand{skill: skill}
	if err := registry.RegisterSkill(cmd); err != nil {
		// Return a shadow error so the caller can surface the indicator.
		return fmt.Errorf("%w: %q is already registered", ErrTriggerShadowed, skill.EffectiveTrigger())
	}
	return nil
}

// LiveUnregister removes a skill from disk and unregisters it from the
// registry. Returns ErrSkillNotFound when the skill does not exist.
func LiveUnregister(store *SkillStore, registry *Registry, id string) error {
	sk, err := store.Get(id)
	if err != nil {
		return err
	}
	if err := store.Delete(id); err != nil {
		return err
	}
	registry.Unregister(sk.EffectiveTrigger())
	return nil
}

// ErrTriggerShadowed is returned when a skill's effective trigger is
// already occupied by a higher-precedence command (personal > team > org).
// The skill is persisted but not registered until the conflict resolves.
var ErrTriggerShadowed = errors.New("slashcmd: trigger shadowed by higher-priority command")

// RenameLocalTrigger updates the LocalTrigger of the skill identified by id.
// When newTrigger is empty the rename is cleared (effective trigger reverts to Trigger).
//
// The registry is updated live: the old effective trigger is unregistered and
// the new effective trigger is registered. Returns ErrSkillNotFound when the
// skill does not exist. Returns ErrTriggerShadowed (wrapped) when newTrigger
// is already occupied by a higher-priority command.
//
// Resolves FR-401: user can rename a local trigger to resolve a shadow conflict.
func RenameLocalTrigger(store *SkillStore, registry *Registry, id, newTrigger string) error {
	sk, err := store.Get(id)
	if err != nil {
		return err
	}

	// Unregister the old effective trigger.
	oldTrigger := sk.EffectiveTrigger()
	registry.Unregister(oldTrigger)

	// Apply the rename.
	sk.LocalTrigger = newTrigger
	if err := store.Save(sk); err != nil {
		// Best-effort re-register on save failure so the registry stays consistent.
		if oldTrigger != "" {
			cmd := skillCommand{skill: sk}
			_ = registry.RegisterSkill(cmd)
		}
		return fmt.Errorf("slashcmd: RenameLocalTrigger: save: %w", err)
	}

	// Re-register under the new effective trigger.
	newEffective := sk.EffectiveTrigger()
	if newEffective != "" {
		cmd := skillCommand{skill: sk}
		if regErr := registry.RegisterSkill(cmd); regErr != nil {
			return fmt.Errorf("%w: %q is already registered", ErrTriggerShadowed, newEffective)
		}
	}
	return nil
}
