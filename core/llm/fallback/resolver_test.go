package fallback

import (
	"context"
	"errors"
	"testing"
)

// fakeStore is a minimal in-memory Store for resolver tests.
type fakeStore struct {
	chains map[string]*Chain
}

func (s *fakeStore) List(_ context.Context) ([]*Chain, error) { return nil, nil }
func (s *fakeStore) Get(_ context.Context, id string) (*Chain, error) {
	if c, ok := s.chains[id]; ok {
		return c, nil
	}
	return nil, nil
}
func (s *fakeStore) Save(_ context.Context, c *Chain) error { return nil }
func (s *fakeStore) Delete(_ context.Context, id string) error { return nil }

func testChain(id string) *Chain {
	return &Chain{
		ID:   id,
		Name: id,
		Entries: []ChainEntry{
			{ProviderID: "p2", Triggers: []TriggerCondition{TriggerErrorAny}},
		},
	}
}

func TestStoreResolver_NodeOverrideWinsOverSessionAndApp(t *testing.T) {
	store := &fakeStore{chains: map[string]*Chain{"node-chain": testChain("node-chain")}}
	r := &StoreResolver{
		Store: store,
		SessionDefault: func(_ context.Context, _ string) (string, error) { return "session-chain", nil },
		AppDefault:     func(_ context.Context) (string, error) { return "app-chain", nil },
	}
	ctx := WithChainIDOverride(context.Background(), "node-chain")
	got, err := r.Resolve(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got == nil || got.ID != "node-chain" {
		t.Fatalf("want node-chain to win, got %+v", got)
	}
}

func TestStoreResolver_SessionDefaultWinsOverApp(t *testing.T) {
	store := &fakeStore{chains: map[string]*Chain{"session-chain": testChain("session-chain")}}
	r := &StoreResolver{
		Store: store,
		SessionDefault: func(_ context.Context, _ string) (string, error) { return "session-chain", nil },
		AppDefault:     func(_ context.Context) (string, error) { return "app-chain", nil },
	}
	got, err := r.Resolve(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got == nil || got.ID != "session-chain" {
		t.Fatalf("want session-chain, got %+v", got)
	}
}

func TestStoreResolver_AppDefaultUsedWhenNoOverrideOrSession(t *testing.T) {
	store := &fakeStore{chains: map[string]*Chain{"app-chain": testChain("app-chain")}}
	r := &StoreResolver{
		Store:          store,
		SessionDefault: func(_ context.Context, _ string) (string, error) { return "", nil },
		AppDefault:     func(_ context.Context) (string, error) { return "app-chain", nil },
	}
	got, err := r.Resolve(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got == nil || got.ID != "app-chain" {
		t.Fatalf("want app-chain, got %+v", got)
	}
}

func TestStoreResolver_NoLevelConfigured_ReturnsNilNil(t *testing.T) {
	r := &StoreResolver{Store: &fakeStore{chains: map[string]*Chain{}}}
	got, err := r.Resolve(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Resolve: unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil chain when nothing configured, got %+v", got)
	}
}

func TestStoreResolver_StoreOverrideWinsOverBundled(t *testing.T) {
	operatorChain := testChain("shared-id")
	operatorChain.Name = "operator override"
	bundledChain := testChain("shared-id")
	bundledChain.Name = "bundled default"

	r := &StoreResolver{
		Store:          &fakeStore{chains: map[string]*Chain{"shared-id": operatorChain}},
		SessionDefault: func(_ context.Context, _ string) (string, error) { return "shared-id", nil },
		bundled:        func() ([]*Chain, error) { return []*Chain{bundledChain}, nil },
	}
	got, err := r.Resolve(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got == nil || got.Name != "operator override" {
		t.Fatalf("want operator-saved chain to win, got %+v", got)
	}
}

func TestStoreResolver_FallsBackToBundledWhenNoStoreEntry(t *testing.T) {
	bundledChain := testChain("bundled-only")
	r := &StoreResolver{
		Store:          &fakeStore{chains: map[string]*Chain{}},
		SessionDefault: func(_ context.Context, _ string) (string, error) { return "bundled-only", nil },
		bundled:        func() ([]*Chain, error) { return []*Chain{bundledChain}, nil },
	}
	got, err := r.Resolve(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got == nil || got.ID != "bundled-only" {
		t.Fatalf("want bundled fallback, got %+v", got)
	}
}

func TestStoreResolver_UnknownChainID_FailsOpenToNoFallback(t *testing.T) {
	r := &StoreResolver{
		Store:          &fakeStore{chains: map[string]*Chain{}},
		SessionDefault: func(_ context.Context, _ string) (string, error) { return "typo-id", nil },
		bundled:        func() ([]*Chain, error) { return nil, nil },
	}
	got, err := r.Resolve(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Resolve: want fail-open (nil, nil) on unknown id, got error: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil chain for unresolvable id, got %+v", got)
	}
}

func TestStoreResolver_SessionDefaultErrorPropagates(t *testing.T) {
	wantErr := errors.New("session lookup boom")
	r := &StoreResolver{
		Store:          &fakeStore{chains: map[string]*Chain{}},
		SessionDefault: func(_ context.Context, _ string) (string, error) { return "", wantErr },
	}
	_, err := r.Resolve(context.Background(), "sess-1")
	if err == nil {
		t.Fatal("expected error to propagate from SessionDefault")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("want wrapped %v, got %v", wantErr, err)
	}
}

func TestChainIDOverrideFromContext_EmptyStringIsNoOp(t *testing.T) {
	ctx := WithChainIDOverride(context.Background(), "")
	if _, ok := ChainIDOverrideFromContext(ctx); ok {
		t.Fatal("want empty-string override to be a no-op")
	}
}
