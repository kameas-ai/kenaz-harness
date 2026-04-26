package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/stdio"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// fakePool stands in for *stdio.Pool. It records every OpenOne /
// CloseOne call so tests can assert the ServerSpec passed in matches
// the recipe's ToServerSpec output.
type fakePool struct {
	mu       sync.Mutex
	opened   []coremcp.ServerSpec
	closed   []string
	openErr  error
	closeErr error
	statuses map[string]stdio.RecipeStatus
}

func newFakePool() *fakePool {
	return &fakePool{statuses: map[string]stdio.RecipeStatus{}}
}

func (p *fakePool) OpenOne(_ context.Context, spec coremcp.ServerSpec) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.openErr != nil {
		return p.openErr
	}
	p.opened = append(p.opened, spec)
	p.statuses[spec.Name] = stdio.RecipeStatus{
		ID:    spec.Name,
		State: string(stdio.StateRunning),
	}
	return nil
}

func (p *fakePool) CloseOne(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closeErr != nil {
		return p.closeErr
	}
	p.closed = append(p.closed, id)
	delete(p.statuses, id)
	return nil
}

func (p *fakePool) RecipeStatus(id string) (stdio.RecipeStatus, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.statuses[id]
	return s, ok
}

func (p *fakePool) opens() []coremcp.ServerSpec {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]coremcp.ServerSpec, len(p.opened))
	copy(out, p.opened)
	return out
}

func (p *fakePool) closes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.closed))
	copy(out, p.closed)
	return out
}

// recordingKeychain captures the locator+plaintext pairs Write
// receives so tests can verify the install path stages every
// supplied env key.
type recordingKeychain struct {
	mu      sync.Mutex
	entries map[string]string
}

func newRecordingKeychain() *recordingKeychain {
	return &recordingKeychain{entries: map[string]string{}}
}

func (k *recordingKeychain) Write(_ context.Context, locator string, plaintext []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.entries[locator] = string(plaintext)
	for i := range plaintext {
		plaintext[i] = 0
	}
	return nil
}

func (k *recordingKeychain) get(locator string) (string, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.entries[locator]
	return v, ok
}

// recordingForgetter captures Forget calls.
type recordingForgetter struct {
	mu       sync.Mutex
	deleted  []string
	backend  *secrets.MemoryBackend
}

func (f *recordingForgetter) Forget(_ context.Context, locator string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, locator)
	if f.backend != nil {
		f.backend.ClearEntry(secrets.RefKeychain, locator)
	}
	return nil
}

func (f *recordingForgetter) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.deleted))
	copy(out, f.deleted)
	return out
}

// fixedKeychain mirrors the real keychainWriter in api.go: writes
// flow into a shared MemoryBackend so ResolveEnv finds them on the
// next call. Tests use this for the "round-trip" install + status
// path where the spec.Env passed to OpenOne must match the resolved
// values.
type fixedKeychain struct {
	backend *secrets.MemoryBackend
}

func (k *fixedKeychain) Write(_ context.Context, locator string, plaintext []byte) error {
	if k.backend != nil {
		k.backend.SetEntry(secrets.RefKeychain, locator, plaintext)
	}
	for i := range plaintext {
		plaintext[i] = 0
	}
	return nil
}

// testRecipe returns a minimal one-key recipe for in-package tests.
func testRecipe(id string) recipes.Recipe {
	return recipes.Recipe{
		ID:          id,
		DisplayName: "Test " + id,
		Command:     []string{"/bin/echo"},
		EnvKeys: []recipes.EnvKey{
			{Name: "TEST_API_KEY", Display: "Test API Key", Required: true},
		},
	}
}

// testCatalog returns a one-recipe catalog suitable for table tests.
func testCatalog(id string) *recipes.Catalog {
	return &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{testRecipe(id)}}
}

