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
