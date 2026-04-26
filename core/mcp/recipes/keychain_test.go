package recipes_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/registry"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/secret"
)

// fakeBackend is a minimal secrets.Backend test double. Mirrors the
// shape used in core/secrets/secrets_test.go but is locator-keyed so
// each test can set up "this locator resolves to that bytes / that
// error" without a registry.
type fakeBackend struct {
	value map[string][]byte
	err   map[string]error
}

func (f *fakeBackend) Kind() registry.BackendKind       { return "fake" }
func (f *fakeBackend) SupportedRefKinds() []ref.RefKind { return []ref.RefKind{ref.RefKeychain} }
func (f *fakeBackend) Health(_ context.Context) registry.BackendHealth {
	return registry.BackendHealth{Status: registry.HealthOK, LastChecked: time.Now()}
}
func (f *fakeBackend) Resolve(_ context.Context, r ref.CredentialReference) (secret.Secret, error) {
	if e, ok := f.err[r.Locator]; ok {
		return nil, e
	}
	v, ok := f.value[r.Locator]
	if !ok {
		return nil, errors.New("not found")
	}
	cp := append([]byte(nil), v...)
	return secret.NewStdlibSecret(cp, r.ID(), r.ConsumerID), nil
}

func TestKeychainLocatorFormat(t *testing.T) {
	got := recipes.KeychainLocator("brave-search", "BRAVE_API_KEY")
	want := "mcp/brave-search/BRAVE_API_KEY"
	if got != want {
		t.Errorf("KeychainLocator = %q, want %q", got, want)
	}
}

func TestKeychainLocatorIndependentInputs(t *testing.T) {
	a := recipes.KeychainLocator("a", "X")
	b := recipes.KeychainLocator("b", "X")
	c := recipes.KeychainLocator("a", "Y")
	if a == b || a == c || b == c {
		t.Errorf("locators not unique: a=%q b=%q c=%q", a, b, c)
	}
}

func TestResolveEnvAllRequired(t *testing.T) {
	r := recipes.Recipe{
		ID:      "brave-search",
		Command: []string{"npx"},
		EnvKeys: []recipes.EnvKey{
			{Name: "BRAVE_API_KEY", Required: true},
		},
	}
	be := &fakeBackend{value: map[string][]byte{
		"mcp/brave-search/BRAVE_API_KEY": []byte("secret-token"),
	}}

	env, err := recipes.ResolveEnv(context.Background(), be, r)
	if err != nil {
		t.Fatalf("ResolveEnv: %v", err)
	}
	if env["BRAVE_API_KEY"] != "secret-token" {
		t.Errorf("env[BRAVE_API_KEY] = %q", env["BRAVE_API_KEY"])
	}
	if len(env) != 1 {
		t.Errorf("env has %d keys, want 1", len(env))
	}
}

func TestResolveEnvMissingRequiredErrors(t *testing.T) {
	r := recipes.Recipe{
		ID:      "brave-search",
		Command: []string{"npx"},
		EnvKeys: []recipes.EnvKey{
			{Name: "BRAVE_API_KEY", Required: true},
		},
	}
	be := &fakeBackend{} // empty; every locator returns "not found"

	env, err := recipes.ResolveEnv(context.Background(), be, r)
	if err == nil {
		t.Fatal("ResolveEnv missing-required = nil, want error")
	}
	if env != nil {
		t.Errorf("env = %+v, want nil on error", env)
	}
}

func TestResolveEnvOptionalAbsentSkipped(t *testing.T) {
	r := recipes.Recipe{
		ID:      "fs",
		Command: []string{"fs"},
		EnvKeys: []recipes.EnvKey{
			{Name: "REQUIRED_KEY", Required: true},
			{Name: "OPTIONAL_KEY", Required: false},
		},
	}
	be := &fakeBackend{value: map[string][]byte{
		"mcp/fs/REQUIRED_KEY": []byte("present"),
		// OPTIONAL_KEY intentionally absent.
	}}

	env, err := recipes.ResolveEnv(context.Background(), be, r)
	if err != nil {
		t.Fatalf("ResolveEnv: %v", err)
	}
	if env["REQUIRED_KEY"] != "present" {
		t.Errorf("REQUIRED_KEY = %q", env["REQUIRED_KEY"])
	}
	if _, ok := env["OPTIONAL_KEY"]; ok {
		t.Errorf("OPTIONAL_KEY should be absent from env, got %q", env["OPTIONAL_KEY"])
	}
}

func TestResolveEnvNilBackend(t *testing.T) {
	r := recipes.Recipe{
		ID:      "x",
		Command: []string{"x"},
		EnvKeys: []recipes.EnvKey{{Name: "K", Required: true}},
	}
	if _, err := recipes.ResolveEnv(context.Background(), nil, r); err == nil {
		t.Fatal("nil backend = nil error, want error")
	}
}

