package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/cedarpolicy"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// fakeCedarPolicy is a test double for cedarpolicy.CedarPolicyAPI. It
// records WritePolicySnippet calls so tests can assert which snippets
// were written without touching the filesystem.
type fakeCedarPolicy struct {
	mu       sync.Mutex
	snippets map[string]string // filename → body
	writeErr error
}

// Compile-time witness: *fakeCedarPolicy satisfies cedarpolicy.CedarPolicyAPI.
var _ cedarpolicy.CedarPolicyAPI = (*fakeCedarPolicy)(nil)

func newFakeCedarPolicy() *fakeCedarPolicy {
	return &fakeCedarPolicy{snippets: map[string]string{}}
}

func (f *fakeCedarPolicy) ListPolicies(_ context.Context) ([]cedarpolicy.PolicyFile, error) {
	return nil, nil
}
func (f *fakeCedarPolicy) ReloadPolicies(_ context.Context) error { return nil }
func (f *fakeCedarPolicy) RecentDecisions(_ context.Context, _ int) ([]cedarpolicy.Decision, error) {
	return nil, nil
}
func (f *fakeCedarPolicy) WritePolicySnippet(_ context.Context, filename, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	f.snippets[filename] = body
	return nil
}
func (f *fakeCedarPolicy) RevokePolicySnippet(_ context.Context, filename string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.snippets, filename)
	return nil
}

// ── Editor stubs (cedar-policy-editor-ui-01KQ8TD6 WP01) ──────────────
// These satisfy the expanded CedarPolicyAPI interface; the tools tests
// do not exercise these paths so the stubs are intentionally minimal.

func (f *fakeCedarPolicy) GetPolicy(_ context.Context, name string) (cedarpolicy.PolicyFileDetail, error) {
	return cedarpolicy.PolicyFileDetail{}, nil
}
func (f *fakeCedarPolicy) SavePolicy(_ context.Context, _ string, _ string) (cedarpolicy.ParseResult, error) {
	return cedarpolicy.ParseResult{OK: true}, nil
}
func (f *fakeCedarPolicy) DeletePolicy(_ context.Context, _ string) error { return nil }
func (f *fakeCedarPolicy) ValidatePolicy(_ context.Context, _ string) (cedarpolicy.ParseResult, error) {
	return cedarpolicy.ParseResult{OK: true}, nil
}
func (f *fakeCedarPolicy) InstallTemplate(_ context.Context, _, _ string) (cedarpolicy.PolicyFileDetail, error) {
	return cedarpolicy.PolicyFileDetail{}, nil
}

func (f *fakeCedarPolicy) ListPlanModeActions(_ context.Context) ([]string, error) {
	return nil, nil
}

func (f *fakeCedarPolicy) written() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.snippets))
	for k, v := range f.snippets {
		out[k] = v
	}
	return out
}

// fakePool stands in for *stdio.Pool. It records every OpenOne /
// CloseOne call so tests can assert the ServerSpec passed in matches
// the recipe's ToServerSpec output.
type fakePool struct {
	mu         sync.Mutex
	opened     []coremcp.ServerSpec
	closed     []string
	openErr    error
	closeErr   error
	statuses   map[string]stdio.RecipeStatus
	// serverToolsMap lets tests pre-populate which tools each server
	// advertises, so preSeedToolSnippets has something to iterate.
	serverToolsMap map[string][]coremcp.Tool
}

