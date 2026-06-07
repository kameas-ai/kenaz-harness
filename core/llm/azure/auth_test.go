package azure

import (
	"context"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

//go:embed testdata/models_response.json
var modelsFixture []byte

func TestTestKey_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-key") == "" {
			t.Error("api-key header missing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(modelsFixture)
	}))
	defer srv.Close()

	a := New(WithHTTPClient(&http.Client{}))

	// Override the models URL construction by using a registry with the test server host.
	// We can't easily inject a URL override for TestKey, so we'll test via the
	// internal function using buildModelsURL replaced by Endpoint mechanism.
	// Instead, test via direct TestKey call with the srv host.
	// Since TestKey uses buildModelsURL which always produces https://, we test
	// the function by making the host the test server's address with scheme stripped.
	// This won't work with http:// — so we test the auth_test via TestKey with
	// an httptest TLS server.
	srvTLS := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey := r.Header.Get("api-key")
		if gotKey != "test-key" {
			t.Errorf("api-key = %q; want 'test-key'", gotKey)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(modelsFixture)
	}))
	defer srvTLS.Close()

	a = New(WithHTTPClient(srvTLS.Client()))
	host := strings.TrimPrefix(srvTLS.URL, "https://")

	result, err := a.TestKey(context.Background(), host, []byte("test-key"))
	if err != nil {
		t.Fatalf("TestKey: %v", err)
	}
	if !result.OK {
		t.Errorf("OK = false; want true")
	}
	if result.ModelCount != 3 {
		t.Errorf("ModelCount = %d; want 3", result.ModelCount)
	}
	if result.DeprecationWarning != "" {
		t.Errorf("DeprecationWarning = %q; want empty", result.DeprecationWarning)
	}
}

func TestTestKey_401_ErrProviderAuthFailed(t *testing.T) {
	srvTLS := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"401","message":"Access denied"}}`))
	}))
	defer srvTLS.Close()

	a := New(WithHTTPClient(srvTLS.Client()))
	host := strings.TrimPrefix(srvTLS.URL, "https://")

	result, err := a.TestKey(context.Background(), host, []byte("bad-key"))
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if result.OK {
		t.Error("OK = true; want false")
	}
	var authErr *llm.ErrAuth
	if !errAs(err, &authErr) {
		t.Errorf("error type = %T; want *llm.ErrAuth", err)
	}
}

func TestTestKey_DeprecationHeader(t *testing.T) {
	srvTLS := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("api-deprecation", "2025-01-01")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(modelsFixture)
	}))
	defer srvTLS.Close()

	a := New(WithHTTPClient(srvTLS.Client()))
	host := strings.TrimPrefix(srvTLS.URL, "https://")

	result, err := a.TestKey(context.Background(), host, []byte("key"))
	if err != nil {
		t.Fatalf("TestKey: %v", err)
	}
	if result.DeprecationWarning != "2025-01-01" {
		t.Errorf("DeprecationWarning = %q; want '2025-01-01'", result.DeprecationWarning)
	}
}

func TestTestKey_AzureMLDeprecationHeader(t *testing.T) {
	srvTLS := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("azureml-model-deprecation", "2025-06-01")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(modelsFixture)
	}))
	defer srvTLS.Close()

	a := New(WithHTTPClient(srvTLS.Client()))
	host := strings.TrimPrefix(srvTLS.URL, "https://")

	result, err := a.TestKey(context.Background(), host, []byte("key"))
	if err != nil {
		t.Fatalf("TestKey: %v", err)
	}
	if result.DeprecationWarning != "2025-06-01" {
		t.Errorf("DeprecationWarning = %q; want '2025-06-01'", result.DeprecationWarning)
	}
}

func TestTestKey_BothDeprecationHeaders(t *testing.T) {
	srvTLS := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("api-deprecation", "2025-01-01")
		w.Header().Set("azureml-model-deprecation", "2025-06-01")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(modelsFixture)
	}))
	defer srvTLS.Close()

	a := New(WithHTTPClient(srvTLS.Client()))
	host := strings.TrimPrefix(srvTLS.URL, "https://")

	result, err := a.TestKey(context.Background(), host, []byte("key"))
	if err != nil {
		t.Fatalf("TestKey: %v", err)
	}
	// Both headers must surface, joined by "; ".
	if !strings.Contains(result.DeprecationWarning, "2025-01-01") ||
		!strings.Contains(result.DeprecationWarning, "2025-06-01") {
		t.Errorf("DeprecationWarning = %q; want both dates", result.DeprecationWarning)
	}
	if !strings.Contains(result.DeprecationWarning, "; ") {
		t.Errorf("DeprecationWarning = %q; want '; ' separator", result.DeprecationWarning)
	}
}

// errAs is a minimal generic helper to check error types.
func errAs[T error](err error, target *T) bool {
	v, ok := err.(T)
	if ok {
		*target = v
	}
	return ok
}
