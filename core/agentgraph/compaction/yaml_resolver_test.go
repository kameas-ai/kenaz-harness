package compaction

import (
	"os"
	"path/filepath"
	"testing"
)

func TestYAMLResolver_DisabledWhenEmptyDataDir(t *testing.T) {
	t.Parallel()
	r, err := NewYAMLResolver("")
	if err != nil {
		t.Fatalf("NewYAMLResolver: %v", err)
	}
	if r == nil {
		t.Fatal("nil resolver")
	}
	// Behaves like MemoryResolver: SafeDefaults at the global layer.
	cfg := r.Resolve(ScopeKey{})
	if cfg.Sites == nil {
		t.Errorf("expected Sites populated")
	}
}

func TestYAMLResolver_LoadFromDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write a minimal compaction.yaml with an explicit project override.
	body := `global:
  sites:
    pre_call:
      enabled: true
      pre_call_threshold: 0.7
projects:
  proj-1:
    sites:
      pre_call:
        enabled: false
`
	if err := os.WriteFile(filepath.Join(cfgDir, "compaction.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	r, err := NewYAMLResolver(dir)
	if err != nil {
		t.Fatalf("NewYAMLResolver: %v", err)
	}
	// Resolve for project proj-1 — the pre_call site should be disabled.
	cfg := r.Resolve(ScopeKey{ProjectID: "proj-1"})
	pre := cfg.ForSite(SitePreCall)
	if pre.Enabled {
		t.Errorf("pre_call.Enabled = true; project override should disable")
	}
	// Sanity: a different project gets the global default (Enabled=true).
	cfgOther := r.Resolve(ScopeKey{ProjectID: "proj-2"})
	preOther := cfgOther.ForSite(SitePreCall)
	if !preOther.Enabled {
		t.Errorf("pre_call.Enabled = false for proj-2; expected global true")
	}
}

// TestYAMLResolver_WithDefaultsSeedsGivenGlobalConfig covers the
// production wiring seam: newGraphManagerWithDeps
// (core/rpc/api.go) constructs its resolver via
// NewYAMLResolverWithDefaults(dataDir, ProductionDefaults()) rather
// than NewYAMLResolver's SafeDefaults, so that when no
// config/compaction.yaml exists on disk yet, the in-memory seed
// already reflects today's shipped (pre_call/post_tool disabled)
// production defaults instead of FR-041's own "safe" defaults (which
// have pre_call/post_tool enabled).
func TestYAMLResolver_WithDefaultsSeedsGivenGlobalConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	custom := PresetForTier("conservative")
	r, err := NewYAMLResolverWithDefaults(dir, custom)
	if err != nil {
		t.Fatalf("NewYAMLResolverWithDefaults: %v", err)
	}
	// No compaction.yaml written yet — resolve should reflect the given
	// seed, not SafeDefaults.
	cfg := r.Resolve(ScopeKey{})
	pre := cfg.ForSite(SitePreCall)
	if !pre.Enabled {
		t.Errorf("pre_call.Enabled = false; expected the conservative-tier preset's enabled pre_call to seed the resolver (every tier but \"off\" since agentgraph-total-convergence-01PMGX01 WP08)")
	}
	if pre.PreCallThreshold != 0.95 {
		t.Errorf("pre_call_threshold = %v, want 0.95 (conservative tier)", pre.PreCallThreshold)
	}
}

// TestYAMLResolver_WithDefaultsOverriddenByDiskSection proves the seed
// is only a fallback: when compaction.yaml already has a global
// section on disk, that on-disk config wins over the in-memory
// WithDefaults seed, exactly like NewYAMLResolver's SafeDefaults seed
// is overridden by on-disk global sections today.
func TestYAMLResolver_WithDefaultsOverriddenByDiskSection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `global:
  sites:
    pre_call:
      enabled: true
      pre_call_threshold: 0.42
`
	if err := os.WriteFile(filepath.Join(cfgDir, "compaction.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Seed with a config that disables pre_call — the on-disk file
	// should still win.
	r, err := NewYAMLResolverWithDefaults(dir, PresetForTier("conservative"))
	if err != nil {
		t.Fatalf("NewYAMLResolverWithDefaults: %v", err)
	}
	cfg := r.Resolve(ScopeKey{})
	pre := cfg.ForSite(SitePreCall)
	if !pre.Enabled {
		t.Errorf("pre_call.Enabled = false; on-disk global section should override the in-memory seed")
	}
	if pre.PreCallThreshold != 0.42 {
		t.Errorf("pre_call_threshold = %v, want 0.42 (from disk)", pre.PreCallThreshold)
	}
}

func TestYAMLResolver_PersistsOnSet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r, err := NewYAMLResolver(dir)
	if err != nil {
		t.Fatalf("NewYAMLResolver: %v", err)
	}
	// Set a project layer, then re-read the file from a fresh resolver
	// and assert it sees the override.
	cfg := CompactionConfig{
		Sites: map[Site]SiteConfig{
			SitePreCall: func() SiteConfig {
				sc := SiteConfig{Enabled: false}
				sc.MarkAll()
				return sc
			}(),
		},
	}
	r.Set(LayerProject, "proj-X", cfg)

	// Re-open from disk.
	r2, err := NewYAMLResolver(dir)
	if err != nil {
		t.Fatalf("NewYAMLResolver(2): %v", err)
	}
	got := r2.Resolve(ScopeKey{ProjectID: "proj-X"})
	pre := got.ForSite(SitePreCall)
	if pre.Enabled {
		t.Errorf("pre_call.Enabled should be false after reload")
	}
}
