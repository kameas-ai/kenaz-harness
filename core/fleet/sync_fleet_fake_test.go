package fleet

// sync_fleet_fake_test.go — an httptest stand-in for kenaz-fleet's
// settings-sync handlers (service/handlers_sync.go at the commit named in
// testdata/sync/PROVENANCE.md). It is deliberately as strict as Fleet on the
// points the pre-A2 harness got wrong, so a regression to that wire fails:
//
//   - only PUT and GET are routed; POST (the old push) gets 405;
//   - the body decodes into Fleet's own PutSyncRequest shape, where lww_ts is
//     a string, so an int64 lww_ts fails to decode: 400 invalid_request_body;
//   - lww_ts and ?since= must parse as RFC 3339: 400 invalid_lww_ts /
//     invalid_since;
//   - categories outside Fleet's five: 422 unknown_category on PUT, 400 on GET;
//   - lww_ts <= stored: 409 stale_lww_ts; lww_ts > now+5m: 422 lww_ts_in_future;
//   - storage keeps microsecond precision, as Postgres TIMESTAMPTZ does;
//   - an absent row answers 200 with the zero time and payload {}.
//
// Error bodies use Fleet's httpcore.ErrorResponse envelope.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fleetPutSyncRequest copies kenaz-fleet's PutSyncRequest field for field.
type fleetPutSyncRequest struct {
	LWWTs   string          `json:"lww_ts"`
	Payload json.RawMessage `json:"payload"`
}

// fleetSyncStateResponse copies kenaz-fleet's SyncStateResponse.
type fleetSyncStateResponse struct {
	Category  string          `json:"category"`
	LWWTs     string          `json:"lww_ts"`
	Payload   json.RawMessage `json:"payload"`
	UpdatedAt string          `json:"updated_at"`
}

// fleetSyncCategories mirrors kenaz-fleet's ParseSyncCategory.
var fleetSyncCategories = map[string]bool{
	"provider_profiles": true,
	"model_prefs":       true,
	"mcp_recipes":       true,
	"installed_mcp":     true,
	"ui_theme":          true,
}

type fakeFleetRow struct {
	LWWTs   time.Time
	Payload json.RawMessage
}

type fakeFleetRequest struct {
	Method string
	Path   string
	Query  string
	Body   string
}

type fakeFleetSync struct {
	mu       sync.Mutex
	rows     map[string]fakeFleetRow
	requests []fakeFleetRequest
	// now is the server clock; tests may move it.
	now func() time.Time
	// putStatusOverride, when non-zero, answers every PUT with this status
	// and error code instead of storing (for error-path tests).
	putStatusOverride int
	putCodeOverride   string
	// omitDate suppresses the Date header (net/http adds one otherwise).
	omitDate bool
}

func newFakeFleetSync(t *testing.T) *fakeFleetSync {
	t.Helper()
	return &fakeFleetSync{rows: map[string]fakeFleetRow{}, now: time.Now}
}

func writeFleetErr(w http.ResponseWriter, status int, code string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"message": "fake fleet: " + code,
		"details": details,
	})
}

