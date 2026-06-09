package credstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/credstore"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
	"github.com/kameas-ai/kenaz-harness/core/secrets/ref"
	"github.com/kameas-ai/kenaz-harness/core/secrets/registry"
	"github.com/kameas-ai/kenaz-harness/core/secrets/secret"
)

// ── fakeMCPBackend ─────────────────────────────────────────────────────
//
// fakeMCPBackend implements secrets.Backend (registry.Backend) using an
// in-memory map keyed by locator. Used exclusively for IssueForMCPSpawn
// tests. Distinct from fakeResolver (which implements ResolverAPI with a
// consumerID parameter) because secrets.Backend has a two-parameter
// Resolve.

type fakeMCPBackend struct {
	data map[string][]byte
}

func newFakeMCPBackend(entries map[string][]byte) secrets.Backend {
	return &fakeMCPBackend{data: entries}
}

func (b *fakeMCPBackend) Kind() registry.BackendKind { return "fake-mcp" }
func (b *fakeMCPBackend) SupportedRefKinds() []ref.RefKind {
	return []ref.RefKind{ref.RefKeychain}
}
func (b *fakeMCPBackend) Resolve(_ context.Context, r ref.CredentialReference) (secret.Secret, error) {
	v, ok := b.data[r.Locator]
	if !ok {
		return nil, errors.New("fake: not found: " + r.Locator)
	}
	dup := make([]byte, len(v))
	copy(dup, v)
	return secret.NewStdlibSecret(dup, r.ID(), r.ConsumerID), nil
}
func (b *fakeMCPBackend) Health(_ context.Context) registry.BackendHealth {
	return registry.BackendHealth{Status: registry.HealthOK}
}

// ── gate stubs ─────────────────────────────────────────────────────────

// denyGate is a cedar.Gate that always returns Deny.
type denyGate struct{}

func (denyGate) Evaluate(
	_ context.Context,
	principal cedarlib.EntityUID,
	action string,
	resource cedarlib.EntityUID,
	_ map[cedarlib.String]cedarlib.Value,
) cedar.Decision {
	return cedar.Decision{
		Outcome:   cedar.Deny,
		Action:    action,
		Principal: principal.String(),
		Resource:  resource.String(),
		Reason:    "test deny",
	}
}

// naGate is a cedar.Gate that always returns NotApplicable.
type naGate struct{}

func (naGate) Evaluate(
	_ context.Context,
	principal cedarlib.EntityUID,
	action string,
	resource cedarlib.EntityUID,
	_ map[cedarlib.String]cedarlib.Value,
) cedar.Decision {
	return cedar.Decision{
		Outcome:   cedar.NotApplicable,
		Action:    action,
		Principal: principal.String(),
		Resource:  resource.String(),
		Reason:    "no rule matched",
	}
}

// ── helpers ────────────────────────────────────────────────────────────

// locFor returns the canonical keychain locator for a recipe + key pair.
func locFor(recipeID, key string) string {
	return recipes.KeychainLocator(recipeID, key)
}

// newStoreWithRegistry returns a store with an optional Cedar gate and an
// optional prompt registry. The store's resolver is a no-op stub (used
// only by Issue/Use, not by IssueForMCPSpawn which takes its own backend).
func newStoreWithRegistry(
	t *testing.T,
	gate cedar.Gate,
	reg *cedar.Registry,
) credstore.Store {
	t.Helper()
	s := credstore.New(
		newFakeResolver(nil),
		nil,
		credstore.WithCedarGate(gate, nil),
		credstore.WithPromptRegistry(reg),
	)
	t.Cleanup(s.Close)
	return s
}

// ── Allow path ─────────────────────────────────────────────────────────

