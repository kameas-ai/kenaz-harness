package bundleartifact

import (
	"context"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

const goodModelProfileYAML = `
id: claude-sonnet-*
version: "2026.07.30"
fallback_chain_id: anthropic-then-openrouter
retry:
  max_attempts: 3
  base_ms: 250
  max_ms: 5000
  jitter: full
`

func newModelProfileStore(t *testing.T) *llm.ModelProfileStore {
	t.Helper()
	return llm.NewModelProfileStore()
}

func TestModelProfileHandler_KindLabel(t *testing.T) {
	h := NewModelProfileHandler(newModelProfileStore(t))
	if h.Kind() != ModelProfileKind {
		t.Fatalf("kind: %s", h.Kind())
	}
	if h.Kind() == Kind {
		t.Fatal("ModelProfileKind must be distinct from the connection-config Kind")
	}
}

func TestModelProfileHandler_ParseValidateActivate_Happy(t *testing.T) {
	store := newModelProfileStore(t)
	h := NewModelProfileHandler(store)

	parsed, err := h.Parse([]byte(goodModelProfileYAML))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Validate(parsed); err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(context.Background(), parsed); err != nil {
		t.Fatal(err)
	}

	got, found, err := store.Resolve("claude-sonnet-4-7-20260420", "2026.07.30")
	if err != nil || !found {
		t.Fatalf("resolve after activate: found=%v err=%v", found, err)
	}
	if got.FallbackChainId != "anthropic-then-openrouter" {
		t.Fatalf("resolved profile: %+v", got)
	}
}

func TestModelProfileHandler_ActivateCollision(t *testing.T) {
	store := newModelProfileStore(t)
	h := NewModelProfileHandler(store)
	parsed, err := h.Parse([]byte(goodModelProfileYAML))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(context.Background(), parsed); err != nil {
		t.Fatal(err)
	}
	if err := h.Activate(context.Background(), parsed); err == nil {
		t.Fatal("expected collision on duplicate Activate")
	}
}

func TestModelProfileHandler_RejectsMissingVersion(t *testing.T) {
	h := NewModelProfileHandler(newModelProfileStore(t))
	parsed, err := h.Parse([]byte("id: some-model\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Validate(parsed); err == nil {
		t.Fatal("expected validation error for missing version")
	}
}

// TestConnectionAndBehavioralProfilesResolveIndependently is the direct
// proof for spec §5 / WP02's central requirement: rotating a credential
// (ProviderProfile via Handler + registry.Registry) must never require
// re-promoting a ModelProfile (via ModelProfileHandler + ModelProfileStore),
// and vice-versa. It activates one of each kind against completely
// separate handlers/stores, then evicts/reloads the ProviderProfile (the
// "credential rotation" seam) and confirms the ModelProfile resolution is
// completely unaffected, and vice versa.
func TestConnectionAndBehavioralProfilesResolveIndependently(t *testing.T) {
	reg := newReg(t) // from handler_test.go: registry.New(registry.Options{})
	connHandler := NewHandler(reg)

	store := newModelProfileStore(t)
	behavioralHandler := NewModelProfileHandler(store)

	// Activate a connection profile (credential/endpoint config).
	connParsed, err := connHandler.Parse([]byte(goodYAML)) // from handler_test.go
	if err != nil {
		t.Fatal(err)
	}
	if err := connHandler.Validate(connParsed); err != nil {
		t.Fatal(err)
	}
	if err := connHandler.Activate(context.Background(), connParsed); err != nil {
		t.Fatal(err)
	}

	// Activate a behavioral profile (prompt/tool-dialect/retry config).
	behavParsed, err := behavioralHandler.Parse([]byte(goodModelProfileYAML))
	if err != nil {
		t.Fatal(err)
	}
	if err := behavioralHandler.Validate(behavParsed); err != nil {
		t.Fatal(err)
	}
	if err := behavioralHandler.Activate(context.Background(), behavParsed); err != nil {
		t.Fatal(err)
	}

	// Sanity: both resolve before any rotation/re-promotion.
	if _, err := reg.Profile("anthropic-default"); err != nil {
		t.Fatalf("connection profile not resolvable: %v", err)
	}
	behav, found, err := store.Resolve("claude-sonnet-4-7-20260420", "2026.07.30")
	if err != nil || !found {
		t.Fatalf("behavioral profile not resolvable: found=%v err=%v", found, err)
	}

	// Simulate a credential rotation: evict + reload the connection
	// profile under the SAME id. This must not touch the
	// ModelProfileStore at all.
	if err := reg.Evict("anthropic-default"); err != nil {
		t.Fatalf("evict connection profile: %v", err)
	}
	if err := connHandler.Activate(context.Background(), connParsed); err != nil {
		t.Fatalf("reactivate connection profile after rotation: %v", err)
	}
	stillBehav, stillFound, err := store.Resolve("claude-sonnet-4-7-20260420", "2026.07.30")
	if err != nil || !stillFound {
		t.Fatalf("behavioral resolution regressed after credential rotation: found=%v err=%v", stillFound, err)
	}
	if stillBehav.FallbackChainId != behav.FallbackChainId {
		t.Fatalf("behavioral profile content changed after unrelated credential rotation: before=%+v after=%+v", behav, stillBehav)
	}

	// Simulate a behavioral re-promotion: evict + reload the model
	// profile under the same (id, version). This must not touch the
	// connection registry at all.
	store.Evict("claude-sonnet-*", "2026.07.30")
	if err := behavioralHandler.Activate(context.Background(), behavParsed); err != nil {
		t.Fatalf("reactivate behavioral profile after re-promotion: %v", err)
	}
	connAfter, err := reg.Profile("anthropic-default")
	if err != nil {
		t.Fatalf("connection resolution regressed after unrelated behavioral re-promotion: %v", err)
	}
	if connAfter.Kind != "anthropic" {
		t.Fatalf("connection profile content changed after unrelated behavioral re-promotion: %+v", connAfter)
	}
}