func TestInstallRecipe_MissingRequiredEnvFails(t *testing.T) {
	t.Parallel()
	cat := testCatalog("test-recipe")
	enabled := &recipes.EnabledRecipes{}
	pool := newFakePool()
	keychain := newRecordingKeychain()
	api := New(Config{
		Catalog:  cat,
		Enabled:  enabled,
		Pool:     pool,
		Secrets:  secrets.NewMemoryBackend(),
		Keychain: keychain,
		DataDir:  t.TempDir(),
	})

	_, err := api.InstallRecipe(context.Background(), "test-recipe", map[string]string{}, nil)
	if err == nil {
		t.Fatalf("InstallRecipe with missing env: want error, got nil")
	}
	if len(enabled.List()) != 0 {
		t.Fatalf("EnabledRecipes was mutated despite validation failure: %v", enabled.List())
	}
	if len(keychain.entries) != 0 {
		t.Fatalf("keychain received writes despite validation failure: %v", keychain.entries)
	}
	if len(pool.opens()) != 0 {
		t.Fatalf("pool received opens despite validation failure")
	}
}

func TestInstallRecipe_RoundTripPersistsAndSpawns(t *testing.T) {
	t.Parallel()
	cat := testCatalog("test-recipe")
	enabled := &recipes.EnabledRecipes{}
	backend := secrets.NewMemoryBackend()
	pool := newFakePool()
	api := New(Config{
		Catalog:  cat,
		Enabled:  enabled,
		Pool:     pool,
		Secrets:  backend,
		Keychain: &fixedKeychain{backend: backend},
		DataDir:  t.TempDir(),
	})

	env := map[string]string{"TEST_API_KEY": "marker-12345"}
	status, err := api.InstallRecipe(context.Background(), "test-recipe", env, nil)
	if err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}
	if status.ID != "test-recipe" {
		t.Fatalf("status.ID = %q, want %q", status.ID, "test-recipe")
	}
	// Plaintext input map must be zeroed.
	if env["TEST_API_KEY"] != "" {
		t.Fatalf("input env not zeroed: %q", env["TEST_API_KEY"])
	}
	if len(enabled.List()) != 1 || enabled.List()[0].ID != "test-recipe" {
		t.Fatalf("enabled list = %v, want one entry", enabled.List())
	}
	opens := pool.opens()
	if len(opens) != 1 {
		t.Fatalf("pool opens = %d, want 1", len(opens))
	}
	if opens[0].Name != "test-recipe" {
		t.Fatalf("opened spec Name = %q, want %q", opens[0].Name, "test-recipe")
	}
	if opens[0].Transport != "stdio" {
		t.Fatalf("opened spec Transport = %q, want stdio", opens[0].Transport)
	}
	if got := opens[0].Env["TEST_API_KEY"]; got != "marker-12345" {
		t.Fatalf("opened spec Env[TEST_API_KEY] = %q, want %q", got, "marker-12345")
	}
}