func TestResolveEnvNoKeys(t *testing.T) {
	r := recipes.Recipe{ID: "no-env", Command: []string{"x"}}
	be := &fakeBackend{}
	env, err := recipes.ResolveEnv(context.Background(), be, r)
	if err != nil {
		t.Fatalf("ResolveEnv no-keys: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("env = %+v, want empty", env)
	}
}

func TestResolveEnvBackendErrorOnRequired(t *testing.T) {
	r := recipes.Recipe{
		ID:      "fs",
		Command: []string{"fs"},
		EnvKeys: []recipes.EnvKey{{Name: "K", Required: true}},
	}
	sentinel := errors.New("backend explosion")
	be := &fakeBackend{err: map[string]error{"mcp/fs/K": sentinel}}

	_, err := recipes.ResolveEnv(context.Background(), be, r)
	if err == nil {
		t.Fatal("backend error = nil, want error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want wrapping %v", err, sentinel)
	}
}

func TestResolveEnvUsesKeychainKind(t *testing.T) {
	r := recipes.Recipe{
		ID:      "fs",
		Command: []string{"fs"},
		EnvKeys: []recipes.EnvKey{{Name: "K", Required: true}},
	}
	captured := ref.CredentialReference{}
	be := &captureBackend{
		captured: &captured,
		val:      []byte("v"),
	}
	if _, err := recipes.ResolveEnv(context.Background(), be, r); err != nil {
		t.Fatal(err)
	}
	if captured.Kind != secrets.RefKeychain {
		t.Errorf("ref kind = %v, want RefKeychain", captured.Kind)
	}
	if captured.Locator != "mcp/fs/K" {
		t.Errorf("ref locator = %q", captured.Locator)
	}
}

// captureBackend records the last CredentialReference it was given.
// Useful for testing the wiring between ResolveEnv and the backend.
type captureBackend struct {
	captured *ref.CredentialReference
	val      []byte
}

func (c *captureBackend) Kind() registry.BackendKind       { return "capture" }
func (c *captureBackend) SupportedRefKinds() []ref.RefKind { return []ref.RefKind{ref.RefKeychain} }
func (c *captureBackend) Health(_ context.Context) registry.BackendHealth {
	return registry.BackendHealth{Status: registry.HealthOK}
}
func (c *captureBackend) Resolve(_ context.Context, r ref.CredentialReference) (secret.Secret, error) {
	*c.captured = r
	cp := append([]byte(nil), c.val...)
	return secret.NewStdlibSecret(cp, r.ID(), r.ConsumerID), nil
}

func TestEnvAuditHashStable(t *testing.T) {
	r := recipes.Recipe{
		ID: "brave-search",
		EnvKeys: []recipes.EnvKey{
			{Name: "BRAVE_API_KEY", Required: true},
		},
	}
	a := recipes.EnvAuditHash(r)
	b := recipes.EnvAuditHash(r)
	if a != b {
		t.Errorf("hash unstable across calls: %q vs %q", a, b)
	}
	if a == "" {
		t.Error("hash empty")
	}
	if len(a) != 64 {
		t.Errorf("hash len = %d, want 64 (sha256 hex)", len(a))
	}
}

func TestEnvAuditHashSortInvariant(t *testing.T) {
	a := recipes.Recipe{
		ID:      "x",
		EnvKeys: []recipes.EnvKey{{Name: "A"}, {Name: "B"}},
	}
	b := recipes.Recipe{
		ID:      "x",
		EnvKeys: []recipes.EnvKey{{Name: "B"}, {Name: "A"}},
	}
	ha := recipes.EnvAuditHash(a)
	hb := recipes.EnvAuditHash(b)
	if ha != hb {
		t.Errorf("hash differs by env_keys order: %q vs %q (must be sort-invariant)", ha, hb)
	}
}

func TestEnvAuditHashChangesWhenLocatorChanges(t *testing.T) {
	original := recipes.Recipe{
		ID:      "brave-search",
		EnvKeys: []recipes.EnvKey{{Name: "BRAVE_API_KEY"}},
	}
	renamed := recipes.Recipe{
		ID:      "brave-search",
		EnvKeys: []recipes.EnvKey{{Name: "BRAVE_TOKEN"}},
	}
	relocated := recipes.Recipe{
		ID:      "brave-web", // different recipe id
		EnvKeys: []recipes.EnvKey{{Name: "BRAVE_API_KEY"}},
	}
	added := recipes.Recipe{
		ID: "brave-search",
		EnvKeys: []recipes.EnvKey{
			{Name: "BRAVE_API_KEY"},
			{Name: "BRAVE_REGION"},
		},
	}

	h := recipes.EnvAuditHash(original)
	if h == recipes.EnvAuditHash(renamed) {
		t.Error("rename of env-key did not change hash")
	}
	if h == recipes.EnvAuditHash(relocated) {
		t.Error("rename of recipe id did not change hash")
	}
	if h == recipes.EnvAuditHash(added) {
		t.Error("adding an env-key did not change hash")
	}
}

func TestEnvAuditHashEmptyRecipe(t *testing.T) {
	r := recipes.Recipe{ID: "no-env"}
	h := recipes.EnvAuditHash(r)
	if h == "" {
		t.Fatal("empty hash")
	}
	// Empty hash MUST be different from a recipe with one key — the
	// "no env keys" hash is sha256("") which is not the same as
	// sha256("mcp/no-env/X").
	withKey := recipes.Recipe{ID: "no-env", EnvKeys: []recipes.EnvKey{{Name: "X"}}}
	if h == recipes.EnvAuditHash(withKey) {
		t.Error("empty-keys hash == one-key hash; collision")
	}
}
