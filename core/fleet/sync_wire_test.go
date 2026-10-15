package fleet

// sync_wire_test.go — the settings-sync wire against kenaz-fleet's documented
// contract (provider-credentials-client-01PCCLI01 A2). The fixtures under
// testdata/sync/ are Fleet's shapes (see PROVENANCE.md); the fake in
// sync_fleet_fake_test.go is as strict as Fleet where the pre-A2 harness was
// wrong (POST → 405, int64 lww_ts → 400, ?since=<int> → 400).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func readSyncFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "sync", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func jsonKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// newWireRig builds a Syncer on a strict fake Fleet with one category
// registered and enabled without the enable-time push.
func newWireRig(t *testing.T, cat SyncCategory, payload string, applied *atomic.Value) (*Syncer, *fakeFleetSync) {
	t.Helper()
	fake := newFakeFleetSync(t)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	stubTokens(t, TokenSet{AccessToken: "at-wire", RefreshToken: "rt-wire", ExpiresAt: time.Now().Add(time.Hour)})
	syncer := NewSyncer(makeTestClient(t, srv.URL))
	t.Cleanup(syncer.Stop)
	syncer.SetUploadGate(func() bool { return true }) // a user who has confirmed
	syncer.RegisterCategory(cat, CategoryConfig{
		Collector: func(context.Context) (json.RawMessage, error) { return json.RawMessage(payload), nil },
		Applier: func(_ context.Context, raw json.RawMessage) error {
			if applied != nil {
				applied.Store(string(raw))
			}
			return nil
		},
	})
	syncer.categories[cat].mu.Lock()
	syncer.categories[cat].enabled = true
	syncer.categories[cat].mu.Unlock()
	return syncer, fake
}

func lwwOf(s *Syncer, cat SyncCategory) int64 {
	st := s.categories[cat]
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.lwwTS
}

func lastErrOf(s *Syncer, cat SyncCategory) string {
	st := s.categories[cat]
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.lastErr
}