func newFakePool() *fakePool {
	return &fakePool{
		statuses:       map[string]stdio.RecipeStatus{},
		serverToolsMap: map[string][]coremcp.Tool{},
	}
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

func (p *fakePool) ServerTools(id string) []coremcp.Tool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.serverToolsMap[id]
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
	// Install now calls CloseOne for idempotency before OpenOne, so the
	// recorded closes are: [install's pre-evict, uninstall's evict].
	// Both must reference the same recipe id.
	closes := pool.closes()
	if len(closes) != 2 || closes[0] != "test-recipe" || closes[1] != "test-recipe" {
		t.Fatalf("pool closes = %v, want [test-recipe test-recipe]", closes)
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

// TestInstallRecipe_GrantedWorkspaceOverridesDefault (spec 089 plan D4):
// with a resolved workspace override active (a workbench's /workspace
// mount), the shipped filesystem recipe's "${DATA_DIR}/agent-workspace"
// default resolves to the OVERRIDE — the directory the agent actually
// works in — and no harness marker or private workspace is materialised.
func TestInstallRecipe_GrantedWorkspaceOverridesDefault(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	granted := t.TempDir()
	enabled := &recipes.EnabledRecipes{}
	api := New(Config{
		Catalog:      fsCatalog(),
		Enabled:      enabled,
		Pool:         newFakePool(),
		Secrets:      secrets.NewMemoryBackend(),
		DataDir:      dataDir,
		WorkspaceDir: granted,
	})
	if _, err := api.InstallRecipe(context.Background(), "filesystem", nil, nil); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}
	// The granted dir is the user's tree — never marked as harness-owned.
	if _, err := os.Stat(filepath.Join(granted, ".kenaz-workspace")); !os.IsNotExist(err) {
		t.Fatal("marker must not be written into a granted workspace (D3)")
	}
	// The private default is not created as a side effect either.
	if _, err := os.Stat(filepath.Join(dataDir, "agent-workspace")); !os.IsNotExist(err) {
		t.Fatal("private workspace must not be materialised when the override is in use")
	}
	entry, ok := enabled.Get("filesystem")
	if !ok {
		t.Fatal("filesystem not persisted")
	}
	dirs, ok := entry.Config["allowed_directories"].([]string)
	if !ok || len(dirs) != 1 || dirs[0] != granted {
		t.Fatalf("persisted dirs = %v, want [%s]", entry.Config["allowed_directories"], granted)
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
	if _, statErr := os.Stat(filepath.Join(wantWorkspace, ".kenaz-workspace")); statErr != nil {
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

// ── WP11 pre-seed tests ────────────────────────────────────────────────────

// toolsRecipe returns a recipe that declares two tools and has no
// PromptOnFirstUse restrictions. Used by the pre-seed tests.
func toolsRecipe(id string, toolNames ...string) recipes.Recipe {
	return recipes.Recipe{
		ID:          id,
		DisplayName: "Tools " + id,
		Command:     []string{"/bin/echo"},
	}
}

// TestPreSeed_AllToolsSeededWhenNoPromptList verifies that when
// PromptOnFirstUse is empty and PreSeedingPolicy is "" (allow_all),
// every tool discovered by the pool gets a Cedar snippet.
func TestPreSeed_AllToolsSeededWhenNoPromptList(t *testing.T) {
	t.Parallel()
	backend := secrets.NewMemoryBackend()
	pool := newFakePool()
	cedar := newFakeCedarPolicy()

	r := recipes.Recipe{
		ID:          "my-recipe",
		DisplayName: "My Recipe",
		Command:     []string{"/bin/echo"},
	}
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{r}}

	// Pre-populate the pool with tools for my-recipe.
	pool.serverToolsMap["my-recipe"] = []coremcp.Tool{
		{Server: "my-recipe", Name: "search"},
		{Server: "my-recipe", Name: "fetch"},
	}

	api := New(Config{
		Catalog:     cat,
		Enabled:     &recipes.EnabledRecipes{},
		Pool:        pool,
		Secrets:     backend,
		DataDir:     t.TempDir(),
		CedarPolicy: cedar,
	})

	if _, err := api.InstallRecipe(context.Background(), "my-recipe", nil, nil); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}

	written := cedar.written()
	if len(written) != 2 {
		t.Fatalf("want 2 snippets written, got %d: %v", len(written), written)
	}
	wantSearch := "my_recipe__search.cedar"
	wantFetch := "my_recipe__fetch.cedar"
	if _, ok := written[wantSearch]; !ok {
		t.Errorf("snippet %q not written; got %v", wantSearch, written)
	}
	if _, ok := written[wantFetch]; !ok {
		t.Errorf("snippet %q not written; got %v", wantFetch, written)
	}
}

// TestPreSeed_PromptTaggedToolSkipped verifies that a tool listed in
// PromptOnFirstUse does NOT get a pre-seeded snippet while other tools do.
func TestPreSeed_PromptTaggedToolSkipped(t *testing.T) {
	t.Parallel()
	backend := secrets.NewMemoryBackend()
	pool := newFakePool()
	cedar := newFakeCedarPolicy()

	r := recipes.Recipe{
		ID:               "my-recipe",
		DisplayName:      "My Recipe",
		Command:          []string{"/bin/echo"},
		PromptOnFirstUse: []string{"dangerous_tool"},
	}
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{r}}

	pool.serverToolsMap["my-recipe"] = []coremcp.Tool{
		{Server: "my-recipe", Name: "safe_tool"},
		{Server: "my-recipe", Name: "dangerous_tool"},
	}

	api := New(Config{
		Catalog:     cat,
		Enabled:     &recipes.EnabledRecipes{},
		Pool:        pool,
		Secrets:     backend,
		DataDir:     t.TempDir(),
		CedarPolicy: cedar,
	})

	if _, err := api.InstallRecipe(context.Background(), "my-recipe", nil, nil); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}

	written := cedar.written()
	// Only the safe tool should be pre-seeded.
	if len(written) != 1 {
		t.Fatalf("want 1 snippet written, got %d: %v", len(written), written)
	}
	wantSafe := "my_recipe__safe_tool.cedar"
	wantDanger := "my_recipe__dangerous_tool.cedar"
	if _, ok := written[wantSafe]; !ok {
		t.Errorf("snippet %q not written; got %v", wantSafe, written)
	}
	if _, ok := written[wantDanger]; ok {
		t.Errorf("snippet %q was written but should be skipped (prompt_on_first_use)", wantDanger)
	}
}

