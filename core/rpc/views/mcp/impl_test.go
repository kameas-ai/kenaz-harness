package mcp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
)

type stubRegistry struct {
	servers []Server
	err     error
}

func (s stubRegistry) List(_ context.Context) ([]Server, error) { return s.servers, s.err }

type recordingSubscriber struct {
	mu      sync.Mutex
	subs    map[string]<-chan any
	stopped []string
}

func newRecordingSubscriber() *recordingSubscriber {
	return &recordingSubscriber{subs: map[string]<-chan any{}}
}

func (r *recordingSubscriber) Subscribe(_ context.Context, view, _ string, source <-chan any) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := view + "-" + time.Now().Format("150405.000000")
	r.subs[id] = source
	return id, nil
}

func (r *recordingSubscriber) Unsubscribe(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, id)
	r.stopped = append(r.stopped, id)
	return nil
}

func TestListServers_NoRegistry_ReturnsEmpty(t *testing.T) {
	api := NewAPI()
	got, err := api.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nil registry path should yield empty slice, got %d", len(got))
	}
}

func TestListServers_RegistrySorted(t *testing.T) {
	reg := stubRegistry{servers: []Server{
		{ID: "z", Name: "z-server", State: "ready", Version: "1.0.0", Transport: "stdio"},
		{ID: "a", Name: "a-server", State: "connecting", Version: "0.9.0", Transport: "ws"},
	}}
	api := NewAPI(WithRegistry(reg))
	got, err := api.ListServers(context.Background())
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Name != "a-server" || got[1].Name != "z-server" {
		t.Errorf("expected sort by name, got [%s, %s]", got[0].Name, got[1].Name)
	}
}

func TestStartStream_NoBroker_NoOp(t *testing.T) {
	api := NewAPI()
	id, err := api.StartStream(context.Background(), "any")
	if err != nil {
		t.Fatalf("StartStream without broker should not error, got %v", err)
	}
	if id != "" {
		t.Errorf("StartStream without broker should return empty id, got %q", id)
	}
	if err := api.StopStream(context.Background(), "any"); err != nil {
		t.Errorf("StopStream without broker should not error, got %v", err)
	}
}

func TestPublish_FansToSubscribers(t *testing.T) {
	rec := newRecordingSubscriber()
	api := NewAPI(WithSubscriber(rec))

	id, err := api.StartStream(context.Background(), "srv-1")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	rec.mu.Lock()
	src := rec.subs[id]
	rec.mu.Unlock()
	if src == nil {
		t.Fatalf("subscriber missing source channel")
	}

	api.Publish(map[string]any{"name": "tools.invoked"})

	select {
	case ev := <-src:
		m, ok := ev.(map[string]any)
		if !ok || m["name"] != "tools.invoked" {
			t.Errorf("unexpected event payload %v", ev)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("did not receive published event")
	}

	if err := api.StopStream(context.Background(), id); err != nil {
		t.Fatalf("StopStream: %v", err)
	}
	if len(rec.stopped) != 1 {
		t.Errorf("expected 1 unsubscribe, got %d", len(rec.stopped))
	}
}

// =====================================================================
// WP10 — Cedar gate tests.
// =====================================================================

// stubRecipeStore records Save/Delete calls for assertion.
type stubRecipeStore struct {
	mu      sync.Mutex
	saved   []recipes.Recipe
	deleted []string
	saveErr error
}

func (s *stubRecipeStore) Save(r recipes.Recipe) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, r)
	return nil
}
func (s *stubRecipeStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, id)
	return nil
}

// recordingAudit records emitted events.
type recordingAudit struct {
	mu     sync.Mutex
	events []struct{ kind string; attrs map[string]any }
}

func (r *recordingAudit) Emit(_ context.Context, kind string, attrs map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, struct{ kind string; attrs map[string]any }{kind, attrs})
}

// denyGate is a cedar.Gate stub that always denies.
type denyGate struct{}

