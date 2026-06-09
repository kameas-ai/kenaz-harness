package recipes_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

func TestLoadEnabledMissingReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	got, err := recipes.LoadEnabled(dir)
	if err != nil {
		t.Fatalf("LoadEnabled missing-file: %v", err)
	}
	if got == nil {
		t.Fatal("LoadEnabled returned nil")
	}
	if got.List() == nil {
		// An empty slice is acceptable; nil is too.
	}
	if len(got.List()) != 0 {
		t.Errorf("List() len = %d, want 0", len(got.List()))
	}
}

func TestLoadEnabledEmptyDataDir(t *testing.T) {
	if _, err := recipes.LoadEnabled(""); err == nil {
		t.Fatal("LoadEnabled(\"\") = nil, want error")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := &recipes.EnabledRecipes{}
	want.Add(recipes.EnabledRecipe{
		ID:              "brave-search",
		EnabledAt:       time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		SamplingEnabled: false,
		EnvAuditHash:    "abc123",
	})
	want.Add(recipes.EnabledRecipe{
		ID:              "fs",
		EnabledAt:       time.Date(2026, 2, 2, 13, 0, 0, 0, time.UTC),
		SamplingEnabled: true,
		EnvAuditHash:    "def456",
	})

	if err := want.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := recipes.LoadEnabled(dir)
	if err != nil {
		t.Fatalf("LoadEnabled: %v", err)
	}

	gotEntries := got.List()
	wantEntries := want.List()
	if len(gotEntries) != len(wantEntries) {
		t.Fatalf("len = %d, want %d", len(gotEntries), len(wantEntries))
	}
	for i := range wantEntries {
		if gotEntries[i].ID != wantEntries[i].ID {
			t.Errorf("[%d] ID = %q, want %q", i, gotEntries[i].ID, wantEntries[i].ID)
		}
		if !gotEntries[i].EnabledAt.Equal(wantEntries[i].EnabledAt) {
			t.Errorf("[%d] EnabledAt = %v, want %v", i, gotEntries[i].EnabledAt, wantEntries[i].EnabledAt)
		}
		if gotEntries[i].SamplingEnabled != wantEntries[i].SamplingEnabled {
			t.Errorf("[%d] SamplingEnabled = %v, want %v", i, gotEntries[i].SamplingEnabled, wantEntries[i].SamplingEnabled)
		}
		if gotEntries[i].EnvAuditHash != wantEntries[i].EnvAuditHash {
			t.Errorf("[%d] EnvAuditHash = %q, want %q", i, gotEntries[i].EnvAuditHash, wantEntries[i].EnvAuditHash)
		}
	}
}

func TestLoadEnabledCorruptReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp", "recipes.enabled.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := recipes.LoadEnabled(dir)
	if err != nil {
		t.Fatalf("LoadEnabled corrupt: %v (want nil — corruption should log+empty per spec §9 edge 7)", err)
	}
	if got == nil {
		t.Fatal("LoadEnabled corrupt: got nil")
	}
	if len(got.List()) != 0 {
		t.Errorf("corrupt-load List() len = %d, want 0", len(got.List()))
	}
}