// TestSyncWire_RequestMatchesFleetFixture: the PUT body the harness encodes
// has exactly Fleet's PutSyncRequest keys, with lww_ts as an RFC 3339 string.
func TestSyncWire_RequestMatchesFleetFixture(t *testing.T) {
	fixture := readSyncFixture(t, "put_request_ui_theme.json")
	var want fleetPutSyncRequest
	if err := json.Unmarshal(fixture, &want); err != nil {
		t.Fatalf("fixture is not a Fleet PutSyncRequest: %v", err)
	}
	ns, err := parseLWW(want.LWWTs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(syncPutRequest{LWWTs: formatLWW(ns), Payload: want.Payload})
	if err != nil {
		t.Fatal(err)
	}
	if g, w := jsonKeys(t, got), jsonKeys(t, fixture); strings.Join(g, ",") != strings.Join(w, ",") {
		t.Fatalf("request keys = %v, Fleet's = %v", g, w)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(string(fixture)) {
		t.Errorf("request body = %s\nFleet fixture = %s", got, fixture)
	}
}

// TestSyncWire_ResponsesDecode: every Fleet response fixture decodes into the
// harness's response type, with lww_ts as comparable Unix nanos.
func TestSyncWire_ResponsesDecode(t *testing.T) {
	wantTS := time.Date(2026, 10, 15, 12, 0, 0, 123456000, time.UTC).UnixNano()
	for _, name := range []string{"put_response_ui_theme.json", "get_response_model_prefs.json"} {
		var r syncStateResponse
		if err := json.Unmarshal(readSyncFixture(t, name), &r); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if int64(r.LWWTs) != wantTS {
			t.Errorf("%s: lww_ts = %d, want %d", name, int64(r.LWWTs), wantTS)
		}
		if len(r.Payload) == 0 || r.Category == "" {
			t.Errorf("%s: decoded %+v", name, r)
		}
	}
	var absent syncStateResponse
	if err := json.Unmarshal(readSyncFixture(t, "get_response_absent.json"), &absent); err != nil {
		t.Fatal(err)
	}
	if absent.LWWTs != 0 {
		t.Errorf("absent row lww_ts = %d, want 0 (Fleet's zero time)", absent.LWWTs)
	}
	for _, name := range []string{
		"error_409_stale_lww_ts.json", "error_422_lww_ts_in_future.json",
		"error_422_unknown_category.json", "error_422_schema_violation.json",
		"error_400_invalid_lww_ts.json",
	} {
		var e fleetErrorBody
		if err := json.Unmarshal(readSyncFixture(t, name), &e); err != nil || e.Code == "" {
			t.Errorf("%s: not a Fleet ErrorResponse (%v, %+v)", name, err, e)
		}
	}
}

// TestSyncWire_Int64LWWRejectedOnTheWire: Fleet has no int64 path (its
// compatibility shim was dropped, kenaz-fleet PR #266), so an int64 lww_ts
// in a response is malformed, not a legacy form to honour. Nothing local
// holds an int64 form either (lwwTS is never persisted), so no decoder for
// one exists.
func TestSyncWire_Int64LWWRejectedOnTheWire(t *testing.T) {
	var r syncStateResponse
	if err := json.Unmarshal([]byte(`{"category":"ui_theme","lww_ts":1760529600123456789,"payload":{}}`), &r); err == nil {
		t.Error("an int64 lww_ts on the wire must not decode")
	}
	if err := json.Unmarshal([]byte(`{"lww_ts":"yesterday"}`), &r); err == nil {
		t.Error("a non-RFC 3339 string must not decode")
	}
}

// TestSyncWire_NoGateNoUpload: a Syncer nobody has given a gate never
// uploads, whatever triggers the push: enable, a forced push, the held
// flush, or a debounced change. Fail closed, so no boot-order slip or first
// poll cycle can upload before the user's acknowledgement.
func TestSyncWire_NoGateNoUpload(t *testing.T) {
	fake := newFakeFleetSync(t)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	stubTokens(t, TokenSet{AccessToken: "at-nogate", RefreshToken: "rt-nogate", ExpiresAt: time.Now().Add(time.Hour)})
	s := NewSyncer(makeTestClient(t, srv.URL))
	t.Cleanup(s.Stop)
	s.RegisterCategory(SyncCategoryUITheme, CategoryConfig{
		Collector: func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{"theme":"dark"}`), nil },
		Applier:   func(context.Context, json.RawMessage) error { return nil },
	})
	ctx := context.Background()
	if err := s.SetEnabled(ctx, SyncCategoryUITheme, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if err := s.Push(ctx, SyncCategoryUITheme); !errors.Is(err, ErrSyncUploadNotConfirmed) {
		t.Errorf("Push = %v, want ErrSyncUploadNotConfirmed", err)
	}
	_ = s.PushHeld(ctx)
	if n := fake.countMethod(http.MethodPut); n != 0 {
		t.Fatalf("%d PUTs with no gate installed, want 0", n)
	}
	s.SetUploadGate(func() bool { return false })
	_ = s.PushHeld(ctx)
	s.SetUploadGate(nil) // removing a gate closes uploads again
	_ = s.PushHeld(ctx)
	if n := fake.countMethod(http.MethodPut); n != 0 {
		t.Fatalf("%d PUTs, want 0", n)
	}
}

// TestSyncWire_PushIsPUTAndRoundTrips is the regression pin for the
// never-round-tripped wire: device A pushes with PUT + RFC 3339, device B
// pulls it, and B's next pull sends ?since=<RFC 3339> and gets 304. Against
// the pre-A2 code this fails: POST → 405.
func TestSyncWire_PushIsPUTAndRoundTrips(t *testing.T) {
	devA, fake := newWireRig(t, SyncCategoryUITheme, `{"theme":"dark"}`, nil)
	if err := devA.Push(context.Background(), SyncCategoryUITheme); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if n := fake.countMethod(http.MethodPost); n != 0 {
		t.Fatalf("%d POSTs; Fleet routes only PUT", n)
	}
	reqs := fake.snapshot()
	if len(reqs) != 1 || reqs[0].Method != http.MethodPut || reqs[0].Path != "/api/v1/sync/ui_theme" {
		t.Fatalf("requests = %+v, want one PUT /api/v1/sync/ui_theme", reqs)
	}
	var body fleetPutSyncRequest
	if err := json.Unmarshal([]byte(reqs[0].Body), &body); err != nil {
		t.Fatalf("body is not Fleet's PutSyncRequest: %v (%s)", err, reqs[0].Body)
	}
	if _, err := time.Parse(time.RFC3339Nano, body.LWWTs); err != nil {
		t.Fatalf("lww_ts %q is not RFC 3339", body.LWWTs)
	}
	row, ok := fake.get(SyncCategoryUITheme)
	if !ok || string(row.Payload) != `{"theme":"dark"}` {
		t.Fatalf("Fleet row = %+v, %v", row, ok)
	}
	if got := lwwOf(devA, SyncCategoryUITheme); got != row.LWWTs.UnixNano() {
		t.Errorf("local lww %d != Fleet's stored %d", got, row.LWWTs.UnixNano())
	}

	// Device B: same Fleet, nothing local yet.
	var applied atomic.Value
	devB := NewSyncer(devA.client)
	t.Cleanup(devB.Stop)
	devB.RegisterCategory(SyncCategoryUITheme, CategoryConfig{
		Collector: func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{}`), nil },
		Applier: func(_ context.Context, raw json.RawMessage) error {
			applied.Store(string(raw))
			return nil
		},
	})
	devB.categories[SyncCategoryUITheme].enabled = true
	if err := devB.Pull(context.Background(), SyncCategoryUITheme); err != nil {
		t.Fatalf("B Pull: %v", err)
	}
	if got, _ := applied.Load().(string); got != `{"theme":"dark"}` {
		t.Fatalf("B applied %q", got)
	}
	applied.Store("")
	if err := devB.Pull(context.Background(), SyncCategoryUITheme); err != nil {
		t.Fatalf("B second Pull: %v", err)
	}
	if got, _ := applied.Load().(string); got != "" {
		t.Errorf("second pull re-applied %q; want 304", got)
	}
	last := fake.snapshot()[len(fake.snapshot())-1]
	q, _ := url.ParseQuery(last.Query)
	if _, err := time.Parse(time.RFC3339Nano, q.Get("since")); err != nil {
		t.Errorf("?since=%q is not RFC 3339", q.Get("since"))
	}
}

