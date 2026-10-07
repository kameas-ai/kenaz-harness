package fleet

// unit_merge_request_test.go — WP16 promote-as-merge-request tests.
//
// Covers:
//   - personal→team promotion opens a merge request (and the PERSONAL source
//     unit is never pushed as a node — only the reviewed proposal travels).
//   - the merge-request wire body matches fleet's MergeRequestCreateRequest
//     (unit_node_id as a UUID, to_classification, proposed_title/body) and the
//     response is decoded from fleet's {"merge_request": …} envelope.
//   - non-upward targets are rejected with ErrPromoteNotUp.
//   - team→org promotion maps the from/to classes to the sync vocabulary.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

// mrFakeServer records merge-request POST bodies and serves a canned response.
type mrFakeServer struct {
	mu       sync.Mutex
	requests []mergeRequestInput
	// pushNodes records any /context/push node ids so a test can assert the
	// personal source was never pushed.
	pushNodes []string
}

func (s *mrFakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/merge-requests":
		var in mergeRequestInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		s.mu.Lock()
		s.requests = append(s.requests, in)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		// Fleet's MergeRequestResponse envelope (service/api_types.go).
		_ = json.NewEncoder(w).Encode(map[string]any{"merge_request": MergeRequest{
			ID:                 "mr-1",
			UnitNodeID:         in.UnitNodeID,
			FromClassification: "personal",
			ToClassification:   in.ToClassification,
			ProposedVersion:    3,
			Title:              in.ProposedTitle,
			Body:               in.ProposedBody,
			Status:             "open",
			CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		}})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/push":
		var req contextPushRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.mu.Lock()
		for _, n := range req.Nodes {
			s.pushNodes = append(s.pushNodes, n.ID)
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(ContextPushResult{AcceptedNodes: len(req.Nodes)})
	default:
		http.NotFound(w, r)
	}
}

func newMRTestSyncer(t *testing.T, srvURL string) (*UnitSyncer, *units.Manager) {
	t.Helper()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	client := makeTestClient(t, srvURL)
	caps := makeCapPollerWithTeamCap(t)
	m := newUnitTestManager()
	return NewUnitSyncer(client, m, NewUnitMapper("team-1"), caps, t.TempDir()), m
}

func TestCreateMergeRequestForPromote_PersonalToTeam_IsNotAMergeRequest(t *testing.T) {
	fake := &mrFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	syncer, mgr := newMRTestSyncer(t, srv.URL)
	ctx := context.Background()
	src, err := mgr.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p1",
		Classification: units.ClassPersonal, LoadPolicy: units.LoadAlways,
		Title: "my note", Body: "personal body",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Review F2: personal→team (and personal→org) is never a merge request —
	// fleet's MR needs an existing node; the caller pushes at team instead.
	for _, to := range []units.Classification{units.ClassTeam, units.ClassOrg} {
		if _, err := syncer.CreateMergeRequestForPromote(ctx, src, to, "t", "b"); !errors.Is(err, ErrPromoteNotUp) {
			t.Errorf("personal→%s err = %v, want ErrPromoteNotUp (team→org only)", to, err)
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.requests) != 0 || len(fake.pushNodes) != 0 {
		t.Fatalf("requests=%d pushes=%v — nothing may travel", len(fake.requests), fake.pushNodes)
	}
}

func TestCreateMergeRequestForPromote_TeamToOrg(t *testing.T) {
	fake := &mrFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	syncer, mgr := newMRTestSyncer(t, srv.URL)
	ctx := context.Background()

	src := seedTeamUnit(t, mgr, "team doc", "shared")
	if _, err := syncer.CreateMergeRequestForPromote(ctx, src, units.ClassOrg, "", ""); err != nil {
		t.Fatalf("CreateMergeRequestForPromote: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	got := fake.requests[0]
	if got.ToClassification != string(ClassOrgShared) {
		t.Errorf("to = %q, want org_shared", got.ToClassification)
	}
	// Empty title/body default to the source's.
	if got.ProposedTitle != "team doc" || got.ProposedBody != "shared" {
		t.Errorf("defaulted title/body = %q/%q, want team doc/shared", got.ProposedTitle, got.ProposedBody)
	}
}

func TestCreateMergeRequestForPromote_NotUpRejected(t *testing.T) {
	fake := &mrFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	syncer, mgr := newMRTestSyncer(t, srv.URL)
	ctx := context.Background()

	src := seedTeamUnit(t, mgr, "doc", "body")

	// team→team (same level) and team→personal (downward) are NOT write-up.
	if _, err := syncer.CreateMergeRequestForPromote(ctx, src, units.ClassTeam, "", ""); !errors.Is(err, ErrPromoteNotUp) {
		t.Errorf("team→team err = %v, want ErrPromoteNotUp", err)
	}
	if _, err := syncer.CreateMergeRequestForPromote(ctx, src, units.ClassPersonal, "", ""); !errors.Is(err, ErrPromoteNotUp) {
		t.Errorf("team→personal err = %v, want ErrPromoteNotUp", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.requests) != 0 {
		t.Errorf("rejected promotions still sent %d merge requests", len(fake.requests))
	}
}

func TestIsPromotionUp(t *testing.T) {
	cases := []struct {
		from, to units.Classification
		want     bool
	}{
		{units.ClassPersonal, units.ClassTeam, true},
		{units.ClassPersonal, units.ClassOrg, true},
		{units.ClassTeam, units.ClassOrg, true},
		{units.ClassTeam, units.ClassTeam, false},
		{units.ClassOrg, units.ClassTeam, false},
		{units.ClassOrg, units.ClassPersonal, false},
	}
	for _, c := range cases {
		if got := IsPromotionUp(c.from, c.to); got != c.want {
			t.Errorf("IsPromotionUp(%s,%s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}
