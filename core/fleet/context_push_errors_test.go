package fleet

// context_push_errors_test.go — the context push path surfaces kenaz-fleet
// PR #173's per-item rejections and real error codes (shapes from the fleet
// branch at d8adf1e), while staying tolerant of the current server.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	contextpack "github.com/kameas-ai/kenaz-harness/core/context/pack"
)

func pushRig(t *testing.T, status int, body string) *ContextGraphSyncer {
	t.Helper()
	withExternalToken(t, "tok")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return NewContextGraphSyncer(makeTestClient(t, srv.URL), t.TempDir(), makeCapPollerWithTeamCap(t))
}

func teamEntry(id string) ContextNodeEntry {
	team := "team-1"
	return ContextNodeEntry{ID: id, Layer: contextpack.LayerTeam, Kind: "guidance", Title: "t", Body: "b", Version: 1, TeamID: &team}
}

func TestContextPush_NoRejectedField_IsFullSuccess(t *testing.T) {
	s := pushRig(t, 200, `{"accepted_nodes":1,"accepted_edges":0,"conflicts":[]}`)
	res, err := s.PushEntry(context.Background(), teamEntry("n1"), nil)
	if err != nil || res.AcceptedNodes != 1 || len(res.Rejected) != 0 {
		t.Fatalf("res=%+v err=%v, want accepted with no rejections (current server shape)", res, err)
	}
	if st := s.Status(); st.LastPushErr != "" {
		t.Fatalf("LastPushErr = %q, want empty", st.LastPushErr)
	}
}

func TestContextPush_OwnNodeRejected_IsFailure(t *testing.T) {
	s := pushRig(t, 200, `{"accepted_nodes":0,"accepted_edges":0,"conflicts":[],"rejected":[{"id":"n1","kind":"node","reason":"not_permitted"}]}`)
	res, err := s.PushEntry(context.Background(), teamEntry("n1"), nil)
	if res != nil || !errors.Is(err, ErrContextPushRejected) {
		t.Fatalf("res=%+v err=%v, want ErrContextPushRejected — a 200 with the node rejected is not success", res, err)
	}
	if !strings.Contains(err.Error(), "not_permitted") {
		t.Errorf("error copy %q should name the reason", err.Error())
	}
	if st := s.Status(); !strings.Contains(st.LastPushErr, "node n1 not_permitted") {
		t.Fatalf("LastPushErr = %q, want the rejection surfaced", st.LastPushErr)
	}
}

func TestContextPush_EdgeRejected_NodeAccepted_SurfacedNotSilent(t *testing.T) {
	s := pushRig(t, 200, `{"accepted_nodes":1,"accepted_edges":0,"conflicts":[],"rejected":[{"id":"e9","kind":"edge","reason":"not_permitted"}]}`)
	res, err := s.PushEntry(context.Background(), teamEntry("n1"), nil)
	if err != nil {
		t.Fatalf("err = %v, want the node publish to stand", err)
	}
	if len(res.Rejected) != 1 || res.Rejected[0].ID != "e9" {
		t.Fatalf("Rejected = %+v, want the edge rejection carried on the result", res.Rejected)
	}
	if st := s.Status(); !strings.Contains(st.LastPushErr, "edge e9 not_permitted") {
		t.Fatalf("LastPushErr = %q, want the edge rejection surfaced", st.LastPushErr)
	}
}

func TestContextPush_StaleOwnVersion_StaysAConflictNotARejection(t *testing.T) {
	s := pushRig(t, 200, `{"accepted_nodes":0,"accepted_edges":0,"conflicts":[{"node_id":"n1","server_version":3,"client_version":1}]}`)
	res, err := s.PushEntry(context.Background(), teamEntry("n1"), nil)
	if err != nil || len(res.Conflicts) != 1 || len(res.Rejected) != 0 {
		t.Fatalf("res=%+v err=%v, want one conflict and no rejection", res, err)
	}
}

func TestContextPush_ErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		sentinel error
		copy     string
		lastErr  string
	}{
		{"not_team_member", 403, `{"code":"not_team_member","message":"not a member","details":{"node_id":"n1"}}`, ErrNotTeamMember, "not a member of that team", "not_team_member"},
		{"load_policy_requires_admin", 403, `{"code":"load_policy_requires_admin","message":"admin only"}`, ErrLoadPolicyRequiresAdmin, "org admin or owner", "load_policy_requires_admin"},
		{"capability_not_in_tier", 403, `{"code":"capability_not_in_tier","message":"upgrade"}`, ErrCapabilityNotInTier, "not available in current tier", "capability_not_in_tier"},
		{"bare_403_is_generic", 403, `Forbidden`, ErrContextPushForbidden, "refused", "push status 403"},
		{"lint_blocked", 422, `{"code":"lint_blocked","message":"secret","details":{"node_id":"n1","findings":[{"rule":"aws_access_key","severity":"block","excerpt":"AKIA****************"}]}}`, ErrContextLintBlocked, `blocked — a body looks like it contains a secret (aws_access_key: "AKIA****************")`, "lint_blocked"},
		{"invalid_classification", 400, `{"code":"invalid_classification","message":"bad"}`, ErrInvalidClassification, "invalid sharing classification", "invalid_classification"},
		{"missing_node_reference", 400, `{"code":"missing_node_reference","message":"edge endpoint"}`, ErrMissingNodeReference, "isn't visible in your org", "missing_node_reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := pushRig(t, tc.status, tc.body)
			_, err := s.PushEntry(context.Background(), teamEntry("n1"), nil)
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("err = %v, want errors.Is(%v)", err, tc.sentinel)
			}
			if !strings.Contains(err.Error(), tc.copy) {
				t.Errorf("copy %q lacks %q", err.Error(), tc.copy)
			}
			if tc.sentinel != ErrCapabilityNotInTier && errors.Is(err, ErrCapabilityNotInTier) {
				t.Errorf("%s collapsed into capability_not_in_tier", tc.name)
			}
			if st := s.Status(); st.LastPushErr != tc.lastErr {
				t.Errorf("LastPushErr = %q, want %q", st.LastPushErr, tc.lastErr)
			}
		})
	}
}

func TestContextPromote_404NodeNotFound(t *testing.T) {
	s := pushRig(t, 404, `{"code":"node_not_found","message":"no such node"}`)
	_, err := s.Promote(context.Background(), "n1")
	if !errors.Is(err, ErrContextNodeNotFound) {
		t.Fatalf("err = %v, want ErrContextNodeNotFound", err)
	}
}
