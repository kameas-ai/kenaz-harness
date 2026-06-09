package gemini

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestTestKey_AIStudio200 verifies TestKey returns nil for a 200 response.
func TestTestKey_AIStudio200(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the x-goog-api-key header is set (not Authorization).
		if r.Header.Get("x-goog-api-key") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	a := New()
	a.httpc = &http.Client{
		Transport: &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
			req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
			req.URL.Scheme = "http"
			return srv.Client().Do(req)
		}},
	}

	err := a.TestKey(context.Background(), []byte("valid-api-key"))
	if err != nil {
		t.Errorf("TestKey 200: want nil, got %v", err)
	}
}

// TestTestKey_AIStudio401 verifies TestKey returns *ErrAuth for a 401.
func TestTestKey_AIStudio401(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":401,"message":"API key not valid","status":"UNAUTHENTICATED"}}`))
	}))
	defer srv.Close()

	a := New()
	a.httpc = &http.Client{
		Transport: &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
			req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
			req.URL.Scheme = "http"
			return srv.Client().Do(req)
		}},
	}

	err := a.TestKey(context.Background(), []byte("bad-key"))
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	var authErr *llm.ErrAuth
	if !errors.As(err, &authErr) {
		t.Errorf("want *ErrAuth, got %T: %v", err, err)
	}
}

// TestTestKey_EmptyCred verifies TestKey returns *ErrAuth for empty credential.
func TestTestKey_EmptyCred(t *testing.T) {
	t.Parallel()
	a := New()
	err := a.TestKey(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty cred")
	}
	var authErr *llm.ErrAuth
	if !errors.As(err, &authErr) {
		t.Errorf("want *ErrAuth, got %T: %v", err, err)
	}
}

// TestTestKey_Vertex403 verifies TestKey returns *ErrAuth for a 403.
func TestTestKey_Vertex403(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"Permission denied","status":"PERMISSION_DENIED"}}`))
	}))
	defer srv.Close()

	a := New()
	a.httpc = &http.Client{
		Transport: &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
			req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
			req.URL.Scheme = "http"
			return srv.Client().Do(req)
		}},
	}

	err := a.TestKey(context.Background(), []byte("key"))
	if err == nil {
		t.Fatal("expected error for 403")
	}
	var authErr *llm.ErrAuth
	if !errors.As(err, &authErr) {
		t.Errorf("want *ErrAuth for 403, got %T: %v", err, err)
	}
}

// TestAdapterImplementsKeyTester verifies compile-time interface satisfaction.
func TestAdapterImplementsKeyTester(t *testing.T) {
	t.Parallel()
	var _ llm.KeyTester = (*Adapter)(nil)
	// If this compiles, the assertion holds.
}