// TestIssueForMCPSpawn_NilGate_DefaultAllow verifies that when no Cedar
// gate is wired the store returns the resolved env map (default-allow).
func TestIssueForMCPSpawn_NilGate_DefaultAllow(t *testing.T) {
	s := newStoreWithRegistry(t, nil, nil)
	backend := newFakeMCPBackend(map[string][]byte{
		locFor("my-recipe", "API_KEY"): []byte("secret-value"),
	})
	ctx := context.Background()

	env, err := s.IssueForMCPSpawn(ctx, "my-recipe", []string{"API_KEY"}, backend)
	if err != nil {
		t.Fatalf("IssueForMCPSpawn: unexpected error: %v", err)
	}
	if env["API_KEY"] != "secret-value" {
		t.Fatalf("want API_KEY=secret-value, got %q", env["API_KEY"])
	}
}

// ── Deny path ──────────────────────────────────────────────────────────

// TestIssueForMCPSpawn_CedarDeny verifies that an explicit Cedar Deny
// returns ErrMCPSpawnDenied without resolving credentials.
func TestIssueForMCPSpawn_CedarDeny(t *testing.T) {
	s := newStoreWithRegistry(t, denyGate{}, nil)
	backend := newFakeMCPBackend(nil)
	ctx := context.Background()

	_, err := s.IssueForMCPSpawn(ctx, "my-recipe", []string{"API_KEY"}, backend)
	if !errors.Is(err, credstore.ErrMCPSpawnDenied) {
		t.Fatalf("want ErrMCPSpawnDenied, got %v", err)
	}
}

// ── NotApplicable + prompt-Allow path ──────────────────────────────────