func TestSaveAtomicityFilePresent(t *testing.T) {
	dir := t.TempDir()
	e := &recipes.EnabledRecipes{}
	e.Add(recipes.EnabledRecipe{ID: "fs", EnabledAt: time.Now()})
	if err := e.Save(dir); err != nil {
		t.Fatal(err)
	}
	// No leftover tmp files.
	entries, err := os.ReadDir(filepath.Join(dir, "mcp"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		name := ent.Name()
		if name == "recipes.enabled.json" {
			continue
		}
		t.Errorf("unexpected leftover file in mcp/: %s", name)
	}
}

func TestSaveOverwrites(t *testing.T) {
	dir := t.TempDir()
	a := &recipes.EnabledRecipes{}
	a.Add(recipes.EnabledRecipe{ID: "first"})
	if err := a.Save(dir); err != nil {
		t.Fatal(err)
	}
	b := &recipes.EnabledRecipes{}
	b.Add(recipes.EnabledRecipe{ID: "second"})
	if err := b.Save(dir); err != nil {
		t.Fatal(err)
	}

	got, err := recipes.LoadEnabled(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries := got.List()
	if len(entries) != 1 || entries[0].ID != "second" {
		t.Fatalf("post-overwrite entries = %+v, want [{second ...}]", entries)
	}
}

func TestConcurrentSave(t *testing.T) {
	dir := t.TempDir()
	const n = 10

	// Each goroutine has its own EnabledRecipes pointing at the same
	// data dir. The package contract: Save serialises through the
	// receiver's mutex, but two distinct receivers race on the
	// rename. The post-condition we care about is that the on-disk
	// file always parses as a valid EnabledRecipes — never half-
	// written, never empty when both writers wrote non-empty data.
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			e := &recipes.EnabledRecipes{}
			e.Add(recipes.EnabledRecipe{
				ID:        "writer-" + string(rune('a'+i)),
				EnabledAt: time.Now(),
			})
			if err := e.Save(dir); err != nil {
				t.Errorf("goroutine %d: Save: %v", i, err)
			}
		}()
	}
	wg.Wait()

	// File must be present + parseable. Since each writer wrote a
	// single-entry file, the survivor of the rename race is exactly
	// one of those.
	loaded, err := recipes.LoadEnabled(dir)
	if err != nil {
		t.Fatalf("LoadEnabled after concurrent saves: %v", err)
	}
	entries := loaded.List()
	if len(entries) != 1 {
		t.Fatalf("post-race entries len = %d, want 1 (one writer's snapshot wins the rename)", len(entries))
	}
}

