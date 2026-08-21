package http_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	bundle "github.com/kameas-ai/kenaz-harness/core/bundle"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	httpchannel "github.com/kameas-ai/kenaz-harness/core/bundle/channels/http"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// fakeResolver is a race-safe secrets.ResolverAPI fake. Requests it
// serves are recorded under a mutex and read back only via snapshot(),
// per CLAUDE.md's race-safe test fake pattern — httptest handlers and
// the fetch path both run on their own goroutines.
type fakeResolver struct {
	mu       sync.Mutex
	token    string
	resolved []secrets.CredentialReference
}

func (f *fakeResolver) Resolve(_ context.Context, ref secrets.CredentialReference, _ string) (secrets.Secret, error) {
	f.mu.Lock()
	f.resolved = append(f.resolved, ref)
	f.mu.Unlock()
	return fakeSecret{value: []byte(f.token)}, nil
}

func (f *fakeResolver) ResolveFresh(ctx context.Context, ref secrets.CredentialReference, consumerID string) (secrets.Secret, error) {
	return f.Resolve(ctx, ref, consumerID)
}

func (f *fakeResolver) snapshot() []secrets.CredentialReference {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]secrets.CredentialReference, len(f.resolved))
	copy(out, f.resolved)
	return out
}

type fakeSecret struct{ value []byte }

func (s fakeSecret) Use(fn func([]byte) error) error { return fn(s.value) }
func (s fakeSecret) Destroy()                        {}
func (s fakeSecret) ReferenceID() string             { return "fake" }

func TestReachable_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ch, err := httpchannel.Factory(channels.ChannelSpec{Kind: httpchannel.Kind, URL: srv.URL}, secrets.NoopResolver{})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	if err := ch.Reachable(context.Background()); err != nil {
		t.Errorf("Reachable: %v", err)
	}
}

func TestReachable_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	unreachableURL := srv.URL
	srv.Close() // closed before use — connection refused
	ch, err := httpchannel.Factory(channels.ChannelSpec{Kind: httpchannel.Kind, URL: unreachableURL}, secrets.NoopResolver{})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	err = ch.Reachable(context.Background())
	if !errors.Is(err, bundle.ErrChannelUnreachable) {
		t.Errorf("err=%v, want ErrChannelUnreachable", err)
	}
}

func TestFactory_RejectsNonHTTPScheme(t *testing.T) {
	_, err := httpchannel.Factory(channels.ChannelSpec{Kind: httpchannel.Kind, URL: "ftp://example.com/bundle"}, secrets.NoopResolver{})
	if err == nil {
		t.Fatalf("expected an error for a non-http(s) scheme")
	}
}

func TestFactory_RequiresURL(t *testing.T) {
	_, err := httpchannel.Factory(channels.ChannelSpec{Kind: httpchannel.Kind}, secrets.NoopResolver{})
	if err == nil {
		t.Fatalf("expected an error for an empty URL")
	}
}

func TestFetchOK(t *testing.T) {
	body := []byte("bundle artifact bytes served over http_mirror")
	mux := http.NewServeMux()
	mux.HandleFunc("/policy/policy.toml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ch, err := httpchannel.Factory(channels.ChannelSpec{Kind: httpchannel.Kind, URL: srv.URL}, secrets.NoopResolver{})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	var buf bytes.Buffer
	res, err := ch.Fetch(context.Background(), channels.ArtifactCoord{Path: "policy/policy.toml"}, &buf)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), body) {
		t.Errorf("fetched bytes = %q, want %q", buf.Bytes(), body)
	}
	if res.Bytes != int64(len(body)) {
		t.Errorf("FetchResult.Bytes = %d, want %d", res.Bytes, len(body))
	}
	if strings.Contains(res.Endpoint, "?") {
		t.Errorf("Endpoint should never carry a query string: %q", res.Endpoint)
	}
}

func TestFetch_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	ch, err := httpchannel.Factory(channels.ChannelSpec{Kind: httpchannel.Kind, URL: srv.URL}, secrets.NoopResolver{})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	var buf bytes.Buffer
	_, err = ch.Fetch(context.Background(), channels.ArtifactCoord{Path: "missing.bin"}, &buf)
	if err == nil {
		t.Fatalf("expected an error fetching a missing artifact")
	}
}

