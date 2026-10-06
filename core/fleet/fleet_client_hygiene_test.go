package fleet

// fleet_client_hygiene_test.go — WP05 (fleet-requested client hygiene,
// 2026-10-06): background pushers treat 201 as success and 413 as permanent;
// ids interpolated into paths are escaped; /team/members follows
// X-Next-Cursor pagination.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestAuditArchiver_201IsSuccess(t *testing.T) {
	p := &plainPoster{status: http.StatusCreated, body: `{"accepted":2}`}
	a := newArchiverWithEvents(t, p)
	if err := a.flushOnce(context.Background()); err != nil {
		t.Fatalf("flush with 201: %v — 201 is success", err)
	}
	if a.CurrentCursor() == "" {
		t.Error("cursor did not advance on 201")
	}
}

func TestAuditArchiver_413IsPermanent(t *testing.T) {
	p := &plainPoster{status: http.StatusRequestEntityTooLarge, body: `{"code":"payload_too_large"}`}
	a := newArchiverWithEvents(t, p)
	if err := a.flushOnce(context.Background()); !errors.Is(err, ErrAuditBatchTooLarge) {
		t.Fatalf("flush err = %v, want ErrAuditBatchTooLarge", err)
	}
	if !a.TooLarge() {
		t.Fatal("413 did not latch")
	}
	// The loop idles instead of re-posting the same batch forever.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	if got := p.count(); got != 1 {
		t.Fatalf("posted %d times after a 413, want 1", got)
	}
	if err := a.ArchiveNow(context.Background()); !errors.Is(err, ErrAuditBatchTooLarge) {
		t.Fatalf("ArchiveNow err = %v, want ErrAuditBatchTooLarge", err)
	}
	a.ResetUnsupported() // fleet session reset clears it
	if a.TooLarge() {
		t.Error("session reset did not clear the 413 latch")
	}
}

func TestClassifyAppendError_413PermanentAnd201Accepted(t *testing.T) {
	if reason, permanent := classifyAppendError(&AppendStatusError{Status: http.StatusRequestEntityTooLarge}); reason != "payload_too_large" || !permanent {
		t.Fatalf("413 → (%q, %v), want (payload_too_large, true)", reason, permanent)
	}
	withExternalToken(t, "tok")
	f := newCountingFleet(t, http.StatusCreated, `{"accepted":1}`, "application/json")
	es, err := NewEventStream(makeTestClient(t, f.srv.URL), "stream-1", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := es.Append(context.Background(), []Event{{Payload: []byte("x")}}); err != nil {
		t.Fatalf("append with 201: %v", err)
	}
}

// pathRecorder records the escaped request path (RawPath/RequestURI).
type pathRecorder struct {
	mu   sync.Mutex
	uris []string
}

func (p *pathRecorder) handler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.uris = append(p.uris, r.RequestURI)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func (p *pathRecorder) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.uris...)
}

func TestFleetPaths_IdsAreEscaped(t *testing.T) {
	rec := &pathRecorder{}
	srv := httptest.NewServer(rec.handler(http.StatusOK, `{}`))
	defer srv.Close()
	withExternalToken(t, "tok")
	c := makeTestClient(t, srv.URL)
	ctx := context.Background()

	_ = c.Unpublish(ctx, "a/b?c")
	_, _ = FetchCatalogItem(ctx, c, "x/y", "1.0#z")
	h := NewHandoffHandler(c, nil, nil)
	_, _ = h.fetchRecipientPublicKey(ctx, "u&admin=1")
	_, _ = h.AcceptShare(ctx, "../inbox")
	if es, err := NewEventStream(c, "s/x?y", make([]byte, 32)); err == nil {
		_ = es.DeleteRemote(ctx)
	} else {
		t.Fatal(err)
	}

	want := []string{
		"/api/v1/catalog/a%2Fb%3Fc",
		"/api/v1/catalog/x%2Fy@1.0%23z",
		"/api/v1/identity/public-key?user_id=u%26admin%3D1",
		"/api/v1/handoff/..%2Finbox",
		"/api/v1/context/s%2Fx%3Fy",
	}
	got := rec.snapshot()
	if len(got) != len(want) {
		t.Fatalf("requests = %v, want %d", got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestListTeam_FollowsXNextCursor(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("cursor") {
		case "":
			w.Header().Set("X-Next-Cursor", "c2")
			_ = json.NewEncoder(w).Encode([]TeamMember{{UserID: "u1"}, {UserID: "u2"}})
		case "c2":
			w.Header().Set("X-Next-Cursor", "c3")
			_ = json.NewEncoder(w).Encode([]TeamMember{{UserID: "u3"}})
		case "c3":
			_ = json.NewEncoder(w).Encode([]TeamMember{{UserID: "u4"}}) // last page: no header
		}
	}))
	defer srv.Close()
	withExternalToken(t, "tok")
	h := NewHandoffHandler(makeTestClient(t, srv.URL), nil, nil)
	members, err := h.ListTeam(context.Background())
	if err != nil {
		t.Fatalf("ListTeam: %v", err)
	}
	if len(members) != 4 {
		t.Fatalf("members = %d, want 4 across 3 pages", len(members))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 3 || queries[0] != "limit=500" || queries[1] != "cursor=c2&limit=500" {
		t.Errorf("queries = %v", queries)
	}
}

func TestListTeam_RepeatedCursorStops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Next-Cursor", "same")
		_ = json.NewEncoder(w).Encode([]TeamMember{{UserID: "u"}})
	}))
	defer srv.Close()
	withExternalToken(t, "tok")
	h := NewHandoffHandler(makeTestClient(t, srv.URL), nil, nil)
	members, err := h.ListTeam(context.Background())
	if err != nil || len(members) != 2 {
		t.Fatalf("members=%d err=%v, want 2 pages then stop on the repeated cursor", len(members), err)
	}
}
