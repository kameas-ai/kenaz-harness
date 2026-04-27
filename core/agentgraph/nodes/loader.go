package nodes

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed manifests/*.yaml
var bundledFS embed.FS

// DefaultBundledDir is the embed sub-path for shipped manifest YAMLs.
const DefaultBundledDir = "manifests"

// LoadOptions controls catalog loading. Mirrors
// `core/agentgraph/activities/loader.go`'s options shape so the two
// subsystems are consistent.
type LoadOptions struct {
	// UserDir is the optional override directory (typically
	// `<DataDir>/agent_graph/nodes/`). Empty disables user overrides;
	// missing directory is silently ignored.
	//
	// WP01 wires the parameter through but does not yet enforce the
	// forbidden-field rules from FR-021 — that lands in WP07. WP01 still
	// performs the deep-merge so resolver tests that drive overrides
	// against fixtures remain meaningful.
	UserDir string
	// SkipBundled, when true, skips the embedded manifest set entirely.
	// Used by tests that drive only handcrafted fixtures.
	SkipBundled bool
}

// LoadCatalog reads every shipped + user manifest into a Catalog. The
// returned Catalog satisfies the (future) ManifestCatalog seam declared
// in `core/agentgraph/seams.go`.
//
// Load policy (in order):
//  1. Shipped manifests at compile-time (//go:embed manifests/*.yaml).
//  2. If LoadOptions.UserDir is non-empty and exists, every *.yaml file
//     in the directory (non-recursive) is loaded; collisions on `id`
//     deep-merge the user manifest over the shipped manifest.
//
// Each manifest is parsed, content-hashed, and registered. The
// inheritance resolver runs once over the full set after both sources
// are loaded so cross-source extends chains resolve consistently.
func LoadCatalog(opts LoadOptions) (*Catalog, error) {
	parsed := map[string]*Manifest{}
	hashes := map[string]string{}
	sources := map[string]string{}
	var loadErrs []string

	if !opts.SkipBundled {
		if err := loadBundled(parsed, hashes, sources); err != nil {
			loadErrs = append(loadErrs, err.Error())
		}
	}
	if opts.UserDir != "" {
		if err := loadFromDir(parsed, hashes, sources, opts.UserDir); err != nil {
			loadErrs = append(loadErrs, err.Error())
		}
	}

	if len(parsed) == 0 && len(loadErrs) == 0 {
		return newCatalog(), nil
	}

	resolved, resolveErr := resolveAll(parsed)
	cat := newCatalog()
	for id, rm := range resolved {
		rm.Hash = hashes[id]
		rm.Source = sources[id]
		cat.put(rm)
	}
	// Surface the resolver error first (it is the most specific and
	// callers may errors.Is(..., ErrCycleDetected)). Other parse
	// errors are appended after a newline so the message remains
	// readable.
	if resolveErr != nil {
		if len(loadErrs) > 0 {
			return cat, fmt.Errorf("%w; additional load errors: %s", resolveErr, strings.Join(loadErrs, "; "))
		}
		return cat, resolveErr
	}
	if len(loadErrs) > 0 {
		return cat, fmt.Errorf("nodes: load errors: %s", strings.Join(loadErrs, "; "))
	}
	return cat, nil
}

// loadBundled walks the embedded YAML directory.
func loadBundled(parsed map[string]*Manifest, hashes, sources map[string]string) error {
	entries, err := fs.ReadDir(bundledFS, DefaultBundledDir)
	if err != nil {
		return fmt.Errorf("read bundled dir: %w", err)
	}
	var errs []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
			continue
		}
		path := DefaultBundledDir + "/" + e.Name()
		data, rerr := fs.ReadFile(bundledFS, path)
		if rerr != nil {
			errs = append(errs, fmt.Sprintf("read %s: %v", path, rerr))
			continue
		}
		m, perr := parseManifest(data, "bundled:"+e.Name(), e.Name())
		if perr != nil {
			errs = append(errs, fmt.Sprintf("parse %s: %v", path, perr))
			continue
		}
		if existing, ok := parsed[m.ID]; ok && existing != nil {
			errs = append(errs, fmt.Sprintf("%s: %v: %q", path, ErrDuplicateID, m.ID))
			continue
		}
		parsed[m.ID] = m
		hashes[m.ID] = hashOf(data)
		sources[m.ID] = "bundled:" + e.Name()
	}
	if len(errs) > 0 {
		return errors.New("bundled: " + strings.Join(errs, "; "))
	}
	return nil
}

// loadFromDir walks an on-disk directory of *.yaml manifest files.
// Missing directory is not an error. User manifests deep-merge over the
// previously-parsed shipped manifest of the same `id`.
func loadFromDir(parsed map[string]*Manifest, hashes, sources map[string]string, dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat user dir %q: %w", dir, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("user dir %q is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read user dir %q: %w", dir, err)
	}
	var errs []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, rerr := os.ReadFile(full) //nolint:gosec // user-controlled path under DataDir is intentional
		if rerr != nil {
			errs = append(errs, fmt.Sprintf("read %s: %v", full, rerr))
			continue
		}
		m, perr := parseManifest(data, full, e.Name())
		if perr != nil {
			errs = append(errs, fmt.Sprintf("parse %s: %v", full, perr))
			continue
		}
		if existing, ok := parsed[m.ID]; ok && existing != nil {
			merged, merr := mergeUserOverlay(existing, m)
			if merr != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", full, merr))
				continue
			}
			parsed[m.ID] = merged
			// Record the source as the user file path; the hash reflects
			// the user bytes (the merged output is computed; tests assert
			// the hash differs from the shipped manifest's).
			sources[m.ID] = "user:" + full
			hashes[m.ID] = hashOf(data)
			continue
		}
		parsed[m.ID] = m
		hashes[m.ID] = hashOf(data)
		sources[m.ID] = "user:" + full
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// parseManifest decodes one YAML document, normalises layer fields, and
// returns the in-memory Manifest. The fileName is used for filename↔ID
// agreement checks (FR-001 + edge case 2: "Manifest filename mismatches
// its `id` field — load-time error").
func parseManifest(data []byte, source, fileName string) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrManifestParse, source, err)
	}
	// Normalise: archetype manifests fill ID from Archetype.
	if m.IsArchetype() {
		if m.ID == "" {
			m.ID = m.Archetype
		}
		if m.ID != m.Archetype {
			return nil, fmt.Errorf("%w: %s: archetype %q and id %q disagree",
				ErrManifestParse, source, m.Archetype, m.ID)
		}
	}
	if m.ID == "" {
		return nil, fmt.Errorf("%w: %s: manifest missing top-level id/archetype",
			ErrManifestParse, source)
	}
	// Filename↔ID check. We strip the leading underscore archetype
	// convention (`_archetype.compute.yaml`) and the `.yaml` suffix.
	expected := manifestStem(fileName)
	if expected != "" && expected != m.ID {
		return nil, fmt.Errorf("%w: %s: filename stem %q != manifest id %q",
			ErrManifestParse, source, expected, m.ID)
	}
	return &m, nil
}

// manifestStem strips `.yaml`, `_archetype.` prefix, leaving the
// canonical ID we expect to match Manifest.ID. The `_archetype.` prefix
// keeps the archetype manifests visually grouped at the top of a `ls`;
// it has no semantic meaning.
func manifestStem(fileName string) string {
	stem := strings.TrimSuffix(strings.ToLower(fileName), ".yaml")
	stem = strings.TrimSuffix(stem, ".yml")
	if stem == "" {
		return ""
	}
	const prefix = "_archetype."
	if strings.HasPrefix(stem, prefix) {
		stem = strings.TrimPrefix(stem, prefix)
	}
	return stem
}

// mergeUserOverlay deep-merges a user manifest over a shipped manifest.
// FR-020 / FR-022 specify the allowed fields; FR-021 specifies the
// forbidden ones. WP01 enforces the forbidden-field rules so the
// merge produces a clean shape; WP07 will surface the user-friendly
// error reporting against file paths.
func mergeUserOverlay(shipped, user *Manifest) (*Manifest, error) {
	// FR-021: id, category, extends, executor are forbidden.
	if user.ID != "" && user.ID != shipped.ID {
		return nil, fmt.Errorf("%w: id (shipped=%q user=%q)", ErrForbiddenOverrideField, shipped.ID, user.ID)
	}
	if user.Category != "" && user.Category != shipped.Category {
		return nil, fmt.Errorf("%w: category", ErrForbiddenOverrideField)
	}
	if user.Extends != "" && user.Extends != shipped.Extends {
		return nil, fmt.Errorf("%w: extends", ErrForbiddenOverrideField)
	}
	if user.Executor != "" && user.Executor != shipped.Executor {
		return nil, fmt.Errorf("%w: executor", ErrForbiddenOverrideField)
	}
	// Per-attr forbidden field: changing an attr's `type:` is also
	// forbidden (FR-022 / edge case 14).
	for name, userSpec := range user.Attrs {
		if shippedSpec, ok := shipped.Attrs[name]; ok && userSpec.Type != "" && userSpec.Type != shippedSpec.Type {
			return nil, fmt.Errorf("%w: attrs.%s.type (shipped=%q user=%q)",
				ErrForbiddenOverrideField, name, shippedSpec.Type, userSpec.Type)
		}
	}

	merged := cloneManifest(*shipped)
	if user.DisplayName != "" {
		merged.DisplayName = user.DisplayName
	}
	if user.Description != "" {
		merged.Description = user.Description
	}
	if user.Version != "" {
		merged.Version = user.Version
	}
	// Aliases are additive only (FR-022).
	if len(user.Aliases) > 0 {
		seen := map[string]bool{}
		for _, a := range merged.Aliases {
			seen[a] = true
		}
		for _, a := range user.Aliases {
			if !seen[a] {
				merged.Aliases = append(merged.Aliases, a)
				seen[a] = true
			}
		}
	}
	// Defaults: key-by-key merge (user overrides win).
	if len(user.Defaults) > 0 {
		if merged.Defaults == nil {
			merged.Defaults = map[string]interface{}{}
		}
		for k, v := range user.Defaults {
			merged.Defaults[k] = v
		}
	}
	// Attrs merge: per-spec, user may tune `default`, `min/max`,
	// `enum`, `description`. We perform a shallow merge of the
	// AttrSpec fields under the merged map.
	if len(user.Attrs) > 0 {
		if merged.Attrs == nil {
			merged.Attrs = map[string]AttrSpec{}
		}
		for name, userSpec := range user.Attrs {
			cur := merged.Attrs[name]
			if userSpec.Type != "" {
				cur.Type = userSpec.Type
			}
			if userSpec.Default != nil {
				cur.Default = userSpec.Default
			}
			if userSpec.Enum != nil {
				cur.Enum = append([]string(nil), userSpec.Enum...)
			}
			if userSpec.Min != nil {
				v := *userSpec.Min
				cur.Min = &v
			}
			if userSpec.Max != nil {
				v := *userSpec.Max
				cur.Max = &v
			}
			if userSpec.MinLength != nil {
				v := *userSpec.MinLength
				cur.MinLength = &v
			}
			if userSpec.MaxLength != nil {
				v := *userSpec.MaxLength
				cur.MaxLength = &v
			}
			if userSpec.Description != "" {
				cur.Description = userSpec.Description
			}
			merged.Attrs[name] = cur
		}
	}
	return &merged, nil
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// listSorted returns every catalog entry in deterministic ID order. The
// catalog itself uses a map; this is the public iteration helper used
// by tests + the eventual frontend RPC.
func (c *Catalog) listSorted() []*ResolvedManifest {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*ResolvedManifest, 0, len(c.byID))
	for _, rm := range c.byID {
		out = append(out, rm)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Manifest.ID < out[j].Manifest.ID
	})
	return out
}