func (denyGate) Evaluate(_ context.Context, principal cedarlib.EntityUID, action string, resource cedarlib.EntityUID, _ map[cedarlib.String]cedarlib.Value) cedar.Decision {
	return cedar.Decision{
		Outcome:   cedar.Deny,
		Action:    action,
		Principal: principal.String(),
		Resource:  resource.String(),
		Reason:    "forbid policy matched",
	}
}

// TestAddRecipe_CedarDeny verifies that AddRecipe returns a
// *cedar.PolicyDeniedError when the gate denies and does NOT persist.
func TestAddRecipe_CedarDeny(t *testing.T) {
	store := &stubRecipeStore{}
	api := NewAPI(
		WithRecipeStore(store),
		WithGate(denyGate{}),
	)
	r := recipes.Recipe{
		ID:          "my-recipe",
		DisplayName: "My Recipe",
		Command:     []string{"npx", "-y", "@myorg/mcp-server"},
	}
	err := api.AddRecipe(context.Background(), r)
	if err == nil {
		t.Fatal("AddRecipe with deny gate should return error")
	}
	if !cedar.IsPolicyDenied(err) {
		t.Fatalf("expected *cedar.PolicyDeniedError, got %T: %v", err, err)
	}
	store.mu.Lock()
	saved := len(store.saved)
	store.mu.Unlock()
	if saved != 0 {
		t.Errorf("no recipe should be saved on Cedar deny, got %d", saved)
	}
}

// TestAddRecipe_NoGate_Permits verifies that AddRecipe succeeds when no
// gate is wired (default-permit).
func TestAddRecipe_NoGate_Permits(t *testing.T) {
	store := &stubRecipeStore{}
	audit := &recordingAudit{}
	api := NewAPI(
		WithRecipeStore(store),
		WithAudit(audit),
		// No WithGate — nil gate = permit.
	)
	r := recipes.Recipe{
		ID:          "github",
		DisplayName: "GitHub",
		Command:     []string{"npx", "-y", "@modelcontextprotocol/server-github"},
	}
	if err := api.AddRecipe(context.Background(), r); err != nil {
		t.Fatalf("AddRecipe with nil gate should permit: %v", err)
	}
	store.mu.Lock()
	saved := len(store.saved)
	store.mu.Unlock()
	if saved != 1 {
		t.Errorf("expected 1 saved recipe, got %d", saved)
	}
	audit.mu.Lock()
	evLen := len(audit.events)
	audit.mu.Unlock()
	if evLen != 1 {
		t.Errorf("expected 1 audit event, got %d", evLen)
	}
}

// TestRemoveRecipe_EmitsAudit verifies RemoveRecipe emits mcp.recipe.removed.
func TestRemoveRecipe_EmitsAudit(t *testing.T) {
	store := &stubRecipeStore{}
	audit := &recordingAudit{}
	api := NewAPI(
		WithRecipeStore(store),
		WithAudit(audit),
	)
	if err := api.RemoveRecipe(context.Background(), "github"); err != nil {
		t.Fatalf("RemoveRecipe: %v", err)
	}
	store.mu.Lock()
	deleted := store.deleted
	store.mu.Unlock()
	if len(deleted) != 1 || deleted[0] != "github" {
		t.Errorf("expected Delete(\"github\"), got %v", deleted)
	}
	audit.mu.Lock()
	evs := audit.events
	audit.mu.Unlock()
	if len(evs) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(evs))
	}
	if evs[0].kind != "mcp.recipe.removed" {
		t.Errorf("expected kind mcp.recipe.removed, got %q", evs[0].kind)
	}
}

// TestAddRecipe_NoStore_Error verifies AddRecipe returns ErrUserRecipesDisabled
// when no RecipeStore is wired.
func TestAddRecipe_NoStore_Error(t *testing.T) {
	api := NewAPI() // no store
	r := recipes.Recipe{
		ID:          "github",
		DisplayName: "GitHub",
		Command:     []string{"npx", "-y", "@modelcontextprotocol/server-github"},
	}
	err := api.AddRecipe(context.Background(), r)
	if err == nil {
		t.Fatal("AddRecipe without store should error")
	}
	if !errors.Is(err, recipes.ErrUserRecipesDisabled) {
		t.Fatalf("expected ErrUserRecipesDisabled, got %v", err)
	}
}
