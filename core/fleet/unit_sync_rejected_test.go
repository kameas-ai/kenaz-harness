package fleet

// unit_sync_rejected_test.go — review R3(a): unit push maps errors by code
// and never advances the sidecar for a unit fleet rejected per-item
// (kenaz-fleet PR #173 `rejected[]`), so a rejected unit stays dirty.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixedPushServer struct {
	mu     sync.Mutex
	status int
	body   func(nodeIDs []string) string
	pushes int
	lastID []string
}

func (f *fixedPushServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var ids []string
	for _, part := range strings.Split(string(raw), `"id":"`)[1:] {
		ids = append(ids, part[:strings.IndexByte(part, '"')])
	}
	f.mu.Lock()
	f.pushes++
	f.lastID = ids
	status, body := f.status, f.body(ids)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func (f *fixedPushServer) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.pushes }

func TestUnitSyncer_PushDirty_RejectedUnitStaysDirty(t *testing.T) {
	fake := &fixedPushServer{status: 200, body: func(ids []string) string {
		return `{"accepted_nodes":0,"accepted_edges":0,"conflicts":[],"rejected":[{"id":"` + ids[0] + `","kind":"node","reason":"not_permitted"}]}`
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	m := newUnitTestManager()
	ctx := context.Background()
	u := seedTeamUnit(t, m, "doc", "body")
	syncer := NewUnitSyncer(makeTestClient(t, srv.URL), m, NewUnitMapper("team-1"), makeCapPollerWithTeamCap(t), t.TempDir())

	if _, err := syncer.PushDirty(ctx); err != nil {
		t.Fatalf("PushDirty: %v", err)
	}
	if st, err := m.GetSyncState(ctx, u.ID); err == nil && st.SyncedServerVersion != 0 {
		t.Fatalf("sidecar advanced for a rejected unit: %+v", st)
	}
	// Still dirty: the next PushDirty offers it again.
	if _, err := syncer.PushDirty(ctx); err != nil {
		t.Fatalf("PushDirty 2: %v", err)
	}
	if got := fake.count(); got != 2 {
		t.Fatalf("pushes = %d, want 2 — a rejected unit must stay dirty", got)
	}
}

func TestUnitSyncer_PushDirty_403MapsByCode(t *testing.T) {
	fake := &fixedPushServer{status: 403, body: func([]string) string {
		return `{"code":"not_team_member","message":"no","details":{"node_id":"x"}}`
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	m := newUnitTestManager()
	seedTeamUnit(t, m, "doc", "body")
	syncer := NewUnitSyncer(makeTestClient(t, srv.URL), m, NewUnitMapper("team-1"), makeCapPollerWithTeamCap(t), t.TempDir())

	_, err := syncer.PushDirty(context.Background())
	if !errors.Is(err, ErrNotTeamMember) || errors.Is(err, ErrCapabilityNotInTier) {
		t.Fatalf("err = %v, want ErrNotTeamMember (not capability_not_in_tier)", err)
	}
}
