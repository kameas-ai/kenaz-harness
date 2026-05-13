package agents

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// LoadAll loads the full merged profile set: bundled profiles first, then
// user-authored profiles from <dataDir>/agents/*.yaml. User profiles win on
// id collision (same semantics as slashcmd overrides).
//
// dataDir may be empty; in that case only bundled profiles are returned.
// LoadAll never returns a nil slice — it returns an empty slice on errors.
func LoadAll(dataDir string) ([]Profile, error) {
	bundled, err := loadBundled()
	if err != nil {
		return nil, fmt.Errorf("agents.LoadAll: bundled: %w", err)
	}

	// Index bundled by id.
	byID := make(map[string]Profile, len(bundled))
	for _, p := range bundled {
		byID[p.ID] = p
	}

	if dataDir != "" {
		userProfiles, err := loadUserDir(filepath.Join(dataDir, "agents"))
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("agents.LoadAll: user: %w", err)
		}
		// User profiles override bundled by id.
		for _, p := range userProfiles {
			byID[p.ID] = p
		}
	}

	out := make([]Profile, 0, len(byID))
	for _, p := range byID {
		out = append(out, p)
	}
	sortProfiles(out)
	return out, nil
}

// LoadProfile returns a single profile by id, searching bundled + user
// profiles in the same order as LoadAll. Returns ErrProfileNotFound when
// the id is not known.
func LoadProfile(dataDir, id string) (Profile, error) {
	all, err := LoadAll(dataDir)
	if err != nil {
		return Profile{}, err
	}
	for _, p := range all {
		if p.ID == id {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("%w: %q", ErrProfileNotFound, id)
}

// Save persists a user-authored profile to <dataDir>/agents/<id>.yaml.
// Returns ErrBundledReadOnly when the id matches a bundled profile.
func Save(dataDir string, p Profile) error {
	if err := Validate(p); err != nil {
		return fmt.Errorf("agents.Save: %w", err)
	}
	// Reject attempts to overwrite a bundled profile id.
	bundled, err := loadBundled()
	if err != nil {
		return fmt.Errorf("agents.Save: %w", err)
	}
	for _, b := range bundled {
		if b.ID == p.ID {
			return ErrBundledReadOnly
		}
	}

	agentsDir := filepath.Join(dataDir, "agents")
	if err := os.MkdirAll(agentsDir, 0o750); err != nil {
		return fmt.Errorf("agents.Save: mkdir: %w", err)
	}

	data, err := marshalProfile(p)
	if err != nil {
		return fmt.Errorf("agents.Save: marshal: %w", err)
	}

	path := filepath.Join(agentsDir, p.ID+".yaml")
	if err := os.WriteFile(path, data, 0o640); err != nil {
		return fmt.Errorf("agents.Save: write %s: %w", path, err)
	}
	return nil
}

// Delete removes the user-authored profile with the given id from
// <dataDir>/agents/. Returns ErrBundledReadOnly for bundled ids and
// ErrProfileNotFound when no user file exists for the id.
func Delete(dataDir, id string) error {
	bundled, err := loadBundled()
	if err != nil {
		return fmt.Errorf("agents.Delete: %w", err)
	}
	for _, b := range bundled {
		if b.ID == id {
			return ErrBundledReadOnly
		}
	}

	path := filepath.Join(dataDir, "agents", id+".yaml")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %q", ErrProfileNotFound, id)
		}
		return fmt.Errorf("agents.Delete: %w", err)
	}
	return nil
}

// loadBundled reads all bundled profiles from the embedded FS. Results
// are cached after the first call. Bundled profiles have Bundled=true.
var bundledCache struct {
	once     sync.Once
	profiles []Profile
	err      error
}

func loadBundled() ([]Profile, error) {
	bundledCache.once.Do(func() {
		var out []Profile
		err := fs.WalkDir(BundledFS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".yaml") {
				return nil
			}
			data, err := BundledFS.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
			var p Profile
			if err := yaml.Unmarshal(data, &p); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			p.Bundled = true
			if err := Validate(p); err != nil {
				return fmt.Errorf("validate %s: %w", path, err)
			}
			out = append(out, p)
			return nil
		})
		bundledCache.profiles = out
		bundledCache.err = err
	})
	return bundledCache.profiles, bundledCache.err
}

// loadUserDir reads all *.yaml files from dir and returns the parsed profiles.
// Files that fail to parse are skipped with a logged warning (non-fatal so a
// single bad user file doesn't hide all other profiles).
func loadUserDir(dir string) ([]Profile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Profile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			// Non-fatal: skip unreadable file.
			continue
		}
		var p Profile
		if err := yaml.Unmarshal(data, &p); err != nil {
			// Non-fatal: skip malformed file.
			continue
		}
		p.Bundled = false
		if err := Validate(p); err != nil {
			// Non-fatal: skip invalid profile.
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// marshalProfile converts a Profile to its YAML byte representation.
// AutonomyTier is written as a string.
func marshalProfile(p Profile) ([]byte, error) {
	raw := profileYAML{
		ID:                   p.ID,
		Name:                 p.Name,
		Description:          p.Description,
		WhenToUse:            p.WhenToUse,
		Model:                p.Model,
		AutonomyTierStr:      p.AutonomyTier.String(),
		AllowedTools:         p.AllowedTools,
		DeniedTools:          p.DeniedTools,
		BudgetTokens:         p.BudgetTokens,
		BudgetTimeS:          p.BudgetTimeS,
		SystemPromptOverride: p.SystemPromptOverride,
		MergePolicy:          p.EffectiveMergePolicy(),
	}
	return yaml.Marshal(raw)
}

// sortProfiles sorts a profile slice by id for deterministic output.
func sortProfiles(ps []Profile) {
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && ps[j].ID < ps[j-1].ID; j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
}