func TestUninstallRecipe_RemovesAndCloses(t *testing.T) {
	t.Parallel()
	cat := testCatalog("test-recipe")
	enabled := &recipes.EnabledRecipes{}
	backend := secrets.NewMemoryBackend()
	pool := newFakePool()
	api := New(Config{
		Catalog:  cat,
		Enabled:  enabled,
		Pool:     pool,
		Secrets:  backend,
		Keychain: &fixedKeychain{backend: backend},
		DataDir:  t.TempDir(),
	})

	if _, err := api.InstallRecipe(context.Background(), "test-recipe", map[string]string{"TEST_API_KEY": "x"}, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := api.UninstallRecipe(context.Background(), "test-recipe"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if len(enabled.List()) != 0 {
		t.Fatalf("enabled list = %v, want empty after Uninstall", enabled.List())
	}
	closes := pool.closes()
	if len(closes) != 1 || closes[0] != "test-recipe" {
		t.Fatalf("pool closes = %v, want [test-recipe]", closes)
	}
}

func TestForgetRecipeKey_DeletesLocator(t *testing.T) {
	t.Parallel()
	backend := secrets.NewMemoryBackend()
	backend.SetEntry(secrets.RefKeychain, recipes.KeychainLocator("brave-search", "BRAVE_API_KEY"), []byte("staged"))
	forgetter := &recordingForgetter{backend: backend}
	api := New(Config{
		Catalog:   testCatalog("brave-search"),
		Enabled:   &recipes.EnabledRecipes{},
		Pool:      newFakePool(),
		Secrets:   backend,
		Forgetter: forgetter,
		DataDir:   t.TempDir(),
	})
	if err := api.ForgetRecipeKey(context.Background(), "brave-search", "BRAVE_API_KEY"); err != nil {
		t.Fatalf("ForgetRecipeKey: %v", err)
	}
	calls := forgetter.calls()
	want := recipes.KeychainLocator("brave-search", "BRAVE_API_KEY")
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("forgetter calls = %v, want [%s]", calls, want)
	}
	// In-memory backend must no longer resolve the locator.
	_, err := backend.Resolve(context.Background(), secrets.CredentialReference{Kind: secrets.RefKeychain, Locator: want})
	if err == nil {
		t.Fatalf("Resolve after ForgetRecipeKey: want error, got nil")
	}
}

func TestRecipeStatus_NotInstalledReturnsStopped(t *testing.T) {
	t.Parallel()
	api := New(Config{
		Catalog: testCatalog("test-recipe"),
		Enabled: &recipes.EnabledRecipes{},
		Pool:    newFakePool(),
		Secrets: secrets.NewMemoryBackend(),
		DataDir: t.TempDir(),
	})
	status, err := api.RecipeStatus(context.Background(), "test-recipe")
	if err != nil {
		t.Fatalf("RecipeStatus: %v", err)
	}
	if status.Enabled {
		t.Fatalf("status.Enabled = true for not-installed recipe")
	}
	if status.State != string(stdio.StateStopped) {
		t.Fatalf("status.State = %q, want %q", status.State, stdio.StateStopped)
	}
}

func TestRecipeStatus_InstalledPassesThrough(t *testing.T) {
	t.Parallel()
	cat := testCatalog("test-recipe")
	backend := secrets.NewMemoryBackend()
	pool := newFakePool()
	api := New(Config{
		Catalog:  cat,
		Enabled:  &recipes.EnabledRecipes{},
		Pool:     pool,
		Secrets:  backend,
		Keychain: &fixedKeychain{backend: backend},
		DataDir:  t.TempDir(),
	})
	if _, err := api.InstallRecipe(context.Background(), "test-recipe", map[string]string{"TEST_API_KEY": "x"}, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}
	status, err := api.RecipeStatus(context.Background(), "test-recipe")
	if err != nil {
		t.Fatalf("RecipeStatus: %v", err)
	}
	if status.State != string(stdio.StateRunning) {
		t.Fatalf("status.State = %q, want %q", status.State, stdio.StateRunning)
	}
}

func TestListRecipes_OverlaysEnabledAndStatus(t *testing.T) {
	t.Parallel()
	cat := testCatalog("test-recipe")
	backend := secrets.NewMemoryBackend()
	pool := newFakePool()
	enabled := &recipes.EnabledRecipes{}
	api := New(Config{
		Catalog:  cat,
		Enabled:  enabled,
		Pool:     pool,
		Secrets:  backend,
		Keychain: &fixedKeychain{backend: backend},
		DataDir:  t.TempDir(),
	})

	// Pre-install: one row, not enabled.
	listings, err := api.ListRecipes(context.Background())
	if err != nil {
		t.Fatalf("ListRecipes: %v", err)
	}
	if len(listings) != 1 {
		t.Fatalf("listings = %d, want 1", len(listings))
	}
	if listings[0].Enabled {
		t.Fatalf("pre-install listing is Enabled=true")
	}

	if _, err := api.InstallRecipe(context.Background(), "test-recipe", map[string]string{"TEST_API_KEY": "x"}, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}

	listings, err = api.ListRecipes(context.Background())
	if err != nil {
		t.Fatalf("ListRecipes#2: %v", err)
	}
	if !listings[0].Enabled {
		t.Fatalf("post-install listing is Enabled=false")
	}
	if listings[0].Status.State != string(stdio.StateRunning) {
		t.Fatalf("post-install Status.State = %q, want %q", listings[0].Status.State, stdio.StateRunning)
	}
	if !listings[0].KeysPresent {
		t.Fatalf("post-install KeysPresent = false")
	}
}

func TestUninstallRecipe_UnknownIsNonFatal(t *testing.T) {
	t.Parallel()
	pool := &fakePool{
		statuses: map[string]stdio.RecipeStatus{},
		closeErr: stdio.ErrServerNotFound,
	}
	api := New(Config{
		Catalog: testCatalog("test-recipe"),
		Enabled: &recipes.EnabledRecipes{},
		Pool:    pool,
		Secrets: secrets.NewMemoryBackend(),
		DataDir: t.TempDir(),
	})
	if err := api.UninstallRecipe(context.Background(), "ghost"); err != nil {
		t.Fatalf("UninstallRecipe(ghost) = %v, want nil (ErrServerNotFound is swallowed)", err)
	}
}

// Compile-time witness that the configured Config can construct the
// API and the resulting *API satisfies ToolsAPI.
func TestAPI_SatisfiesToolsAPI(t *testing.T) {
	t.Parallel()
	var _ ToolsAPI = New(Config{})
	// Sanity: a no-catalog construction must surface an error from
	// ListRecipes rather than panic.
	api := New(Config{})
	if _, err := api.ListRecipes(context.Background()); err == nil {
		t.Fatalf("ListRecipes with no catalog: want error, got nil")
	}
}

// fsCatalog returns a one-recipe catalog for the filesystem-style
// install path: ArgsTemplate driven by an allowed_directories
// directory_list ConfigOption. No env keys.
func fsCatalog() *recipes.Catalog {
	return &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{{
		ID:           "filesystem",
		DisplayName:  "Filesystem",
		Command:      []string{"npx", "-y", "@modelcontextprotocol/server-filesystem"},
		ArgsTemplate: []string{"${ALLOWED_DIRS}"},
		ConfigOptions: []recipes.ConfigOption{{
			Name:     "allowed_directories",
			Display:  "Allowed directories",
			Kind:     recipes.ConfigKindDirectoryList,
			Default:  []any{"${DATA_DIR}/agent-workspace"},
			Required: true,
		}},
	}}}
}

