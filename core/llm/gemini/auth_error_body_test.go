package gemini

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A token planted in the Google token endpoint's error body must not be
// returned in the mint error, which callers surface and log.
func TestMintToken_ErrorNeverCarriesTheBody(t *testing.T) {
	stray := "ya29." + strings.Repeat("Z", 40)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":"invalid_grant","error_description":"bad %s","access_token":"%s"}`, stray, stray)
	}))
	defer srv.Close()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	a := &serviceAccountAuth{
		sa:      serviceAccountJSON{Type: "service_account", ClientEmail: "x@example.iam", TokenURI: srv.URL},
		privKey: key,
		httpc:   &http.Client{Timeout: 5 * time.Second},
	}
	_, _, err = a.mintToken(context.Background())
	if err == nil {
		t.Fatal("want an error from a 400 token endpoint")
	}
	if strings.Contains(err.Error(), stray) {
		t.Fatalf("the token endpoint body leaked into the error: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("lost the OAuth error code: %v", err)
	}
}

// The GCE metadata token endpoint's error body never reaches the error.
func TestFetchMetadataToken_ErrorNeverCarriesTheBody(t *testing.T) {
	stray := "ya29." + strings.Repeat("M", 40)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(w, `{"error":"access_denied","access_token":"%s"}`, stray)
	}))
	defer srv.Close()
	_, _, err := fetchMetadataTokenFrom(context.Background(), &http.Client{Timeout: 5 * time.Second}, srv.URL)
	if err == nil || strings.Contains(err.Error(), stray) {
		t.Fatalf("err = %v (must exist and not carry the body)", err)
	}
}