func (f *fakeFleetSync) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const prefix = "/api/v1/sync/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	cat := strings.TrimPrefix(r.URL.Path, prefix)
	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, _ = io.ReadAll(r.Body)
	}
	f.mu.Lock()
	f.requests = append(f.requests, fakeFleetRequest{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: string(bodyBytes),
	})
	serverNow, omitDate := f.now(), f.omitDate
	f.mu.Unlock()
	// Every Fleet response carries the server clock in Date (net/http adds
	// it); send the fake's own clock so a test can skew it.
	if omitDate {
		w.Header()["Date"] = nil
	} else {
		w.Header().Set("Date", serverNow.UTC().Format(http.TimeFormat))
	}

	switch r.Method {
	case http.MethodPut:
		f.handlePut(w, cat, bodyBytes)
	case http.MethodGet:
		f.handleGet(w, r, cat)
	default:
		// Go 1.22 method-pattern mux: an unrouted method is 405.
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeFleetSync) handlePut(w http.ResponseWriter, cat string, body []byte) {
	if !fleetSyncCategories[cat] {
		writeFleetErr(w, http.StatusUnprocessableEntity, "unknown_category", nil)
		return
	}
	var req fleetPutSyncRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeFleetErr(w, http.StatusBadRequest, "invalid_request_body", nil)
		return
	}
	if req.LWWTs == "" {
		writeFleetErr(w, http.StatusBadRequest, "missing_lww_ts", nil)
		return
	}
	ts, err := time.Parse(time.RFC3339Nano, req.LWWTs)
	if err != nil {
		writeFleetErr(w, http.StatusBadRequest, "invalid_lww_ts", nil)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putStatusOverride != 0 {
		writeFleetErr(w, f.putStatusOverride, f.putCodeOverride,
			map[string]any{"path": "$.theme", "reason": "fixed reason", "category": cat})
		return
	}
	if ts.After(f.now().Add(5 * time.Minute)) {
		writeFleetErr(w, http.StatusUnprocessableEntity, "lww_ts_in_future", nil)
		return
	}
	if len(req.Payload) == 0 || string(req.Payload) == "null" {
		req.Payload = json.RawMessage(`{}`)
	}
	ts = ts.Truncate(time.Microsecond) // TIMESTAMPTZ resolution
	if prev, ok := f.rows[cat]; ok && !ts.After(prev.LWWTs) {
		writeFleetErr(w, http.StatusConflict, "stale_lww_ts", map[string]any{"lww_ts": req.LWWTs})
		return
	}
	f.rows[cat] = fakeFleetRow{LWWTs: ts, Payload: req.Payload}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(fleetSyncStateResponse{
		Category: cat, LWWTs: ts.UTC().Format(time.RFC3339Nano), Payload: req.Payload,
		UpdatedAt: f.now().UTC().Format(time.RFC3339Nano),
	})
}

func (f *fakeFleetSync) handleGet(w http.ResponseWriter, r *http.Request, cat string) {
	if !fleetSyncCategories[cat] {
		writeFleetErr(w, http.StatusBadRequest, "unknown_category", map[string]any{"category": cat})
		return
	}
	var since time.Time
	if s := r.URL.Query().Get("since"); s != "" {
		var err error
		if since, err = time.Parse(time.RFC3339Nano, s); err != nil {
			writeFleetErr(w, http.StatusBadRequest, "invalid_since", map[string]any{"since": s})
			return
		}
	}
	f.mu.Lock()
	row, ok := f.rows[cat]
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(fleetSyncStateResponse{
			Category: cat, LWWTs: time.Time{}.UTC().Format(time.RFC3339Nano),
			Payload: json.RawMessage(`{}`), UpdatedAt: time.Time{}.UTC().Format(time.RFC3339Nano),
		})
		return
	}
	if !since.IsZero() && !row.LWWTs.After(since) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(fleetSyncStateResponse{
		Category: cat, LWWTs: row.LWWTs.UTC().Format(time.RFC3339Nano),
		Payload: row.Payload, UpdatedAt: f.now().UTC().Format(time.RFC3339Nano),
	})
}

// get returns the stored row for cat.
func (f *fakeFleetSync) get(cat SyncCategory) (fakeFleetRow, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[string(cat)]
	return row, ok
}

// put seeds a row as if another device had pushed it.
func (f *fakeFleetSync) put(cat SyncCategory, ts time.Time, payload string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[string(cat)] = fakeFleetRow{LWWTs: ts.Truncate(time.Microsecond), Payload: json.RawMessage(payload)}
}

// snapshot returns a copy of every request received.
func (f *fakeFleetSync) snapshot() []fakeFleetRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeFleetRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

// countMethod counts received requests with the given method.
func (f *fakeFleetSync) countMethod(method string) int {
	n := 0
	for _, r := range f.snapshot() {
		if r.Method == method {
			n++
		}
	}
	return n
}
