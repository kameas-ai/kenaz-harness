package registry_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/registry"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/secret"
)

// fakeBackend is a deliberately tiny test double. It lives in the test
// package only — it does not import any real SDK, demonstrating the
// NFR-005 contract that a backend can be added without touching
// consumer code.
type fakeBackend struct {
	kind     registry.BackendKind
	supports []ref.RefKind
	value    []byte
	err      error
	resolved int
	health   registry.HealthStatus
}

func (f *fakeBackend) Kind() registry.BackendKind         { return f.kind }
func (f *fakeBackend) SupportedRefKinds() []ref.RefKind   { return f.supports }
func (f *fakeBackend) Health(_ context.Context) registry.BackendHealth {
	return registry.BackendHealth{Status: f.health, LastChecked: time.Now()}
}
func (f *fakeBackend) Resolve(_ context.Context, r ref.CredentialReference) (secret.Secret, error) {
	f.resolved++
	if f.err != nil {
		return nil, f.err
	}
	cp := append([]byte(nil), f.value...)
	return secret.NewStdlibSecret(cp, r.ID(), r.ConsumerID), nil
}

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := registry.New()
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		value:    []byte("v"),
		health:   registry.HealthOK,
	}
	if err := r.Register(b); err != nil {
		t.Fatal(err)
	}
	got, err := r.Lookup(ref.RefEnv)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind() != "fake" {
		t.Errorf("Kind=%q", got.Kind())
	}
}

func TestRegistry_DuplicateRefKind(t *testing.T) {
	r := registry.New()
	a := &fakeBackend{kind: "a", supports: []ref.RefKind{ref.RefEnv}}
	b := &fakeBackend{kind: "b", supports: []ref.RefKind{ref.RefEnv}}
	if err := r.Register(a); err != nil {
		t.Fatal(err)
	}
	err := r.Register(b)
	if !errors.Is(err, registry.ErrDuplicateRefKind) {
		t.Errorf("got %v want ErrDuplicateRefKind", err)
	}
}

func TestRegistry_DuplicateBackendKind(t *testing.T) {
	r := registry.New()
	a := &fakeBackend{kind: "a", supports: []ref.RefKind{ref.RefEnv}}
	b := &fakeBackend{kind: "a", supports: []ref.RefKind{ref.RefFile}}
	if err := r.Register(a); err != nil {
		t.Fatal(err)
	}
	err := r.Register(b)
	if !errors.Is(err, registry.ErrDuplicateBackendKind) {
		t.Errorf("got %v want ErrDuplicateBackendKind", err)
	}
}

func TestRegistry_NilBackend(t *testing.T) {
	r := registry.New()
	if err := r.Register(nil); !errors.Is(err, registry.ErrBackendNil) {
		t.Errorf("got %v want ErrBackendNil", err)
	}
}

func TestRegistry_EmptyKindRejected(t *testing.T) {
	r := registry.New()
	b := &fakeBackend{kind: "", supports: []ref.RefKind{ref.RefEnv}}
	if err := r.Register(b); err == nil {
		t.Error("expected error on empty kind")
	}
}

func TestRegistry_NoSupportedKindsRejected(t *testing.T) {
	r := registry.New()
	b := &fakeBackend{kind: "x", supports: nil}
	if err := r.Register(b); err == nil {
		t.Error("expected error when no ref kinds advertised")
	}
}

func TestRegistry_RefUnknownRejected(t *testing.T) {
	r := registry.New()
	b := &fakeBackend{kind: "x", supports: []ref.RefKind{ref.RefUnknown}}
	if err := r.Register(b); err == nil {
		t.Error("expected error on RefUnknown")
	}
}

func TestRegistry_LookupMiss(t *testing.T) {
	r := registry.New()
	_, err := r.Lookup(ref.RefEnv)
	if !errors.Is(err, registry.ErrNoBackend) {
		t.Errorf("got %v want ErrNoBackend", err)
	}
	_, err = r.LookupByKind("nope")
	if !errors.Is(err, registry.ErrNoBackend) {
		t.Errorf("LookupByKind got %v want ErrNoBackend", err)
	}
}

func TestRegistry_Dispatch(t *testing.T) {
	r := registry.New()
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		value:    []byte("hello"),
	}
	if err := r.Register(b); err != nil {
		t.Fatal(err)
	}
	cred := ref.CredentialReference{Kind: ref.RefEnv, Locator: "MY_VAR"}
	s, used, err := r.Dispatch(context.Background(), cred)
	if err != nil {
		t.Fatal(err)
	}
	if used.Kind() != "fake" {
		t.Errorf("dispatched to %q", used.Kind())
	}
	if b.resolved != 1 {
		t.Errorf("resolved=%d", b.resolved)
	}
	defer s.Destroy()
	if err := s.Use(func(v []byte) error {
		if string(v) != "hello" {
			t.Errorf("got %q", v)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRegistry_DispatchUnknown(t *testing.T) {
	r := registry.New()
	cred := ref.CredentialReference{Kind: ref.RefEnv, Locator: "X"}
	_, _, err := r.Dispatch(context.Background(), cred)
	if !errors.Is(err, registry.ErrNoBackend) {
		t.Errorf("got %v", err)
	}
}

func TestRegistry_ListAndKinds(t *testing.T) {
	r := registry.New()
	b1 := &fakeBackend{kind: "a", supports: []ref.RefKind{ref.RefEnv}}
	b2 := &fakeBackend{kind: "b", supports: []ref.RefKind{ref.RefFile}}
	if err := r.Register(b1); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(b2); err != nil {
		t.Fatal(err)
	}
	list := r.List()
	if len(list) != 2 || list[0].Kind() != "a" || list[1].Kind() != "b" {
		t.Errorf("List=%v", list)
	}
	kinds := r.Kinds()
	if len(kinds) != 2 || kinds[0] != "a" || kinds[1] != "b" {
		t.Errorf("Kinds=%v", kinds)
	}
}

func TestRegistry_ConcurrentRegister(t *testing.T) {
	r := registry.New()
	var wg sync.WaitGroup
	for i, k := range []ref.RefKind{ref.RefEnv, ref.RefFile, ref.RefKeychain, ref.RefAWSProfile} {
		wg.Add(1)
		go func(i int, k ref.RefKind) {
			defer wg.Done()
			b := &fakeBackend{
				kind:     registry.BackendKind([]string{"a", "b", "c", "d"}[i]),
				supports: []ref.RefKind{k},
			}
			if err := r.Register(b); err != nil {
				t.Errorf("Register %d: %v", i, err)
			}
		}(i, k)
	}
	wg.Wait()
	if len(r.List()) != 4 {
		t.Errorf("expected 4, got %d", len(r.List()))
	}
}
