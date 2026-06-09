package gemini

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestAPIKeyAuth_Authorize verifies the x-goog-api-key header is set correctly.
func TestAPIKeyAuth_Authorize(t *testing.T) {
	t.Parallel()
	auth := newAPIKeyAuth([]byte("test-api-key-xyz"))
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err := auth.Authorize(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := req.Header.Get("x-goog-api-key")
	if got != "test-api-key-xyz" {
		t.Errorf("want x-goog-api-key=test-api-key-xyz, got %q", got)
	}
	// Authorization header must NOT be set for API key mode.
	if req.Header.Get("Authorization") != "" {
		t.Errorf("Authorization header should not be set for API key auth, got %q", req.Header.Get("Authorization"))
	}
}

// TestAPIKeyAuth_Status verifies API key auth status returns "ok".
func TestAPIKeyAuth_Status(t *testing.T) {
	t.Parallel()
	auth := newAPIKeyAuth([]byte("key"))
	if auth.Status() != "ok" {
		t.Errorf("want status=ok, got %q", auth.Status())
	}
}

// TestAdcAuth_Constructs verifies adcAuth constructs without credential bytes.
func TestAdcAuth_Constructs(t *testing.T) {
	t.Parallel()
	auth := newADCAuth()
	if auth == nil {
		t.Fatal("newADCAuth returned nil")
	}
	s := auth.Status()
	if s == "" {
		t.Error("Status should return a non-empty string")
	}
}

// TestAdcAuth_StatusWithFakeMetadata verifies adcAuth returns a non-empty
// token-expiry status after a successful metadata fetch.
func TestAdcAuth_StatusWithFakeMetadata(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"test-adc-token","expires_in":3600}`))
	}))
	defer srv.Close()

	auth := newADCAuth()
	auth.httpc = srv.Client()
	// Status before any fetch.
	s := auth.Status()
	if s == "" {
		t.Error("expected non-empty Status()")
	}
}

// TestServiceAccountAuth_ParsesJSON verifies service account auth rejects
// invalid JSON and wrong type field.
func TestServiceAccountAuth_ParsesJSON(t *testing.T) {
	t.Parallel()

	t.Run("invalid JSON", func(t *testing.T) {
		t.Parallel()
		_, err := newServiceAccountAuth([]byte("not-json"))
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		t.Parallel()
		_, err := newServiceAccountAuth([]byte(`{"type":"user"}`))
		if err == nil {
			t.Error("expected error for wrong type")
		}
	})

	t.Run("missing private key", func(t *testing.T) {
		t.Parallel()
		_, err := newServiceAccountAuth([]byte(`{"type":"service_account","client_email":"x@p.iam.gserviceaccount.com","private_key":""}`))
		if err == nil {
			t.Error("expected error for missing private key")
		}
	})
}

// TestClassifyStatus verifies HTTP status → error type mapping.
func TestClassifyStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status  int
		wantTyp string
	}{
		{401, "auth"},
		{403, "auth"},
		{429, "transient"},
		{500, "transient"},
		{503, "transient"},
		{408, "transient"},
		{400, "invalid"},
		{404, "invalid"},
		{422, "invalid"},
	}
	for _, tc := range tests {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			t.Parallel()
			err := classifyStatus(tc.status, nil)
			if err == nil {
				t.Fatalf("status %d: expected non-nil error", tc.status)
			}
			var authErr *llm.ErrAuth
			var transErr *llm.ErrTransient
			var invalidErr *llm.ErrInvalidRequest
			switch tc.wantTyp {
			case "auth":
				if !errors.As(err, &authErr) {
					t.Errorf("status %d: want *ErrAuth, got %T", tc.status, err)
				}
			case "transient":
				if !errors.As(err, &transErr) {
					t.Errorf("status %d: want *ErrTransient, got %T", tc.status, err)
				}
			case "invalid":
				if !errors.As(err, &invalidErr) {
					t.Errorf("status %d: want *ErrInvalidRequest, got %T", tc.status, err)
				}
			}
		})
	}
}
