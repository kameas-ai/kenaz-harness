package credstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/credstore"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
	"github.com/kameas-ai/kenaz-harness/core/secrets/ref"
	"github.com/kameas-ai/kenaz-harness/core/secrets/secret"
)

// fakeResolver is a ResolverAPI used in tests. It returns a
// StdlibSecret wrapping a copy of the registered bytes.
type fakeResolver struct {
	data map[string][]byte // keyed by locator
}

func newFakeResolver(entries map[string][]byte) secrets.ResolverAPI {
	return &fakeResolver{data: entries}
}

func (r *fakeResolver) Resolve(_ context.Context, cred secrets.CredentialReference, _ string) (secrets.Secret, error) {
	b, ok := r.data[cred.Locator]
	if !ok {
		return nil, errors.New("fake: not found: " + cred.Locator)
	}
	dup := make([]byte, len(b))
	copy(dup, b)
	return secret.NewStdlibSecret(dup, cred.ID(), cred.ConsumerID), nil
}

// ResolveFresh delegates to Resolve for the test seam — fakeResolver has
// no cache, so cache-bypass is the same as a regular call.
func (r *fakeResolver) ResolveFresh(ctx context.Context, cred secrets.CredentialReference, consumerID string) (secrets.Secret, error) {
	return r.Resolve(ctx, cred, consumerID)
}

// validRef returns a simple RefEnv CredentialReference.
func validRef(locator string) secrets.CredentialReference {
	return secrets.CredentialReference{Kind: ref.RefEnv, Locator: locator}
}

// newStore returns a Store backed by the given fake clock and resolver.
func newStore(t *testing.T, resolver secrets.ResolverAPI, clock func() time.Time) credstore.Store {
	t.Helper()
	s := credstore.New(resolver, clock)
	t.Cleanup(s.Close)
	return s
}

// ── Issue ─────────────────────────────────────────────────────────────

func TestIssue_EmptyRef(t *testing.T) {
	s := newStore(t, newFakeResolver(nil), nil)
	ctx := context.Background()
	_, err := s.Issue(ctx, secrets.CredentialReference{}, "test")
	if !errors.Is(err, credstore.ErrEmptyCredential) {
		t.Fatalf("want ErrEmptyCredential, got %v", err)
	}
}

func TestIssue_DefaultTTL(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	resolver := newFakeResolver(map[string][]byte{"MY_KEY": []byte("secret")})
	s := newStore(t, resolver, clock)
	ctx := context.Background()

	h, err := s.Issue(ctx, validRef("MY_KEY"), "test")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if h == "" {
		t.Fatal("empty handle returned")
	}
}

func TestIssue_WithTTL(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	resolver := newFakeResolver(map[string][]byte{"K": []byte("v")})
	s := newStore(t, resolver, clock)
	ctx := context.Background()

	h, err := s.Issue(ctx, validRef("K"), "test", credstore.WithTTL(5*time.Second))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if h == "" {
		t.Fatal("empty handle")
	}
}

// ── Use ───────────────────────────────────────────────────────────────

func TestUse_HappyPath(t *testing.T) {
	resolver := newFakeResolver(map[string][]byte{"K": []byte("hello")})
	s := newStore(t, resolver, nil)
	ctx := context.Background()

	h, err := s.Issue(ctx, validRef("K"), "round-trip")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	var got []byte
	err = s.Use(ctx, h, func(b []byte) error {
		got = make([]byte, len(b))
		copy(got, b)
		return nil
	})
	if err != nil {
		t.Fatalf("Use: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("want 'hello', got %q", got)
	}
}

func TestUse_UnknownHandle(t *testing.T) {
	s := newStore(t, newFakeResolver(nil), nil)
	ctx := context.Background()
	err := s.Use(ctx, credstore.Handle("ch_bogus"), func([]byte) error { return nil })
	if !errors.Is(err, credstore.ErrUnknownHandle) {
		t.Fatalf("want ErrUnknownHandle, got %v", err)
	}
}

func TestUse_UAfterFree(t *testing.T) {
	resolver := newFakeResolver(map[string][]byte{"K": []byte("val")})
	s := newStore(t, resolver, nil)
	ctx := context.Background()

	h, err := s.Issue(ctx, validRef("K"), "uaf-test")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// First use succeeds.
	if err := s.Use(ctx, h, func([]byte) error { return nil }); err != nil {
		t.Fatalf("first Use: %v", err)
	}

	// Second use must return ErrUseAfterFree.
	err = s.Use(ctx, h, func([]byte) error { return nil })
	if !errors.Is(err, credstore.ErrUseAfterFree) {
		t.Fatalf("want ErrUseAfterFree, got %v", err)
	}
}

func TestUse_ExpiredHandle(t *testing.T) {
	var nowMu time.Time
	nowMu = time.Now()
	clock := func() time.Time { return nowMu }
	resolver := newFakeResolver(map[string][]byte{"K": []byte("val")})
	s := newStore(t, resolver, clock)
	ctx := context.Background()

	h, err := s.Issue(ctx, validRef("K"), "expire-test", credstore.WithTTL(1*time.Second))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Advance the fake clock past the TTL.
	nowMu = nowMu.Add(2 * time.Second)

	err = s.Use(ctx, h, func([]byte) error { return nil })
	if !errors.Is(err, credstore.ErrHandleExpired) {
		t.Fatalf("want ErrHandleExpired, got %v", err)
	}
}