func TestSaveSerialisesSameReceiver(t *testing.T) {
	dir := t.TempDir()
	e := &recipes.EnabledRecipes{}
	for i := 0; i < 50; i++ {
		e.Add(recipes.EnabledRecipe{
			ID:        "id-" + string(rune('a'+(i%26))),
			EnabledAt: time.Now(),
		})
	}

	// 8 goroutines all calling Save on the same receiver. The
	// internal mutex must serialise them so the on-disk file is
	// always a coherent snapshot.
	var wg sync.WaitGroup
	const workers = 8
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				if err := e.Save(dir); err != nil {
					t.Errorf("Save: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	got, err := recipes.LoadEnabled(dir)
	if err != nil {
		t.Fatalf("post-concurrent LoadEnabled: %v", err)
	}
	if len(got.List()) == 0 {
		t.Fatal("post-concurrent loaded empty list, want last-saved snapshot")
	}
}

func TestAddReplacesExisting(t *testing.T) {
	e := &recipes.EnabledRecipes{}
	e.Add(recipes.EnabledRecipe{ID: "x", SamplingEnabled: false})
	e.Add(recipes.EnabledRecipe{ID: "x", SamplingEnabled: true})
	got := e.List()
	if len(got) != 1 {
		t.Fatalf("want 1 entry after re-add, got %d", len(got))
	}
	if !got[0].SamplingEnabled {
		t.Error("Add did not replace existing entry")
	}
}

func TestRemove(t *testing.T) {
	e := &recipes.EnabledRecipes{}
	e.Add(recipes.EnabledRecipe{ID: "a"})
	e.Add(recipes.EnabledRecipe{ID: "b"})
	if !e.Remove("a") {
		t.Error("Remove(a) = false, want true")
	}
	if e.Remove("a") {
		t.Error("Remove(a) = true on second call, want false")
	}
	if _, ok := e.Get("a"); ok {
		t.Error("Get(a) ok after Remove")
	}
	if _, ok := e.Get("b"); !ok {
		t.Error("Get(b) not ok after unrelated Remove")
	}
}

func TestSaveLoadRoundTrip_WithConfig(t *testing.T) {
	dir := t.TempDir()
	want := &recipes.EnabledRecipes{}
	want.Add(recipes.EnabledRecipe{
		ID:              "fs",
		EnabledAt:       time.Date(2026, 3, 3, 14, 0, 0, 0, time.UTC),
		SamplingEnabled: false,
		EnvAuditHash:    "deadbeef",
		Config: map[string]any{
			"allowed_directories": []any{"/tmp/a", "/tmp/b"},
			"read_only":           true,
			"label":               "primary",
		},
	})
	if err := want.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := recipes.LoadEnabled(dir)
	if err != nil {
		t.Fatalf("LoadEnabled: %v", err)
	}
	entries := got.List()
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	cfg := entries[0].Config
	if cfg == nil {
		t.Fatal("Config is nil after round-trip")
	}
	dirs, ok := cfg["allowed_directories"].([]any)
	if !ok {
		t.Fatalf("allowed_directories type = %T, want []any", cfg["allowed_directories"])
	}
	if len(dirs) != 2 || dirs[0] != "/tmp/a" || dirs[1] != "/tmp/b" {
		t.Errorf("allowed_directories = %v", dirs)
	}
	if ro, ok := cfg["read_only"].(bool); !ok || !ro {
		t.Errorf("read_only = %v (type %T), want true", cfg["read_only"], cfg["read_only"])
	}
	if cfg["label"] != "primary" {
		t.Errorf("label = %v", cfg["label"])
	}
}

func TestLoadEnabled_BackwardCompatNoConfigField(t *testing.T) {
	// A pre-WP01 enabled.json (no "config" key on entries) must
	// unmarshal cleanly with Config == nil.
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp", "recipes.enabled.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	old := `{"entries":[{"id":"brave-search","enabled_at":"2026-01-01T00:00:00Z","sampling_enabled":false,"env_audit_hash":"abc"}]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := recipes.LoadEnabled(dir)
	if err != nil {
		t.Fatalf("LoadEnabled: %v", err)
	}
	entries := got.List()
	if len(entries) != 1 {
		t.Fatalf("entries len = %d", len(entries))
	}
	if entries[0].Config != nil {
		t.Errorf("Config = %v, want nil for missing field", entries[0].Config)
	}
}

func TestSave_NilConfigOmitsFromDisk(t *testing.T) {
	// `omitempty` on Config means a nil map disappears from the
	// rendered JSON, keeping the on-disk shape identical to a
	// pre-WP01 file when no config is set.
	dir := t.TempDir()
	e := &recipes.EnabledRecipes{}
	e.Add(recipes.EnabledRecipe{ID: "brave-search", EnabledAt: time.Now()})
	if err := e.Save(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "mcp", "recipes.enabled.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytesContains(data, []byte(`"config"`)) {
		t.Errorf("nil Config leaked to disk: %s", data)
	}
}

func bytesContains(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestSaveOnDiskFormatIsHumanReadable(t *testing.T) {
	// The atomic-write contract uses MarshalIndent. A regression to
	// non-indented JSON would still be functional, but we want to
	// catch it explicitly because operators read this file.
	dir := t.TempDir()
	e := &recipes.EnabledRecipes{}
	e.Add(recipes.EnabledRecipe{ID: "brave-search", EnabledAt: time.Now()})
	if err := e.Save(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "mcp", "recipes.enabled.json"))
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("file does not parse as JSON: %v\n%s", err, data)
	}
	if _, ok := probe["entries"]; !ok {
		t.Errorf("file missing entries key: %s", data)
	}
	// Indent check: a single-line file would be < 80 bytes; an
	// indented one with one entry is ~150+. We only assert "contains
	// a newline" to keep this resilient to future formatting tweaks.
	hasNewline := false
	for _, b := range data {
		if b == '\n' {
			hasNewline = true
			break
		}
	}
	if !hasNewline {
		t.Errorf("file is not indented: %s", data)
	}
}