// TestPreSeed_PromptOnlyPolicyWritesNoSnippets verifies that when
// PreSeedingPolicy is "prompt_only", no snippets are written even when
// tools are discovered.
func TestPreSeed_PromptOnlyPolicyWritesNoSnippets(t *testing.T) {
	t.Parallel()
	backend := secrets.NewMemoryBackend()
	pool := newFakePool()
	cedar := newFakeCedarPolicy()

	r := recipes.Recipe{
		ID:               "my-recipe",
		DisplayName:      "My Recipe",
		Command:          []string{"/bin/echo"},
		PreSeedingPolicy: "prompt_only",
	}
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{r}}

	pool.serverToolsMap["my-recipe"] = []coremcp.Tool{
		{Server: "my-recipe", Name: "tool_a"},
		{Server: "my-recipe", Name: "tool_b"},
	}

	api := New(Config{
		Catalog:     cat,
		Enabled:     &recipes.EnabledRecipes{},
		Pool:        pool,
		Secrets:     backend,
		DataDir:     t.TempDir(),
		CedarPolicy: cedar,
	})

	if _, err := api.InstallRecipe(context.Background(), "my-recipe", nil, nil); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}

	written := cedar.written()
	if len(written) != 0 {
		t.Fatalf("want 0 snippets written with prompt_only policy, got %d: %v", len(written), written)
	}
}

// ── RequestAdditionalAllowedDir tests ─────────────────────────────────────

// fsRecipe returns a minimal filesystem recipe suitable for RequestAdditionalAllowedDir tests.
func fsRecipe() recipes.Recipe {
	return recipes.Recipe{
		ID:          "filesystem",
		DisplayName: "Filesystem",
		Command:     []string{"npx", "-y", "@modelcontextprotocol/server-filesystem"},
	}
}

// fsCatalogMinimal returns a catalog with just the filesystem recipe.
func fsCatalogMinimal() *recipes.Catalog {
	return &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{fsRecipe()}}
}

// installFSRecipe sets up an EnabledRecipes with one "filesystem" entry
// containing the given allowed_directories.
func installFSRecipe(dirs []string) *recipes.EnabledRecipes {
	er := &recipes.EnabledRecipes{}
	er.Add(recipes.EnabledRecipe{
		ID:        "filesystem",
		EnabledAt: time.Now().UTC(),
		Config:    map[string]any{"allowed_directories": dirs},
	})
	return er
}

