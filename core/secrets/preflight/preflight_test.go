package preflight_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/secrets/events"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/preflight"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/registry"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/secret"
)

type fakeBackend struct {
	kind     registry.BackendKind
	supports []ref.RefKind
	fail     map[string]error
	health   registry.HealthStatus
}

func (f *fakeBackend) Kind() registry.BackendKind         { return f.kind }
func (f *fakeBackend) SupportedRefKinds() []ref.RefKind   { return f.supports }
func (f *fakeBackend) Health(_ context.Context) registry.BackendHealth {
	return registry.BackendHealth{Status: f.health, LastChecked: time.Now()}
}
func (f *fakeBackend) Resolve(_ context.Context, r ref.CredentialReference) (secret.Secret, error) {
	if e, ok := f.fail[r.Locator]; ok {
		return nil, e
	}
	return secret.NewStdlibSecret([]byte("v"), r.ID(), r.ConsumerID), nil
}

func newRegistry(t *testing.T, b registry.Backend) *registry.Registry {
	t.Helper()
	r := registry.New()
	if err := r.Register(b); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPreFlight_AllResolvable(t *testing.T) {
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		health:   registry.HealthOK,
	}
	r := newRegistry(t, b)
	log := events.NewBufferingLogger()
	v := preflight.New(r, log)

	refs := []ref.CredentialReference{
		{Kind: ref.RefEnv, Locator: "A"},
		{Kind: ref.RefEnv, Locator: "B"},
	}
	results, err := v.Run(context.Background(), refs)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("results=%d", len(results))
	}
	for _, res := range results {
		if res.Status != preflight.StatusResolvable {
			t.Errorf("status=%v", res.Status)
		}
	}
	if log.Len() != 2 {
		t.Errorf("expected 2 events, got %d", log.Len())
	}
}

func TestPreFlight_RequiredFails(t *testing.T) {
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		fail:     map[string]error{"BAD": errors.New("not found: BAD")},
		health:   registry.HealthOK,
	}
	r := newRegistry(t, b)
	v := preflight.New(r, nil)
	refs := []ref.CredentialReference{
		{Kind: ref.RefEnv, Locator: "OK"},
		{Kind: ref.RefEnv, Locator: "BAD"},
	}
	results, err := v.Run(context.Background(), refs)
	if !errors.Is(err, preflight.ErrPreflightFailed) {
		t.Errorf("got err=%v want ErrPreflightFailed", err)
	}
	if results[1].Status != preflight.StatusUnresolvable {
		t.Errorf("got status=%v", results[1].Status)
	}
	if results[1].ErrorCode != "not_found" {
		t.Errorf("ErrorCode=%q", results[1].ErrorCode)
	}
}

func TestPreFlight_OptionalDoesNotFail(t *testing.T) {
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		fail:     map[string]error{"OPT": errors.New("not found: OPT")},
		health:   registry.HealthOK,
	}
	r := newRegistry(t, b)
	v := preflight.New(r, nil)
	refs := []ref.CredentialReference{
		{Kind: ref.RefEnv, Locator: "OPT", Optional: true},
	}
	results, err := v.Run(context.Background(), refs)
	if err != nil {
		t.Errorf("optional should not fail Run, got %v", err)
	}
	if results[0].Status != preflight.StatusOptionalUnresolvable {
		t.Errorf("status=%v", results[0].Status)
	}
}

func TestPreFlight_NoBackend(t *testing.T) {
	r := registry.New()
	v := preflight.New(r, nil)
	refs := []ref.CredentialReference{{Kind: ref.RefEnv, Locator: "X"}}
	results, err := v.Run(context.Background(), refs)
	if !errors.Is(err, preflight.ErrPreflightFailed) {
		t.Errorf("expected ErrPreflightFailed, got %v", err)
	}
	if results[0].ErrorCode != "no_backend" {
		t.Errorf("ErrorCode=%q", results[0].ErrorCode)
	}
}

func TestPreFlight_BackendDegraded(t *testing.T) {
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		health:   registry.HealthDegraded,
	}
	r := newRegistry(t, b)
	v := preflight.New(r, nil)
	refs := []ref.CredentialReference{{Kind: ref.RefEnv, Locator: "X"}}
	results, err := v.Run(context.Background(), refs)
	if err != nil {
		t.Errorf("degraded should not fail, got %v", err)
	}
	if results[0].Status != preflight.StatusBackendDegraded {
		t.Errorf("status=%v", results[0].Status)
	}
}

func TestPreFlight_BackendUnavailableFailsRequired(t *testing.T) {
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		health:   registry.HealthUnavailable,
	}
	r := newRegistry(t, b)
	v := preflight.New(r, nil)
	refs := []ref.CredentialReference{{Kind: ref.RefEnv, Locator: "X"}}
	results, err := v.Run(context.Background(), refs)
	if !errors.Is(err, preflight.ErrPreflightFailed) {
		t.Errorf("expected ErrPreflightFailed, got %v", err)
	}
	if results[0].Status != preflight.StatusUnresolvable {
		t.Errorf("status=%v", results[0].Status)
	}
	if results[0].ErrorCode != "backend_unavailable" {
		t.Errorf("ErrorCode=%q", results[0].ErrorCode)
	}
}

func TestPreFlight_NilLoggerTolerated(t *testing.T) {
	// Cycle-break path: nil logger must not panic.
	b := &fakeBackend{
		kind:     "fake",
		supports: []ref.RefKind{ref.RefEnv},
		health:   registry.HealthOK,
	}
	r := newRegistry(t, b)
	v := preflight.New(r, nil)
	_, err := v.Run(context.Background(), []ref.CredentialReference{{Kind: ref.RefEnv, Locator: "X"}})
	if err != nil {
		t.Fatal(err)
	}
}
