package fleet

// catalog_lifecycle_test.go — skill-library-01SKLIB01 WP01: lifecycle on the
// UNSIGNED catalog list/fetch wire, and a revoked fetch as a named error.
//
// The JSON bodies below are fleet's LIVE shapes (kenaz-fleet main @ b57b2ec,
// service/handlers_catalog.go CatalogItemMetaAPI / CatalogFetchResponse and
// httpcore.ErrorResponse), not the harness's own struct re-encoded.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// lifecycleCatalogServer serves a fixed list body and a per-path fetch
// response, and records every list request's raw query (mutex: the client
// may run on another goroutine than the assertion).
type lifecycleCatalogServer struct {
	listStatus int
	listBody   string
	fetch      map[string]struct {
		status int
		body   string
	}

	mu      sync.Mutex
	queries []string
}

func (s *lifecycleCatalogServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/v1/catalog/list" {
		s.mu.Lock()
		s.queries = append(s.queries, r.URL.RawQuery)
		s.mu.Unlock()
		st := s.listStatus
		if st == 0 {
			st = http.StatusOK
		}
		w.WriteHeader(st)
		_, _ = w.Write([]byte(s.listBody))
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/api/v1/catalog/")
	if f, ok := s.fetch[key]; ok {
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.body))
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"code":"not_found","message":"catalog item not found or not accessible"}`))
}

func (s *lifecycleCatalogServer) listQueries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...)
}

func newLifecycleClient(t *testing.T, s *lifecycleCatalogServer) *Client {
	t.Helper()
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	return makeTestClient(t, srv.URL)
}

// Live fleet list body: an active row, a deprecated row pointing at its
// successor, a revoked row (no payload, revoked_at set), and an unknown
// future state.
const liveLifecycleListBody = `{"items":[
 {"id":"a1","owner_user_id":"u","kind":"skill","slug":"deploy","version":"1.0.0","visibility":"team","description":"d","signature":"","mandated":false,"published_at":"2026-10-01T00:00:00Z","mandated_reviewed":false,"lifecycle":"active"},
 {"id":"d1","owner_user_id":"u","kind":"skill","slug":"deploy","version":"0.9.0","visibility":"team","description":"d","signature":"","mandated":false,"published_at":"2026-09-01T00:00:00Z","mandated_reviewed":false,"lifecycle":"deprecated","superseded_by":"a1"},
 {"id":"r1","owner_user_id":"u","kind":"workflow","slug":"nightly","version":"2","visibility":"org_public","description":"w","signature":"","mandated":false,"published_at":"2026-08-01T00:00:00Z","mandated_reviewed":false,"lifecycle":"revoked","revoked_at":"2026-10-05T10:00:00Z"},
 {"id":"x1","owner_user_id":"u","kind":"skill","slug":"future","version":"1","visibility":"team","description":"","signature":"","mandated":false,"published_at":"2026-10-02T00:00:00Z","mandated_reviewed":false,"lifecycle":"quarantined"}
]}`

func TestCatalogList_DecodesLifecycle(t *testing.T) {
	c := newLifecycleClient(t, &lifecycleCatalogServer{listBody: liveLifecycleListBody})
	items, err := c.List(context.Background(), CatalogFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byID := map[string]CatalogItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if got := byID["a1"]; got.EffectiveLifecycle() != CatalogLifecycleActive || got.IsRevoked() {
		t.Errorf("a1 = %+v, want active", got)
	}
	if got := byID["d1"]; got.Lifecycle != CatalogLifecycleDeprecated || got.SupersededBy != "a1" {
		t.Errorf("d1 lifecycle=%q superseded_by=%q, want deprecated/a1", got.Lifecycle, got.SupersededBy)
	}
	r := byID["r1"]
	if !r.IsRevoked() || r.RevokedAt == nil || !r.RevokedAt.Equal(time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("r1 = %+v, want revoked with revoked_at", r)
	}
	// Forward compat: an unknown state is preserved verbatim, never an
	// error, and is NOT revocation.
	if x := byID["x1"]; x.EffectiveLifecycle() != "quarantined" || x.IsRevoked() {
		t.Errorf("x1 lifecycle=%q revoked=%v, want quarantined/false", x.EffectiveLifecycle(), x.IsRevoked())
	}
}

// AC-6: a pre-0114 fleet sends no lifecycle key at all — every item reads
// as active, nothing is revoked, and nothing errors.
func TestCatalogList_Pre0114FleetIsActive(t *testing.T) {
	body := `{"items":[{"id":"a1","owner_user_id":"u","kind":"skill","slug":"s","version":"1","visibility":"team","description":"","signature":"","mandated":false,"published_at":"2026-10-01T00:00:00Z"}]}`
	c := newLifecycleClient(t, &lifecycleCatalogServer{listBody: body})
	items, err := c.List(context.Background(), CatalogFilter{})
	if err != nil || len(items) != 1 {
		t.Fatalf("List = %v, %v", items, err)
	}
	if items[0].Lifecycle != "" || items[0].EffectiveLifecycle() != CatalogLifecycleActive || items[0].IsRevoked() {
		t.Errorf("pre-0114 item = %+v, want empty lifecycle read as active", items[0])
	}
}

// The list request never carries fleet's ?lifecycle= filter: the no-param
// default is what includes revoked rows (fleet answer OQ-2, 2026-10-06).
func TestCatalogList_NeverSendsLifecycleFilter(t *testing.T) {
	s := &lifecycleCatalogServer{listBody: `{"items":[]}`}
	c := newLifecycleClient(t, s)
	for _, f := range []CatalogFilter{{}, {Kind: CatalogKindSkill}, {Kind: CatalogKindWorkflow, Visibility: CatalogVisTeam}} {
		if _, err := c.List(context.Background(), f); err != nil {
			t.Fatalf("List(%+v): %v", f, err)
		}
	}
	for _, q := range s.listQueries() {
		if strings.Contains(q, "lifecycle") {
			t.Errorf("list query %q carries a lifecycle filter — revoked rows would be hidden", q)
		}
	}
}

func TestCatalogList_StatusErrorIsTyped(t *testing.T) {
	c := newLifecycleClient(t, &lifecycleCatalogServer{
		listStatus: http.StatusForbidden,
		listBody:   `{"code":"tier_required","message":"nope"}`,
	})
	_, err := c.List(context.Background(), CatalogFilter{})
	var se *CatalogStatusError
	if !errors.As(err, &se) || se.Status != http.StatusForbidden || se.Code != "tier_required" {
		t.Fatalf("err = %v, want *CatalogStatusError 403 tier_required", err)
	}
	if !strings.Contains(err.Error(), "fleet/catalog: list: status 403") {
		t.Errorf("message %q lost the historical prefix", err.Error())
	}
}

func TestFetchCatalogItem_DecodesLifecycle(t *testing.T) {
	s := &lifecycleCatalogServer{fetch: map[string]struct {
		status int
		body   string
	}{
		"d1@0.9.0": {http.StatusOK, `{"id":"d1","kind":"skill","slug":"deploy","version":"0.9.0","visibility":"team","description":"d","payload":"eyJpZCI6ImRlcGxveSJ9","signature":"","mandated":false,"published_at":"2026-09-01T00:00:00Z","lint_blocking":[],"lint_warnings":[],"mandated_reviewed":false,"payload_sha256":"x","size_bytes":15,"lifecycle":"deprecated","superseded_by":"a1"}`},
	}}
	c := newLifecycleClient(t, s)
	it, err := FetchCatalogItem(context.Background(), c, "d1", "0.9.0")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if it.Lifecycle != CatalogLifecycleDeprecated || it.SupersededBy != "a1" || string(it.PayloadBytes) != `{"id":"deploy"}` {
		t.Errorf("fetched = %+v", it)
	}
}

// AC-3: a revoked version fetches as the NAMED error with clear copy — not
// "status 410".
func TestFetchCatalogItem_RevokedIsNamedError(t *testing.T) {
	s := &lifecycleCatalogServer{fetch: map[string]struct {
		status int
		body   string
	}{
		"r1@2": {http.StatusGone, `{"code":"item_revoked","message":"this version was revoked and can no longer be downloaded","details":{"catalog_id":"r1","version":"2"}}`},
		// A 410 without fleet's code is not claimed as revocation.
		"g1@1": {http.StatusGone, `gone`},
	}}
	c := newLifecycleClient(t, s)
	_, err := FetchCatalogItem(context.Background(), c, "r1", "2")
	if !errors.Is(err, ErrCatalogItemRevoked) {
		t.Fatalf("err = %v, want ErrCatalogItemRevoked", err)
	}
	if strings.Contains(err.Error(), "410") || !strings.Contains(err.Error(), "revoked by your org") {
		t.Errorf("message %q: want the user-facing revoked copy, no raw status", err.Error())
	}
	_, err = FetchCatalogItem(context.Background(), c, "g1", "1")
	var se *CatalogStatusError
	if errors.Is(err, ErrCatalogItemRevoked) || !errors.As(err, &se) || se.Status != http.StatusGone {
		t.Errorf("code-less 410 = %v, want a plain *CatalogStatusError", err)
	}
}

// AC-6 signed-wire freeze: lifecycle travels ONLY on the unsigned catalog
// wire. BundleMandatedItem — re-marshalled harness-side, with no omitempty,
// to verify fleet's signature — keeps exactly its v0.91 field set; any added
// field would break signature verification on every deployed harness (fleet
// §5.4). TestSigningPayload_MandatedItems_ByteOrder pins the bytes.
func TestBundleMandatedItem_FieldSetFrozen(t *testing.T) {
	want := []string{`CatalogID json:"catalog_id"`, `Kind json:"kind"`, `Version json:"version"`, `Payload json:"payload"`}
	typ := reflect.TypeOf(BundleMandatedItem{})
	var got []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		got = append(got, f.Name+" "+string(f.Tag))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BundleMandatedItem drifted (signed wire is frozen):\n got %q\nwant %q", got, want)
	}
}