func TestInstallRecipe_RequiredConfigMissingFails(t *testing.T) {
	t.Parallel()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{{
		ID:      "fs",
		Command: []string{"/bin/echo"},
		ConfigOptions: []recipes.ConfigOption{{
			Name:     "allowed_directories",
			Kind:     recipes.ConfigKindDirectoryList,
			Required: true,
		}},
	}}}
	enabled := &recipes.EnabledRecipes{}
	pool := newFakePool()
	api := New(Config{
		Catalog: cat,
		Enabled: enabled,
		Pool:    pool,
		Secrets: secrets.NewMemoryBackend(),
		DataDir: t.TempDir(),
	})
	if _, err := api.InstallRecipe(context.Background(), "fs", nil, nil); err == nil {
		t.Fatal("InstallRecipe with missing required config: want error, got nil")
	}
	if len(enabled.List()) != 0 {
		t.Fatalf("EnabledRecipes was mutated despite validation failure: %v", enabled.List())
	}
	if len(pool.opens()) != 0 {
		t.Fatalf("pool received opens despite validation failure")
	}
}

func TestInstallRecipe_DirectoryListNonExistentFails(t *testing.T) {
	t.Parallel()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{{
		ID:      "fs",
		Command: []string{"/bin/echo"},
		ConfigOptions: []recipes.ConfigOption{{
			Name:     "allowed_directories",
			Kind:     recipes.ConfigKindDirectoryList,
			Required: true,
		}},
	}}}
	api := New(Config{
		Catalog: cat,
		Enabled: &recipes.EnabledRecipes{},
		Pool:    newFakePool(),
		Secrets: secrets.NewMemoryBackend(),
		DataDir: t.TempDir(),
	})
	bogus := filepath.Join(t.TempDir(), "definitely-not-here")
	cfg := map[string]any{"allowed_directories": []any{bogus}}
	_, err := api.InstallRecipe(context.Background(), "fs", nil, cfg)
	if err == nil {
		t.Fatal("InstallRecipe with non-existent path: want error, got nil")
	}
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("err = %v, want ErrPathNotFound", err)
	}
}