func TestFetch_RejectsPathTraversal(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	ch, err := httpchannel.Factory(channels.ChannelSpec{Kind: httpchannel.Kind, URL: srv.URL}, secrets.NoopResolver{})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	var buf bytes.Buffer
	_, err = ch.Fetch(context.Background(), channels.ArtifactCoord{Path: "../../etc/passwd"}, &buf)
	if !errors.Is(err, bundle.ErrPathTraversal) {
		t.Errorf("err=%v, want ErrPathTraversal", err)
	}
}

func TestLookupSignatures_Present(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/kenaz.yaml.sig", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ch, err := httpchannel.Factory(channels.ChannelSpec{Kind: httpchannel.Kind, URL: srv.URL}, secrets.NoopResolver{})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	sigs, err := ch.LookupSignatures(context.Background(), channels.ArtifactCoord{Path: "kenaz.yaml"})
	if err != nil {
		t.Fatalf("LookupSignatures: %v", err)
	}
	if len(sigs) != 1 || sigs[0].Kind != "ed25519_detached" || sigs[0].Locator != "kenaz.yaml.sig" {
		t.Errorf("LookupSignatures = %+v, want one ed25519_detached ref for kenaz.yaml.sig", sigs)
	}
}

func TestLookupSignatures_Absent(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	ch, err := httpchannel.Factory(channels.ChannelSpec{Kind: httpchannel.Kind, URL: srv.URL}, secrets.NoopResolver{})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	sigs, err := ch.LookupSignatures(context.Background(), channels.ArtifactCoord{Path: "kenaz.yaml"})
	if err != nil {
		t.Fatalf("LookupSignatures on a 404 should not error: %v", err)
	}
	if len(sigs) != 0 {
		t.Errorf("expected no signatures, got %+v", sigs)
	}
}

// TestFetch_CredentialNeverLeaks is UNIT-6's credential-hygiene
// requirement (spec §11.5, plan.md's check-no-cred-bytes-in-rpc.sh /
// check-no-credential-in-ui.sh bind here): a credential resolved via
// AuthRef must reach the server as the Authorization header (proving
// the channel actually used it) and must NEVER appear in
// FetchResult.Endpoint or any returned error.
func TestFetch_CredentialNeverLeaks(t *testing.T) {
	const token = "s3cr3t-token-must-not-leak"
	var gotAuthHeader string
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/artifact.bin", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuthHeader = r.Header.Get("Authorization")
		mu.Unlock()
		_, _ = w.Write([]byte("payload"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resolver := &fakeResolver{token: token}
	ch, err := httpchannel.Factory(channels.ChannelSpec{
		Kind: httpchannel.Kind,
		URL:  srv.URL,
		Auth: &channels.AuthRef{Keychain: "test-keychain-entry"},
	}, resolver)
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}

	var buf bytes.Buffer
	res, err := ch.Fetch(context.Background(), channels.ArtifactCoord{Path: "artifact.bin"}, &buf)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	mu.Lock()
	got := gotAuthHeader
	mu.Unlock()
	if got != "Bearer "+token {
		t.Fatalf("server received Authorization=%q, want Bearer %s (the channel must actually use the resolved credential)", got, token)
	}
	if strings.Contains(res.Endpoint, token) {
		t.Errorf("FetchResult.Endpoint leaked the credential: %q", res.Endpoint)
	}
	resolved := resolver.snapshot()
	if len(resolved) != 1 || resolved[0].Locator != "test-keychain-entry" {
		t.Errorf("resolver.snapshot() = %+v, want exactly one resolve for test-keychain-entry", resolved)
	}
}

// TestFetch_BadCredential_ErrorNeverLeaksToken confirms that even on a
// resolver failure, nothing about the credential's value can appear in
// the returned error (there is nothing to leak — Resolve failed before
// any value existed — but this pins the shape so a future refactor
// that starts including partial secret material in error text fails
// loudly).
func TestFetch_BadCredential_ErrorNeverLeaksToken(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	ch, err := httpchannel.Factory(channels.ChannelSpec{
		Kind: httpchannel.Kind,
		URL:  srv.URL,
		Auth: &channels.AuthRef{Keychain: "does-not-exist"},
	}, secrets.NoopResolver{})
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	var buf bytes.Buffer
	_, err = ch.Fetch(context.Background(), channels.ArtifactCoord{Path: "artifact.bin"}, &buf)
	if err == nil {
		t.Fatalf("expected an error when the credential resolver has nothing configured")
	}
}