func TestRequestAdditionalAllowedDir_HappyPathAllowOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	newDir := t.TempDir() // the directory to grant

	reg := cedar.NewRegistry(cedar.WithTimeout(5 * time.Second))
	pool := newFakePool()
	backend := secrets.NewMemoryBackend()
	enabled := installFSRecipe([]string{dir})

	api := New(Config{
		Catalog:        fsCatalogMinimal(),
		Enabled:        enabled,
		Pool:           pool,
		Secrets:        backend,
		DataDir:        t.TempDir(),
		PromptRegistry: reg,
	})

	// Resolve the prompt as AllowOnce in a goroutine while Call blocks.
	go func() {
		for {
			pending := reg.ListPending()
			if len(pending) > 0 {
				_ = reg.Resolve(pending[0].RequestID, cedar.DecisionAllowOnce)
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	granted, expanded, err := api.RequestAdditionalAllowedDir(context.Background(), "filesystem", newDir, "need to read project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !granted {
		t.Error("granted = false; want true")
	}
	if expanded == "" {
		t.Error("expanded path is empty")
	}

	// Config should now include newDir.
	entry, ok := enabled.Get("filesystem")
	if !ok {
		t.Fatal("filesystem entry missing from EnabledRecipes")
	}
	dirs := configStringSlice(entry.Config, "allowed_directories")
	found := false
	for _, p := range dirs {
		if p == expanded {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expanded path %q not in allowed_directories %v", expanded, dirs)
	}

	// CloseOne + OpenOne should have been called (restart).
	if closes := pool.closes(); len(closes) == 0 {
		t.Error("pool.CloseOne was not called (no restart)")
	}
	if opens := pool.opens(); len(opens) == 0 {
		t.Error("pool.OpenOne was not called (no restart)")
	}
}

func TestRequestAdditionalAllowedDir_DenyDecision(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	newDir := t.TempDir()

	reg := cedar.NewRegistry(cedar.WithTimeout(5 * time.Second))
	pool := newFakePool()
	backend := secrets.NewMemoryBackend()
	enabled := installFSRecipe([]string{dir})

	api := New(Config{
		Catalog:        fsCatalogMinimal(),
		Enabled:        enabled,
		Pool:           pool,
		Secrets:        backend,
		DataDir:        t.TempDir(),
		PromptRegistry: reg,
	})

	go func() {
		for {
			pending := reg.ListPending()
			if len(pending) > 0 {
				_ = reg.Resolve(pending[0].RequestID, cedar.DecisionDeny)
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	granted, _, err := api.RequestAdditionalAllowedDir(context.Background(), "filesystem", newDir, "testing deny")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if granted {
		t.Error("granted = true after deny; want false")
	}

	// Config should NOT have been updated.
	entry, _ := enabled.Get("filesystem")
	dirs := configStringSlice(entry.Config, "allowed_directories")
	if len(dirs) != 1 || dirs[0] != dir {
		t.Errorf("allowed_directories changed unexpectedly: %v", dirs)
	}
	if len(pool.closes()) != 0 || len(pool.opens()) != 0 {
		t.Error("pool should not have been touched on deny")
	}
}

func TestRequestAdditionalAllowedDir_DeniedPrefix(t *testing.T) {
	t.Parallel()
	// /etc is on the deny-list — ValidateAllowedDir should reject it
	// before any prompt fires.
	reg := cedar.NewRegistry()
	pool := newFakePool()
	backend := secrets.NewMemoryBackend()
	enabled := installFSRecipe([]string{t.TempDir()})

	api := New(Config{
		Catalog:        fsCatalogMinimal(),
		Enabled:        enabled,
		Pool:           pool,
		Secrets:        backend,
		DataDir:        t.TempDir(),
		PromptRegistry: reg,
	})

	granted, _, err := api.RequestAdditionalAllowedDir(context.Background(), "filesystem", "/etc", "bypass deny-list")
	// Should return an error (or granted=false) without prompting.
	if granted {
		t.Error("granted = true for /etc; want false")
	}
	if err == nil {
		t.Error("expected error for /etc (deny-list); got nil")
	}
	// The registry must have zero pending prompts (no prompt was fired).
	if n := reg.PendingCount(); n != 0 {
		t.Errorf("pending count = %d; want 0 (no prompt should have fired)", n)
	}
}

func TestRequestAdditionalAllowedDir_RestoreOnRestartFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	newDir := t.TempDir()

	reg := cedar.NewRegistry(cedar.WithTimeout(5 * time.Second))
	pool := newFakePool()
	pool.openErr = errors.New("spawn failed")
	backend := secrets.NewMemoryBackend()
	enabled := installFSRecipe([]string{dir})
	dataDir := t.TempDir()

	api := New(Config{
		Catalog:        fsCatalogMinimal(),
		Enabled:        enabled,
		Pool:           pool,
		Secrets:        backend,
		DataDir:        dataDir,
		PromptRegistry: reg,
	})

	go func() {
		for {
			pending := reg.ListPending()
			if len(pending) > 0 {
				_ = reg.Resolve(pending[0].RequestID, cedar.DecisionAllowOnce)
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	granted, _, err := api.RequestAdditionalAllowedDir(context.Background(), "filesystem", newDir, "restart test")
	if granted {
		t.Error("granted = true despite restart failure; want false")
	}
	if err == nil {
		t.Error("expected error from restart failure; got nil")
	}

	// Config should be rolled back — only original dir.
	entry, ok := enabled.Get("filesystem")
	if !ok {
		t.Fatal("filesystem entry missing after rollback")
	}
	dirs := configStringSlice(entry.Config, "allowed_directories")
	if len(dirs) != 1 || dirs[0] != dir {
		t.Errorf("config not rolled back; got %v", dirs)
	}
}