// TestIssueForMCPSpawn_NotApplicable_PromptAllow exercises the
// interactive-prompt path when the Cedar gate returns NotApplicable and
// the user picks Allow-once.
func TestIssueForMCPSpawn_NotApplicable_PromptAllow(t *testing.T) {
	reg := cedar.NewRegistry() // no dispatcher — tests drive round-trips directly

	s := newStoreWithRegistry(t, naGate{}, reg)
	backend := newFakeMCPBackend(map[string][]byte{
		locFor("recipe-a", "TOKEN"): []byte("tok-abc"),
	})
	ctx := context.Background()

	var (
		envResult map[string]string
		issueErr  error
	)

	done := make(chan struct{})
	go func() {
		defer close(done)
		envResult, issueErr = s.IssueForMCPSpawn(ctx, "recipe-a", []string{"TOKEN"}, backend)
	}()

	// Drain the pending request and resolve Allow-once.
	deadline := time.Now().Add(5 * time.Second)
	var reqID string
	for time.Now().Before(deadline) {
		pending := reg.ListPending()
		if len(pending) > 0 {
			reqID = pending[0].RequestID
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if reqID == "" {
		t.Fatal("no pending request appeared within 5s")
	}
	if err := reg.Resolve(reqID, cedar.DecisionAllowOnce); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	<-done
	if issueErr != nil {
		t.Fatalf("IssueForMCPSpawn after AllowOnce: %v", issueErr)
	}
	if envResult["TOKEN"] != "tok-abc" {
		t.Fatalf("want TOKEN=tok-abc, got %q", envResult["TOKEN"])
	}
	if reg.PendingCount() != 0 {
		t.Fatalf("registry leaked: %d pending entries", reg.PendingCount())
	}
}

// ── NotApplicable + prompt-Deny path ───────────────────────────────────

// TestIssueForMCPSpawn_NotApplicable_PromptDeny verifies that when the
// interactive prompt user picks Deny, ErrMCPSpawnDenied is returned.
func TestIssueForMCPSpawn_NotApplicable_PromptDeny(t *testing.T) {
	reg := cedar.NewRegistry()

	s := newStoreWithRegistry(t, naGate{}, reg)
	backend := newFakeMCPBackend(nil)
	ctx := context.Background()

	var issueErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, issueErr = s.IssueForMCPSpawn(ctx, "recipe-b", []string{"KEY"}, backend)
	}()

	deadline := time.Now().Add(5 * time.Second)
	var reqID string
	for time.Now().Before(deadline) {
		pending := reg.ListPending()
		if len(pending) > 0 {
			reqID = pending[0].RequestID
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if reqID == "" {
		t.Fatal("no pending request appeared within 5s")
	}
	if err := reg.Resolve(reqID, cedar.DecisionDeny); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	<-done
	if !errors.Is(issueErr, credstore.ErrMCPSpawnDenied) {
		t.Fatalf("want ErrMCPSpawnDenied after prompt-Deny, got %v", issueErr)
	}
}

// ── NotApplicable + no registry (default-allow) ────────────────────────

// TestIssueForMCPSpawn_NotApplicable_NilRegistry verifies that when no
// prompt registry is wired, NotApplicable defaults to allow (pre-boot
// posture).
func TestIssueForMCPSpawn_NotApplicable_NilRegistry(t *testing.T) {
	s := newStoreWithRegistry(t, naGate{}, nil) // nil registry
	backend := newFakeMCPBackend(map[string][]byte{
		locFor("recipe-c", "SK"): []byte("val"),
	})
	ctx := context.Background()

	env, err := s.IssueForMCPSpawn(ctx, "recipe-c", []string{"SK"}, backend)
	if err != nil {
		t.Fatalf("IssueForMCPSpawn with nil registry: %v", err)
	}
	if env["SK"] != "val" {
		t.Fatalf("want SK=val, got %q", env["SK"])
	}
}

// ── Timeout path ───────────────────────────────────────────────────────

// TestIssueForMCPSpawn_PromptTimeout verifies that a short-timeout registry
// returns ErrMCPSpawnDenied when no one resolves the prompt.
func TestIssueForMCPSpawn_PromptTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in -short mode")
	}

	reg := cedar.NewRegistry(cedar.WithTimeout(50 * time.Millisecond))

	s := newStoreWithRegistry(t, naGate{}, reg)
	backend := newFakeMCPBackend(nil)
	ctx := context.Background()

	_, err := s.IssueForMCPSpawn(ctx, "recipe-timeout", []string{"K"}, backend)
	if !errors.Is(err, credstore.ErrMCPSpawnDenied) {
		t.Fatalf("want ErrMCPSpawnDenied on timeout, got %v", err)
	}
	if reg.PendingCount() != 0 {
		t.Fatalf("registry leaked after timeout: %d entries", reg.PendingCount())
	}
}

// TestIssueForMCPSpawn_ResolverFreshPath verifies the future-canonical
// path: when the caller passes nil backend, IssueForMCPSpawn routes
// through s.resolver.ResolveFresh so events emit + cache stays bypassed.
func TestIssueForMCPSpawn_ResolverFreshPath(t *testing.T) {
	resolver := newFakeResolver(map[string][]byte{
		recipes.KeychainLocator("recipe-x", "API_KEY"): []byte("from-resolver"),
	})
	s := credstore.New(resolver, nil)
	t.Cleanup(s.Close)
	ctx := context.Background()

	env, err := s.IssueForMCPSpawn(ctx, "recipe-x", []string{"API_KEY"}, nil)
	if err != nil {
		t.Fatalf("IssueForMCPSpawn (resolver path): unexpected error: %v", err)
	}
	if env["API_KEY"] != "from-resolver" {
		t.Fatalf("want API_KEY=from-resolver, got %q", env["API_KEY"])
	}
}

// TestIssueForMCPSpawn_NilResolverAndBackend ensures the no-source
// guard returns a clear error rather than nil-deref.
func TestIssueForMCPSpawn_NilResolverAndBackend(t *testing.T) {
	s := credstore.New(nil, nil)
	t.Cleanup(s.Close)
	_, err := s.IssueForMCPSpawn(context.Background(), "r", []string{"K"}, nil)
	if err == nil {
		t.Fatal("want error when both resolver and backend are nil")
	}
}
