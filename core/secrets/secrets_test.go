package secrets_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/secrets"
	"github.com/kameas-ai/kenaz-harness/core/secrets/cache"
	"github.com/kameas-ai/kenaz-harness/core/secrets/events"
	"github.com/kameas-ai/kenaz-harness/core/secrets/ref"
	"github.com/kameas-ai/kenaz-harness/core/secrets/registry"
	"github.com/kameas-ai/kenaz-harness/core/secrets/secret"
)

type fakeBackend struct {
	kind     registry.BackendKind
	supports []ref.RefKind
	value    map[string][]byte
	err      map[string]error
	health   registry.HealthStatus
	calls    int
}

func (f *fakeBackend) Kind() registry.BackendKind         { return f.kind }
func (f *fakeBackend) SupportedRefKinds() []ref.RefKind   { return f.supports }
func (f *fakeBackend) Health(_ context.Context) registry.BackendHealth {
	return registry.BackendHealth{Status: f.health, LastChecked: time.Now()}
}
func (f *fakeBackend) Resolve(_ context.Context, r ref.CredentialReference) (secret.Secret, error) {
	f.calls++
	if e, ok := f.err[r.Locator]; ok {
		return nil, e
	}
	v := f.value[r.Locator]
	cp := append([]byte(nil), v...)
	return secret.NewStdlibSecret(cp, r.ID(), r.ConsumerID), nil
}

func makeResolver(t *testing.T, b *fakeBackend) (*secrets.Resolver, *events.BufferingLogger) {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(b); err != nil {
		t.Fatal(err)
	}
	log := events.NewBufferingLogger()
	r, err := secrets.NewResolver(secrets.ResolverConfig{
		Registry: reg,
		Cache:    cache.New(),
		Logger:   log,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, log
}

func TestResolver_ResolveAndCache(t *testing.T) {
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		value:    map[string][]byte{"X": []byte("hunter2")},
		health:   registry.HealthOK,
	}
	r, log := makeResolver(t, b)
	cred := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}

	s1, err := r.Resolve(context.Background(), cred, "consumer-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Use(func(v []byte) error {
		if string(v) != "hunter2" {
			t.Errorf("got %q", v)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Second resolve should hit cache; backend should NOT be called.
	_, err = r.Resolve(context.Background(), cred, "consumer-a")
	if err != nil {
		t.Fatal(err)
	}
	if b.calls != 1 {
		t.Errorf("expected 1 backend call, got %d", b.calls)
	}

	// Drain events; check that no event payload contains the value.
	drained := log.Drain()
	for _, e := range drained {
		js := []byte(e.ReferenceID + "|" + e.ReferenceKind + "|" + e.Outcome)
		if strings.Contains(string(js), "hunter2") {
			t.Errorf("event leaked value: %v", e)
		}
	}
}

func TestResolver_ResolveBackendError(t *testing.T) {
	sentinel := errors.New("not found")
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		err:      map[string]error{"X": sentinel},
		health:   registry.HealthOK,
	}
	r, _ := makeResolver(t, b)
	cred := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	_, err := r.Resolve(context.Background(), cred, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected wrapped sentinel, got %v", err)
	}
}

func TestResolver_NoBackend(t *testing.T) {
	reg := registry.New()
	r, err := secrets.NewResolver(secrets.ResolverConfig{Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, err = r.Resolve(context.Background(), ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}, "")
	if !errors.Is(err, registry.ErrNoBackend) {
		t.Errorf("expected ErrNoBackend, got %v", err)
	}
}

func TestResolver_NilLogger_NoPanic(t *testing.T) {
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		value:    map[string][]byte{"X": []byte("v")},
		health:   registry.HealthOK,
	}
	reg := registry.New()
	if err := reg.Register(b); err != nil {
		t.Fatal(err)
	}
	r, err := secrets.NewResolver(secrets.ResolverConfig{Registry: reg, Logger: nil})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, err = r.Resolve(context.Background(), ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}, "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestResolver_RequiresRegistry(t *testing.T) {
	_, err := secrets.NewResolver(secrets.ResolverConfig{})
	if err == nil {
		t.Error("expected error when Registry is nil")
	}
}

func TestResolver_PreFlight(t *testing.T) {
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		value:    map[string][]byte{"OK": []byte("v")},
		err:      map[string]error{"BAD": errors.New("not found")},
		health:   registry.HealthOK,
	}
	r, _ := makeResolver(t, b)
	res, err := r.PreFlight(context.Background(), []ref.CredentialReference{
		{Kind: ref.RefEnv, Locator: "OK"},
		{Kind: ref.RefEnv, Locator: "BAD"},
	})
	if !errors.Is(err, secrets.ErrPreflightFailed) {
		t.Errorf("expected ErrPreflightFailed, got %v", err)
	}
	if len(res) != 2 {
		t.Errorf("results=%d", len(res))
	}
}

func TestResolver_HealthMap(t *testing.T) {
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		health:   registry.HealthDegraded,
	}
	r, _ := makeResolver(t, b)
	h := r.Health(context.Background())
	if h["fake"].Status != registry.HealthDegraded {
		t.Errorf("got %v", h["fake"].Status)
	}
}

func TestResolver_InvalidateAndRotation(t *testing.T) {
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		value:    map[string][]byte{"X": []byte("v")},
		health:   registry.HealthOK,
	}
	r, _ := makeResolver(t, b)
	cred := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	_, _ = r.Resolve(context.Background(), cred, "")
	if b.calls != 1 {
		t.Fatalf("calls=%d", b.calls)
	}
	r.Invalidate(cred)
	_, _ = r.Resolve(context.Background(), cred, "")
	if b.calls != 2 {
		t.Errorf("Invalidate did not force re-resolve, calls=%d", b.calls)
	}

	r.NotifyRotation(context.Background(), cred)
	// Notify is async; allow time to drain.
	time.Sleep(20 * time.Millisecond)
	_, _ = r.Resolve(context.Background(), cred, "")
	if b.calls < 3 {
		t.Errorf("rotation should force re-resolve eventually, calls=%d", b.calls)
	}
}
