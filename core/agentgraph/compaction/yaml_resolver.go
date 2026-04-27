package compaction

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// YAMLResolver is the disk-backed resolver. It composes a MemoryResolver
// (for the in-process layer dictionary) with a YAML file at
// <DataDir>/config/compaction.yaml. On construction the file is read
// once; subsequent Set calls flush back to disk best-effort.
//
// File shape (all sections optional; missing sections fall through to
// the in-memory MemoryResolver defaults):
//
//	global:
//	  sites:
//	    pre_call:
//	      enabled: true
//	      pre_call_threshold: 0.85
//	    post_tool:
//	      enabled: false
//	projects:
//	  proj-abc:
//	    sites:
//	      pre_call:
//	        strategy: drop_oldest
//	sessions:
//	  sess-123:
//	    sites:
//	      pre_call:
//	        enabled: false
//
// Project / session ids that don't appear in the file fall through to
// the global section. The resolver does NOT auto-discover project /
// session ids — callers must supply them explicitly through Set or by
// hand-editing the file.
type YAMLResolver struct {
	mu       sync.Mutex
	mem      *MemoryResolver
	path     string
	disabled bool
}

// NewYAMLResolver constructs a resolver. dataDir may be empty to disable
// disk persistence (the resolver behaves as MemoryResolver). Read
// errors at construction are non-fatal — the resolver falls back to
// SafeDefaults. Subsequent Set calls do not retry the read.
func NewYAMLResolver(dataDir string) (*YAMLResolver, error) {
	r := &YAMLResolver{
		mem: NewMemoryResolver(),
	}
	if dataDir == "" {
		r.disabled = true
		return r, nil
	}
	r.path = filepath.Join(dataDir, "config", "compaction.yaml")
	if err := r.loadFromDisk(); err != nil && !os.IsNotExist(err) {
		// Surface the error so the chassis can log at warn; the
		// resolver still works (SafeDefaults from MemoryResolver).
		return r, fmt.Errorf("compaction: load %q: %w", r.path, err)
	}
	return r, nil
}

// Resolve walks the cascade. Delegates to the in-process MemoryResolver
// the YAML file populated.
func (r *YAMLResolver) Resolve(scope ScopeKey) CompactionConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mem.Resolve(scope)
}

// Get returns the persisted layer config.
func (r *YAMLResolver) Get(layer Layer, scopeID string) CompactionConfig {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mem.Get(layer, scopeID)
}

// Set persists a layer's config in memory and (best-effort) writes it
// back to disk.
func (r *YAMLResolver) Set(layer Layer, scopeID string, cfg CompactionConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mem.Set(layer, scopeID, cfg)
	if !r.disabled {
		// Write failures are silent: the in-memory layer still wins for
		// the active process, and the next chassis boot reads stale
		// disk. A future patch can surface the error via an audit emit.
		_ = r.flushToDiskLocked()
	}
}

// Attribution mirrors the MemoryResolver implementation.
func (r *YAMLResolver) Attribution(scope ScopeKey) Attribution {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mem.Attribution(scope)
}

// Compile-time witness.
var _ Resolver = (*YAMLResolver)(nil)

// yamlDoc is the on-disk shape. Each section maps a layer to
// (scopeID → CompactionConfig). Global has no scope id; projects /
// sessions / runs / nodes are keyed by id.
type yamlDoc struct {
	Global   *CompactionConfig            `yaml:"global,omitempty"`
	Projects map[string]CompactionConfig  `yaml:"projects,omitempty"`
	Sessions map[string]CompactionConfig  `yaml:"sessions,omitempty"`
	Runs     map[string]CompactionConfig  `yaml:"runs,omitempty"`
	Nodes    map[string]CompactionConfig  `yaml:"nodes,omitempty"`
}

// loadFromDisk reads + applies the YAML file. Caller MUST hold r.mu.
func (r *YAMLResolver) loadFromDisk() error {
	if r.disabled || r.path == "" {
		return nil
	}
	data, err := os.ReadFile(r.path) //nolint:gosec // user data dir
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var doc yamlDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("compaction: parse: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if doc.Global != nil {
		r.mem.Set(LayerGlobal, "", markAllSites(*doc.Global))
	}
	for id, cfg := range doc.Projects {
		r.mem.Set(LayerProject, id, markAllSites(cfg))
	}
	for id, cfg := range doc.Sessions {
		r.mem.Set(LayerSession, id, markAllSites(cfg))
	}
	for id, cfg := range doc.Runs {
		r.mem.Set(LayerRun, id, markAllSites(cfg))
	}
	for id, cfg := range doc.Nodes {
		r.mem.Set(LayerNode, id, markAllSites(cfg))
	}
	return nil
}

// markAllSites flags every field that the YAML reader populated as
// "explicit" so the cascade attribution surfaces the YAML layer
// instead of treating zero values as "unset". This is heuristic — we
// can't tell from the parsed struct which fields the user wrote vs
// left at zero. We err on the side of "if it's in the file it counts".
func markAllSites(c CompactionConfig) CompactionConfig {
	if c.Sites == nil {
		return c
	}
	out := c.Clone()
	for site, sc := range out.Sites {
		sc.MarkAll()
		out.Sites[site] = sc
	}
	return out
}

// flushToDiskLocked writes the current in-memory layer state back to
// the YAML file. Caller MUST hold r.mu.
func (r *YAMLResolver) flushToDiskLocked() error {
	if r.disabled || r.path == "" {
		return nil
	}
	doc := yamlDoc{
		Projects: map[string]CompactionConfig{},
		Sessions: map[string]CompactionConfig{},
		Runs:     map[string]CompactionConfig{},
		Nodes:    map[string]CompactionConfig{},
	}
	// Snapshot every persisted layer back into the YAML doc so a
	// project / session override that was set in-process survives a
	// round-trip. The MemoryResolver doesn't expose its layer maps,
	// but we can read them through Get for known layers — the YAML
	// doc captures whatever exists at flush time.
	r.mem.mu.RLock()
	for layer, byScope := range r.mem.layers {
		for scopeID, cfg := range byScope {
			snap := cfg.Clone()
			switch layer {
			case LayerGlobal:
				if scopeID == "" {
					doc.Global = &snap
				}
			case LayerProject:
				doc.Projects[scopeID] = snap
			case LayerSession:
				doc.Sessions[scopeID] = snap
			case LayerRun:
				doc.Runs[scopeID] = snap
			case LayerNode:
				doc.Nodes[scopeID] = snap
			}
		}
	}
	r.mem.mu.RUnlock()
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil { //nolint:gosec
		return fmt.Errorf("compaction: mkdir: %w", err)
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("compaction: marshal: %w", err)
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil { //nolint:gosec
		return fmt.Errorf("compaction: write tmp: %w", err)
	}
	return os.Rename(tmp, r.path)
}