// TestSyncWire_AbsentRowNotApplied: Fleet's "nothing stored" answer (zero
// time, {}) must not overwrite local state.
func TestSyncWire_AbsentRowNotApplied(t *testing.T) {
	var applied atomic.Value
	s, _ := newWireRig(t, SyncCategoryModelPrefs, `{}`, &applied)
	if err := s.Pull(context.Background(), SyncCategoryModelPrefs); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if v := applied.Load(); v != nil {
		t.Errorf("absent row applied %v", v)
	}
}

// TestSyncWire_StaleWritePullsOnceNoRetry: 409 stale_lww_ts → exactly one
// pull (the newer remote write wins by LWW), no second PUT.
func TestSyncWire_StaleWritePullsOnceNoRetry(t *testing.T) {
	var applied atomic.Value
	s, fake := newWireRig(t, SyncCategoryUITheme, `{"theme":"dark"}`, &applied)
	fake.put(SyncCategoryUITheme, time.Now().Add(2*time.Minute), `{"theme":"light"}`)
	if err := s.Push(context.Background(), SyncCategoryUITheme); err != nil {
		t.Fatalf("Push after 409: %v", err)
	}
	if p, g := fake.countMethod(http.MethodPut), fake.countMethod(http.MethodGet); p != 1 || g != 1 {
		t.Errorf("PUTs=%d GETs=%d, want 1 and 1 (no retry)", p, g)
	}
	if got, _ := applied.Load().(string); got != `{"theme":"light"}` {
		t.Errorf("remote newer write not applied: %q", got)
	}
}

// TestSyncWire_ClockSkewNoRetry: 422 lww_ts_in_future → ErrSyncClockSkew,
// one PUT, a plain-language LastError.
func TestSyncWire_ClockSkewNoRetry(t *testing.T) {
	s, fake := newWireRig(t, SyncCategoryUITheme, `{"theme":"dark"}`, nil)
	fake.mu.Lock()
	fake.now = func() time.Time { return time.Now().Add(-10 * time.Minute) }
	fake.mu.Unlock()
	err := s.Push(context.Background(), SyncCategoryUITheme)
	if !errors.Is(err, ErrSyncClockSkew) {
		t.Fatalf("Push = %v, want ErrSyncClockSkew", err)
	}
	if n := fake.countMethod(http.MethodPut); n != 1 {
		t.Errorf("%d PUTs, want 1", n)
	}
	if !strings.Contains(lastErrOf(s, SyncCategoryUITheme), "clock") {
		t.Errorf("LastError = %q", lastErrOf(s, SyncCategoryUITheme))
	}
}

// TestSyncWire_SchemaRejectionRecorded: a 422 schema rejection is recorded
// with code and JSON path, never retried.
func TestSyncWire_SchemaRejectionRecorded(t *testing.T) {
	s, fake := newWireRig(t, SyncCategoryUITheme, `{"theme":"dark"}`, nil)
	fake.mu.Lock()
	fake.putStatusOverride, fake.putCodeOverride = http.StatusUnprocessableEntity, "schema_violation"
	fake.mu.Unlock()
	if err := s.Push(context.Background(), SyncCategoryUITheme); err == nil {
		t.Fatal("Push succeeded on a 422")
	}
	if n := fake.countMethod(http.MethodPut); n != 1 {
		t.Errorf("%d PUTs, want 1", n)
	}
	if le := lastErrOf(s, SyncCategoryUITheme); !strings.Contains(le, "schema_violation") || !strings.Contains(le, "$.theme") {
		t.Errorf("LastError = %q, want code and path", le)
	}
}

