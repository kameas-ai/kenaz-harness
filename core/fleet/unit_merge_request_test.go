package fleet

// unit_merge_request_test.go — WP16 promote-as-merge-request tests.
//
// Covers:
//   - personal→team promotion opens a merge request (and the PERSONAL source
//     unit is never pushed as a node — only the reviewed proposal travels).
//   - the merge-request wire body matches the fleet merge_requests object
//     (unit_node_id, from/to_classification, proposed_version, title, body).
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
		_ = json.NewEncoder(w).Encode(MergeRequest{
			ID:                 "mr-1",
			UnitNodeID:         in.UnitNodeID,
			FromClassification: in.FromClassification,
			ToClassification:   in.ToClassification,
			ProposedVersion:    in.ProposedVersion,
			Title:              in.Title,
			Body:               in.Body,
			Status:             "open",
			CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		})
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

func TestCreateMergeRequestForPromote_PersonalToTeam(t *testing.T) {
	fake := &mrFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	syncer, mgr := newMRTestSyncer(t, srv.URL)
	ctx := context.Background()

	// A PERSONAL unit being promoted up to team. The personal unit must never be
	// pushed as a node (NFR-005) — only the reviewed proposal travels.
	src, err := mgr.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p1",
		Classification: units.ClassPersonal, LoadPolicy: units.LoadAlways,
		Title: "my note", Body: "personal body",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	mr, err := syncer.CreateMergeRequestForPromote(ctx, src, units.ClassTeam, "Promote my note", "please review")
	if err != nil {
		t.Fatalf("CreateMergeRequestForPromote: %v", err)
	}
	if mr.ID == "" || mr.Status != "open" {
		t.Fatalf("unexpected MR: %+v", mr)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.requests) != 1 {
		t.Fatalf("merge-request count = %d, want 1", len(fake.requests))
	}
	got := fake.requests[0]
	// Field shapes must match the fleet merge_requests object.
	if got.UnitNodeID != src.ID {
		t.Errorf("unit_node_id = %q, want %q", got.UnitNodeID, src.ID)
	}
	if got.FromClassification != "personal" {
		t.Errorf("from_classification = %q, want personal", got.FromClassification)
	}
	if got.ToClassification != string(ClassTeamShared) {
		t.Errorf("to_classification = %q, want %q", got.ToClassification, ClassTeamShared)
	}
	if got.ProposedVersion != src.Version {
		t.Errorf("proposed_version = %d, want %d", got.ProposedVersion, src.Version)
	}
	if got.Title != "Promote my note" || got.Body != "please review" {
		t.Errorf("title/body = %q/%q", got.Title, got.Body)
	}
	// The personal source was NEVER pushed as a node.
	if len(fake.pushNodes) != 0 {
		t.Errorf("personal unit pushed as node(s) %v — must never happen", fake.pushNodes)
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
	if got.FromClassification != string(ClassTeamShared) {
		t.Errorf("from = %q, want team_shared", got.FromClassification)
	}
	if got.ToClassification != string(ClassOrgShared) {
		t.Errorf("to = %q, want org_shared", got.ToClassification)
	}
	// Empty title/body default to the source's.
	if got.Title != "team doc" || got.Body != "shared" {
		t.Errorf("defaulted title/body = %q/%q, want team doc/shared", got.Title, got.Body)
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
