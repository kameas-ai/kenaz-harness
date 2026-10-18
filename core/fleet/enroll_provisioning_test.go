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

// TestRefreshIdentity_RealFleetWireShape_SurfacesName pins the enroll
// response shape kenaz-fleet main actually serializes: `user_email` and
// `user_display_name` (both omitempty), a singular `role`, and NO
// `email`/`display_name` keys (those are the GET /api/v1/me keys). Before
// the fix the harness read only the /me keys, so every enrolled identity
// had an empty name and the account menu fell back to the org name.
func TestRefreshIdentity_RealFleetWireShape_SurfacesName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/enroll" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"org_id": 7,
			"team_id": "team-1",
			"org_name": "Acme",
			"team_name": "Eng",
			"role": "org_owner",
			"user_id": "u-1",
			"user_email": "ada@example.com",
			"user_display_name": "Ada Lovelace"
		}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	id, err := c.RefreshIdentity(t.Context(), "node-1", "darwin", "0.89.1")
	if err != nil {
		t.Fatalf("RefreshIdentity: %v", err)
	}
	if id.DisplayName != "Ada Lovelace" {
		t.Errorf("DisplayName = %q, want %q (enroll serializes user_display_name)", id.DisplayName, "Ada Lovelace")
	}
	if id.Email != "ada@example.com" {
		t.Errorf("Email = %q, want %q (enroll serializes user_email)", id.Email, "ada@example.com")
	}
	if len(id.Roles) != 1 || id.Roles[0] != "org_owner" {
		t.Errorf("Roles = %v, want [org_owner]", id.Roles)
	}
}

// TestRefreshIdentity_LegacyKeysStillTolerated keeps the /me-shaped keys
// as a fallback, and both absent (omitempty) leaves the fields empty.
func TestRefreshIdentity_LegacyKeysStillTolerated(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantName, wantEmail string
	}{
		{"legacy", `{"org_id":1,"role":"org_member","email":"l@example.com","display_name":"Legacy"}`, "Legacy", "l@example.com"},
		{"new_wins", `{"org_id":1,"role":"org_member","user_email":"n@example.com","user_display_name":"New","email":"l@example.com","display_name":"Legacy"}`, "New", "n@example.com"},
		{"both_absent", `{"org_id":1,"role":"org_member"}`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := newTestClient(t, srv)
			id, err := c.RefreshIdentity(t.Context(), "node-1", "darwin", "0.89.1")
			if err != nil {
				t.Fatalf("RefreshIdentity: %v", err)
			}
			if id.DisplayName != tc.wantName || id.Email != tc.wantEmail {
				t.Errorf("got name=%q email=%q, want name=%q email=%q", id.DisplayName, id.Email, tc.wantName, tc.wantEmail)
			}
		})
	}
}