// TestSyncWire_UnknownCategoryRemembered: if Fleet 422s a category as
// unknown, the Syncer stops sending it.
func TestSyncWire_UnknownCategoryRemembered(t *testing.T) {
	s, fake := newWireRig(t, SyncCategoryUITheme, `{"theme":"dark"}`, nil)
	fake.mu.Lock()
	fake.putStatusOverride, fake.putCodeOverride = http.StatusUnprocessableEntity, "unknown_category"
	fake.mu.Unlock()
	if err := s.Push(context.Background(), SyncCategoryUITheme); !errors.Is(err, ErrSyncCategoryLocalOnly) {
		t.Fatalf("Push = %v", err)
	}
	_ = s.Push(context.Background(), SyncCategoryUITheme)
	_ = s.Pull(context.Background(), SyncCategoryUITheme)
	if n := len(fake.snapshot()); n != 1 {
		t.Errorf("%d requests after unknown_category, want 1", n)
	}
}

// TestSyncWire_SlashCommandsNeverLeaves: slash_commands is not in Fleet's
// sync_schema.go, so it is neither pushed nor pulled, and Status says so.
func TestSyncWire_SlashCommandsNeverLeaves(t *testing.T) {
	const slash SyncCategory = "slash_commands"
	s, fake := newWireRig(t, slash, `{"items":[]}`, nil)
	if err := s.Push(context.Background(), slash); !errors.Is(err, ErrSyncCategoryLocalOnly) {
		t.Errorf("Push = %v", err)
	}
	if err := s.Pull(context.Background(), slash); !errors.Is(err, ErrSyncCategoryLocalOnly) {
		t.Errorf("Pull = %v", err)
	}
	if n := len(fake.snapshot()); n != 0 {
		t.Errorf("%d requests for slash_commands, want 0", n)
	}
	for _, st := range s.Status() {
		if st.Category == slash && (!st.LocalOnly || st.AwaitingConfirmation) {
			t.Errorf("status %+v, want LocalOnly and not awaiting", st)
		}
	}
}

// TestSyncWire_UploadGate: while the gate is closed nothing is sent (not
// even on enable), Status reports the category as awaiting confirmation, and
// pulls still run. Once open, PushHeld uploads it.
func TestSyncWire_UploadGate(t *testing.T) {
	fake := newFakeFleetSync(t)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	stubTokens(t, TokenSet{AccessToken: "at-gate", RefreshToken: "rt-gate", ExpiresAt: time.Now().Add(time.Hour)})
	s := NewSyncer(makeTestClient(t, srv.URL))
	t.Cleanup(s.Stop)
	collected := 0
	s.RegisterCategory(SyncCategoryUITheme, CategoryConfig{
		Collector: func(context.Context) (json.RawMessage, error) {
			collected++
			return json.RawMessage(`{"theme":"dark"}`), nil
		},
		Applier: func(context.Context, json.RawMessage) error { return nil },
	})
	var open atomic.Bool
	s.SetUploadGate(open.Load)

	if err := s.SetEnabled(context.Background(), SyncCategoryUITheme, true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if err := s.Push(context.Background(), SyncCategoryUITheme); !errors.Is(err, ErrSyncUploadNotConfirmed) {
		t.Fatalf("Push = %v, want ErrSyncUploadNotConfirmed", err)
	}
	if n := fake.countMethod(http.MethodPut); n != 0 || collected != 0 {
		t.Fatalf("PUTs=%d collects=%d before confirmation, want 0", n, collected)
	}
	awaiting := false
	for _, st := range s.Status() {
		if st.Category == SyncCategoryUITheme {
			awaiting = st.AwaitingConfirmation
		}
	}
	if !awaiting {
		t.Error("Status should report ui_theme awaiting confirmation")
	}
	if err := s.Pull(context.Background(), SyncCategoryUITheme); err != nil {
		t.Fatalf("Pull while gated: %v", err)
	}
	if n := fake.countMethod(http.MethodGet); n != 1 {
		t.Errorf("GETs = %d while gated, want 1 (pulls are not gated)", n)
	}

	open.Store(true)
	if err := s.PushHeld(context.Background()); err != nil {
		t.Fatalf("PushHeld: %v", err)
	}
	if n := fake.countMethod(http.MethodPut); n != 1 {
		t.Errorf("PUTs after confirmation = %d, want 1", n)
	}
	for _, st := range s.Status() {
		if st.Category == SyncCategoryUITheme && st.AwaitingConfirmation {
			t.Error("still awaiting after confirmation")
		}
	}
}

// TestSyncWire_NextLWWMonotonic: a second push in the same microsecond still
// gets a strictly greater lww_ts (Fleet 409s an equal one).
func TestSyncWire_NextLWWMonotonic(t *testing.T) {
	far := time.Now().Add(time.Hour).UnixNano()
	if got := nextLWW(far); got <= far {
		t.Errorf("nextLWW(%d) = %d, want > prev", far, got)
	}
	if got := nextLWW(0); got%int64(time.Microsecond) != 0 {
		t.Errorf("nextLWW not microsecond-truncated: %d", got)
	}
}
