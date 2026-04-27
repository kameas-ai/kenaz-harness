// Package activities implements the bundled activity sub-graph catalog
// (mission agent-kernel-graph; Bundle A WP05).
//
// Activities are reusable sub-graphs the kernel can invoke from an
// ActivityNode (FR-007). Each activity ships as a YAML file under
// `core/agentgraph/activities/yaml/` and is bundled into the binary
// via `embed`. Users may override or supplement the bundled set by
// dropping YAML files into `<DataDir>/agent_graph/activities/`; the
// loader merges bundled + user-defined catalogs with user overrides
// taking precedence.
//
// This package depends on `core/agentgraph` only — never the reverse —
// to honor charter DIRECTIVE_001 (no cyclic imports). The kernel-side
// `ActivityCatalog` interface defined in `core/agentgraph/seams.go` is
// satisfied by the *Catalog returned from Load.
package activities

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

	agentgraph "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	"gopkg.in/yaml.v3"
)

//go:embed yaml/*.yaml
var bundledFS embed.FS

// DefaultBundledDir is the embed sub-path for the bundled YAML files.
const DefaultBundledDir = "yaml"

// Entry is one resolved activity (as loaded — not yet bound to a Kernel).
// Hash is the hex-encoded SHA-256 of the on-disk YAML bytes; used to
// surface which bundled version a run consumed (per spec: "Activities
// ship in core/agentgraph/activities/ and are version-tagged").
type Entry struct {
	// ID matches Graph.ID (e.g. "plan", "validate", "summarize").
	ID string
	// Version is the version tag declared in the YAML (Graph.Name +
	// description carry the human-readable label; Version is a
	// machine-readable revision string). Empty Version is treated as
	// the canonical "v1" / unversioned variant.
	Version string
	// Hash is the SHA-256 of the source bytes (hex). Stable across
	// processes; recorded in the trace alongside the activity ID.
	Hash string
	// Source identifies where the YAML came from ("bundled" or the
	// absolute file path on disk for user-supplied activities).
	Source string
	// Graph is the parsed (but not yet validated) sub-graph spec. The
	// catalog defers validation to the kernel's existing validator
	// when the activity is first resolved so unrelated activities don't
	// fail-load when a single one is broken.
	Graph agentgraph.Graph
}

// LoadOptions controls catalog loading.
type LoadOptions struct {
	// UserDir is the optional override directory (typically
	// `<DataDir>/agent_graph/activities/`). Empty disables user
	// overrides; missing directory is silently ignored.
	UserDir string
	// SkipBundled, when true, skips the embedded set entirely. Useful
	// in tests that want to drive only user-defined activities.
	SkipBundled bool
}

// LoadCatalog reads every bundled + user activity into a Catalog.
// The returned Catalog satisfies `agentgraph.ActivityCatalog`.
//
// Load policy (in order):
//  1. Bundled YAMLs at compile-time (//go:embed yaml/*.yaml).
//  2. If LoadOptions.UserDir is non-empty and exists, every *.yaml file
//     in the directory (non-recursive) is loaded; collisions on (ID,
//     Version) replace the bundled entry — user override always wins.
//
// Per-file errors are accumulated and surfaced after both directories
// have been walked; the catalog is returned populated with whichever
// files parsed successfully so a single bad user override doesn't
// disable the entire bundled set.
func LoadCatalog(opts LoadOptions) (*Catalog, error) {
	cat := newCatalog()
	var loadErrs []string

	if !opts.SkipBundled {
		if err := loadBundled(cat); err != nil {
			loadErrs = append(loadErrs, err.Error())
		}
	}
	if opts.UserDir != "" {
		if err := loadFromDir(cat, opts.UserDir, "user"); err != nil {
			loadErrs = append(loadErrs, err.Error())
		}
	}
	if len(loadErrs) > 0 {
		return cat, fmt.Errorf("activities: load errors: %s", strings.Join(loadErrs, "; "))
	}
	return cat, nil
}

// loadBundled walks the embedded YAML directory.
func loadBundled(cat *Catalog) error {
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
		ent, perr := parseEntry(data, "bundled:"+e.Name())
		if perr != nil {
			errs = append(errs, fmt.Sprintf("parse %s: %v", path, perr))
			continue
		}
		cat.put(ent)
	}
	if len(errs) > 0 {
		return errors.New("bundled: " + strings.Join(errs, "; "))
	}
	return nil
}

// loadFromDir walks an on-disk directory of *.yaml activity files.
// Missing directory is not an error.
func loadFromDir(cat *Catalog, dir, sourceTag string) error {
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
		ent, perr := parseEntry(data, full)
		if perr != nil {
			errs = append(errs, fmt.Sprintf("parse %s: %v", full, perr))
			continue
		}
		ent.Source = sourceTag + ":" + full
		cat.put(ent)
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// activityEnvelope captures the version tag without re-parsing through
// the typed Graph. Activity authors put `version: "v2"` at the top of
// the YAML; the loader records that string in Entry.Version.
type activityEnvelope struct {
	Version string `yaml:"version"`
}

// parseEntry decodes one YAML payload into an Entry. The Graph itself
// is parsed via agentgraph.LoadYAML (so the loader benefits from the
// kernel's typed-attrs round-trip). Validation is deferred to Resolve
// time so a single bad activity doesn't disable the whole catalog.
func parseEntry(data []byte, source string) (Entry, error) {
	g, err := agentgraph.LoadYAML(data)
	if err != nil {
		return Entry{}, fmt.Errorf("yaml decode: %w", err)
	}
	if g.ID == "" {
		return Entry{}, errors.New("activity yaml missing top-level id")
	}
	// The agentgraph wire format does not currently round-trip a
	// dedicated `version:` field; we tolerate two storage shapes:
	//   1. A `version: "vN"` sibling at the top level (parsed below).
	//   2. A `dial_overrides: { activity_version: "vN" }` entry.
	// Either shape is recorded in Entry.Version.
	var env activityEnvelope
	_ = unmarshalEnvelope(data, &env)
	version := env.Version
	if version == "" {
		if v, ok := g.DialOverrides["activity_version"].(string); ok {
			version = v
		}
	}
	sum := sha256.Sum256(data)
	return Entry{
		ID:      g.ID,
		Version: version,
		Hash:    hex.EncodeToString(sum[:]),
		Source:  source,
		Graph:   g,
	}, nil
}

// unmarshalEnvelope reads only the version sibling from the YAML
// document so we don't have to round-trip the whole spec twice.
func unmarshalEnvelope(data []byte, env *activityEnvelope) error {
	return yaml.Unmarshal(data, env)
}

// listSorted returns the catalog entries deterministically by (ID, Version).
// Convenience for tests + UI listings.
func (c *Catalog) listSorted() []Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Entry, 0, len(c.byID))
	for _, versions := range c.byID {
		for _, e := range versions {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Version < out[j].Version
	})
	return out
}