func TestInstallRecipe_DirectoryListDenyListFails(t *testing.T) {
	t.Parallel()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{{
		ID:      "fs",
		Command: []string{"/bin/echo"},
		ConfigOptions: []recipes.ConfigOption{{
			Name:     "allowed_directories",
			Kind:     recipes.ConfigKindDirectoryList,
			Required: true,
		}},
	}}}
	api := New(Config{
		Catalog: cat,
		Enabled: &recipes.EnabledRecipes{},
		Pool:    newFakePool(),
		Secrets: secrets.NewMemoryBackend(),
		DataDir: t.TempDir(),
	})
	cfg := map[string]any{"allowed_directories": []any{"/etc"}}
	_, err := api.InstallRecipe(context.Background(), "fs", nil, cfg)
	if err == nil {
		t.Fatal("InstallRecipe with /etc: want error, got nil")
	}
	if !errors.Is(err, ErrPathInDenyList) {
		t.Fatalf("err = %v, want ErrPathInDenyList", err)
	}
}

func TestInstallRecipe_UnknownConfigKindFails(t *testing.T) {
	t.Parallel()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{{
		ID:      "weird",
		Command: []string{"/bin/echo"},
		ConfigOptions: []recipes.ConfigOption{{
			Name:     "frobnicate",
			Kind:     "frobnicator", // unknown
			Required: false,
			Default:  "anything",
		}},
	}}}
	api := New(Config{
		Catalog: cat,
		Enabled: &recipes.EnabledRecipes{},
		Pool:    newFakePool(),
		Secrets: secrets.NewMemoryBackend(),
		DataDir: t.TempDir(),
	})
	_, err := api.InstallRecipe(context.Background(), "weird", nil, nil)
	if err == nil {
		t.Fatal("InstallRecipe with unknown ConfigOption kind: want error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown ConfigOption kind") {
		t.Fatalf("err = %v, want \"unknown ConfigOption kind\"", err)
	}
}

func TestInstallRecipe_DataDirSubstitutedInDefault(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	cat := fsCatalog()
	enabled := &recipes.EnabledRecipes{}
	pool := newFakePool()
	api := New(Config{
		Catalog: cat,
		Enabled: enabled,
		Pool:    pool,
		Secrets: secrets.NewMemoryBackend(),
		DataDir: dataDir,
	})
	// No config supplied → falls back to Default which contains
	// ${DATA_DIR}; install must succeed and the workspace must be
	// materialised on disk.
	if _, err := api.InstallRecipe(context.Background(), "filesystem", nil, nil); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}
	wantWorkspace := filepath.Join(dataDir, "agent-workspace")
	if _, statErr := os.Stat(wantWorkspace); statErr != nil {
		t.Fatalf("default workspace not created: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(wantWorkspace, ".kaneaz-workspace")); statErr != nil {
		t.Fatalf("workspace marker not written: %v", statErr)
	}
	// Persisted Config carries the expanded path (no literal ${DATA_DIR}).
	persisted, ok := enabled.Get("filesystem")
	if !ok {
		t.Fatal("filesystem not persisted")
	}
	dirs, ok := persisted.Config["allowed_directories"].([]string)
	if !ok {
		t.Fatalf("persisted config dirs = %T, want []string", persisted.Config["allowed_directories"])
	}
	if len(dirs) != 1 || dirs[0] != wantWorkspace {
		t.Fatalf("persisted dirs = %v, want [%s]", dirs, wantWorkspace)
	}
}

func TestInstallRecipe_BooleanWrongTypeFails(t *testing.T) {
	t.Parallel()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{{
		ID:      "togglerec",
		Command: []string{"/bin/echo"},
		ConfigOptions: []recipes.ConfigOption{{
			Name:     "verbose",
			Kind:     recipes.ConfigKindBoolean,
			Required: true,
		}},
	}}}
	api := New(Config{
		Catalog: cat,
		Enabled: &recipes.EnabledRecipes{},
		Pool:    newFakePool(),
		Secrets: secrets.NewMemoryBackend(),
		DataDir: t.TempDir(),
	})
	cfg := map[string]any{"verbose": "yes"} // wrong type — should be bool
	_, err := api.InstallRecipe(context.Background(), "togglerec", nil, cfg)
	if err == nil {
		t.Fatal("InstallRecipe with non-bool boolean: want error, got nil")
	}
	if !strings.Contains(err.Error(), "must be a boolean") {
		t.Fatalf("err = %v, want \"must be a boolean\"", err)
	}
}

