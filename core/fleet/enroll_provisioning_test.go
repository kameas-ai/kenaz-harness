package fleet_test

// Tests for the enroll error-mapping fix (fleet-enroll-not-provisioned).
//
// Prior to this fix, enrollIdentity treated every non-2xx /api/v1/enroll
// response identically — a raw `fmt.Errorf("fleet: enroll: status %d: %s",
// ...)` with no JSON parse and no code branch. A 403 user_not_provisioned
// response (the Zitadel identity authenticated but has no Fleet account)
// was indistinguishable from a transient 5xx, so nothing downstream could
// tell "stop retrying" from "try again". These tests pin the new
// enrollErrorEnvelope parse + ErrUserNotProvisioned sentinel, and confirm
// a plain transient failure is NOT misclassified as terminal.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

func TestRefreshIdentity_403UserNotProvisioned_ReturnsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/enroll" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		// zitadel_user_id deliberately omitted from this fixture — the
		// envelope's "details" field is real identity data and the
		// production parser does not read it; the test does not need it
		// either. Mirrors the real fleet response shape from the bug report.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    "user_not_provisioned",
			"message": "This Zitadel user has no Fleet account. Finish signup at the SPA host.",
			"details": map[string]string{"zitadel_user_id": "test-user-id"},
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.RefreshIdentity(t.Context(), "node-1", "darwin", "0.73.0")

	if !errors.Is(err, fleet.ErrUserNotProvisioned) {
		t.Fatalf("RefreshIdentity: got %v, want errors.Is(_, ErrUserNotProvisioned)", err)
	}
	// The server's message should still be present for logs/diagnostics,
	// but the caller must be able to branch on the sentinel regardless of
	// the message wording — that's the whole point of the fix.
	if err.Error() == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestRefreshIdentity_403WithoutCode_FallsBackToGenericError(t *testing.T) {
	// A 403 with no recognizable {code} envelope (e.g. an ingress/proxy
	// error page) must NOT be misclassified as the terminal
	// user_not_provisioned condition.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Forbidden"))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.RefreshIdentity(t.Context(), "node-1", "darwin", "0.73.0")

	if errors.Is(err, fleet.ErrUserNotProvisioned) {
		t.Fatalf("RefreshIdentity: got ErrUserNotProvisioned for a bare 403, want generic error")
	}
	if err == nil {
		t.Fatal("expected an error for a 403 response")
	}
}

func TestRefreshIdentity_500Transient_IsNotTerminal(t *testing.T) {
	// A transient server error must remain distinguishable from the
	// terminal user_not_provisioned condition — a caller backing off on
	// ErrUserNotProvisioned specifically must keep retrying this case.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream timeout"))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.RefreshIdentity(t.Context(), "node-1", "darwin", "0.73.0")

	if errors.Is(err, fleet.ErrUserNotProvisioned) {
		t.Fatalf("RefreshIdentity: got ErrUserNotProvisioned for a transient 500, want a plain retryable error")
	}
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}
