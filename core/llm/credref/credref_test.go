package credref

import (
	"context"
	"errors"
	"os"
	"testing"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

func TestResolve_EnvHappyPath(t *testing.T) {
	const key = "TEST_LLM_API_KEY_HAPPY"
	os.Setenv(key, "secret-value")
	defer os.Unsetenv(key)

	r := New(secrets.NewMemoryBackend())
	s, err := r.Resolve(context.Background(), "p1", llm.CredentialReference{Kind: "env", Locator: key})
	if err != nil {
		t.Fatal(err)
	}
	if string(s.Bytes()) != "secret-value" {
		t.Fatalf("unexpected bytes: %q", s.Bytes())
	}
	// Zeroize and re-read; bytes should be cleared.
	Zeroize(s)
	if got := s.Bytes(); got != nil {
		t.Fatalf("expected nil after zeroize, got %q", got)
	}
}

func TestResolve_MissingEnvReturnsTypedError(t *testing.T) {
	r := New(secrets.NewMemoryBackend())
	_, err := r.Resolve(context.Background(), "p1", llm.CredentialReference{Kind: "env", Locator: "DEFINITELY_NOT_SET_KEY"})
	if err == nil {
		t.Fatal("expected error")
	}
	var crErr *llm.ErrCredentialResolution
	if !errors.As(err, &crErr) {
		t.Fatalf("expected ErrCredentialResolution, got %T: %v", err, err)
	}
	if crErr.ProfileID != "p1" || crErr.Ref.Kind != "env" {
		t.Fatalf("error fields: %+v", crErr)
	}
}

func TestResolve_KeychainMissingReturnsTypedError(t *testing.T) {
	r := New(secrets.NewMemoryBackend())
	_, err := r.Resolve(context.Background(), "p1", llm.CredentialReference{Kind: "keychain", Locator: "no-such-entry"})
	if err == nil {
		t.Fatal("expected error")
	}
	var crErr *llm.ErrCredentialResolution
	if !errors.As(err, &crErr) {
		t.Fatalf("expected ErrCredentialResolution, got %v", err)
	}
}

func TestPreflight_ReportsPerProfile(t *testing.T) {
	const key = "TEST_LLM_API_KEY_PRE"
	os.Setenv(key, "v")
	defer os.Unsetenv(key)

	r := New(secrets.NewMemoryBackend())
	profs := []llm.ProviderProfile{
		{ID: "ok", Kind: "anthropic", Cred: llm.CredentialReference{Kind: "env", Locator: key}},
		{ID: "missing", Kind: "openai", Cred: llm.CredentialReference{Kind: "env", Locator: "TEST_NO_SUCH_KEY_PRE"}},
	}
	results := r.Preflight(context.Background(), profs)
	if len(results) != 2 {
		t.Fatalf("expected 2 results")
	}
	if !results[0].Resolved || results[1].Resolved {
		t.Fatalf("unexpected resolution flags: %+v", results)
	}
	if results[1].Err == nil {
		t.Fatalf("missing should have err")
	}
}

func TestZeroize_NilSafe(t *testing.T) {
	Zeroize(nil) // must not panic
}