func TestInstallRecipe_FilesystemRoundTripDefaultWorkspace(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	cat := fsCatalog()
	pool := newFakePool()
	api := New(Config{
		Catalog: cat,
		Enabled: &recipes.EnabledRecipes{},
		Pool:    pool,
		Secrets: secrets.NewMemoryBackend(),
		DataDir: dataDir,
	})
	if _, err := api.InstallRecipe(context.Background(), "filesystem", nil, nil); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}
	opens := pool.opens()
	if len(opens) != 1 {
		t.Fatalf("pool opens = %d, want 1", len(opens))
	}
	wantWorkspace := filepath.Join(dataDir, "agent-workspace")
	wantCmd := []string{
		"npx", "-y", "@modelcontextprotocol/server-filesystem", wantWorkspace,
	}
	if len(opens[0].Command) != len(wantCmd) {
		t.Fatalf("Command = %v, want %v", opens[0].Command, wantCmd)
	}
	for i, want := range wantCmd {
		if opens[0].Command[i] != want {
			t.Fatalf("Command[%d] = %q, want %q", i, opens[0].Command[i], want)
		}
	}
}

func TestInstallRecipe_FilesystemMultipleDirsAppendAsArgs(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	extra1 := t.TempDir()
	extra2 := t.TempDir()
	cat := fsCatalog()
	pool := newFakePool()
	api := New(Config{
		Catalog: cat,
		Enabled: &recipes.EnabledRecipes{},
		Pool:    pool,
		Secrets: secrets.NewMemoryBackend(),
		DataDir: dataDir,
	})
	cfg := map[string]any{
		"allowed_directories": []any{
			"${DATA_DIR}/agent-workspace",
			extra1,
			extra2,
		},
	}
	if _, err := api.InstallRecipe(context.Background(), "filesystem", nil, cfg); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}
	opens := pool.opens()
	if len(opens) != 1 {
		t.Fatalf("pool opens = %d, want 1", len(opens))
	}
	got := opens[0].Command
	// Command base = npx -y server-filesystem; trailing 3 args are the dirs.
	if len(got) != 6 {
		t.Fatalf("Command len = %d, want 6 (3 base + 3 dirs): %v", len(got), got)
	}
	wantWorkspace := filepath.Join(dataDir, "agent-workspace")
	if got[3] != wantWorkspace {
		t.Errorf("Command[3] = %q, want %q", got[3], wantWorkspace)
	}
	if got[4] != extra1 {
		t.Errorf("Command[4] = %q, want %q", got[4], extra1)
	}
	if got[5] != extra2 {
		t.Errorf("Command[5] = %q, want %q", got[5], extra2)
	}
}

func TestRecipeConfig_NotEnabledReturnsEmpty(t *testing.T) {
	t.Parallel()
	api := New(Config{
		Catalog: fsCatalog(),
		Enabled: &recipes.EnabledRecipes{},
	})
	cfg, err := api.RecipeConfig(context.Background(), "filesystem")
	if err != nil {
		t.Fatalf("RecipeConfig: %v", err)
	}
	if len(cfg) != 0 {
		t.Fatalf("cfg = %v, want empty", cfg)
	}
}

func TestRecipeConfig_EnabledReturnsPersistedConfig(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	api := New(Config{
		Catalog: fsCatalog(),
		Enabled: &recipes.EnabledRecipes{},
		Pool:    newFakePool(),
		Secrets: secrets.NewMemoryBackend(),
		DataDir: dataDir,
	})
	wantPath := filepath.Join(dataDir, "agent-workspace")
	if _, err := api.InstallRecipe(context.Background(), "filesystem", nil, map[string]any{
		"allowed_directories": []any{"${DATA_DIR}/agent-workspace"},
	}); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}
	cfg, err := api.RecipeConfig(context.Background(), "filesystem")
	if err != nil {
		t.Fatalf("RecipeConfig: %v", err)
	}
	dirs, ok := cfg["allowed_directories"].([]string)
	if !ok {
		t.Fatalf("allowed_directories type = %T, want []string", cfg["allowed_directories"])
	}
	if len(dirs) != 1 || dirs[0] != wantPath {
		t.Fatalf("allowed_directories = %v, want [%q]", dirs, wantPath)
	}
}
