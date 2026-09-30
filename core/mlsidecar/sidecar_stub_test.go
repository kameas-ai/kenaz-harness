package mlsidecar

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// stubSidecar is a test-only HTTP server speaking the design's wire
// shapes (§3.3): /health, /status, /v1/contracts, /v1/clients/lease,
// /v1/admin/shutdown, /v1/recommend/{kind}, /v1/systemone. The real
// kenaz-ml sidecar does not serve any of this yet (design §9 Phase 1) —
// tasks.md's WP12 row requires the harness's lifecycle manager and
// ladder to be "built against a stub HTTP server ... regardless" of the
// ml team's packaging choices, so this is the ENTIRE far end every test
// in this package talks to. No Python, no real network — httptest only.
type stubSidecar struct {
	mu sync.Mutex

	health       HealthPayload
	healthStatus int // 0 defaults to 200; non-2xx simulates "unreachable"/refused
	// healthRaw, when non-nil, is written to the /health response VERBATIM
	// instead of encoding `health` — used by the security-review's
	// bare-{} regression pin to send the reviewer's EXACT exploit bytes
	// rather than a Go zero-value struct that merely encodes similarly.
	healthRaw []byte

	contracts ContractsPayload

	leaseStatus   int // 0 defaults to 200; 404 simulates a legacy pre-lease engine
	leaseResponse LeaseWireResponse
	leaseCalls    []LeaseWireRequest

	shutdownRequireToken string // "" accepts any Authorization header
	shutdownCalls        []string

	// /v1/recommend scripting (WP15).
	recommendCalls   []string
	recommendRefused map[string]bool
	recommendScript  map[string]RecommendResponse

	// /v1/labels ingest (WP14): the mirror + fault knobs.
	labelMirror      map[string]storedLabel
	labelPosts       []LabelPushRequest
	labelsFailStatus int       // non-2xx simulates an engine error
	labelsAckLimit   int       // >0: apply/ack only the first N rows of a batch
	labelsBogusAck   *LabelAck // non-nil: respond with this ack regardless

	srv *httptest.Server
}

func newStubSidecar() *stubSidecar {
	s := &stubSidecar{
		leaseResponse: LeaseWireResponse{LeaseID: "lease-1", ExpiresInS: 90},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/v1/contracts", s.handleContracts)
	mux.HandleFunc("/v1/clients/lease", s.handleLease)
	mux.HandleFunc("/v1/admin/shutdown", s.handleShutdown)
	mux.HandleFunc("/v1/recommend/", s.handleRecommend)
	mux.HandleFunc("/v1/labels/", s.handleLabels)
	mux.HandleFunc("/v1/systemone", s.handleSystemOne)
	s.srv = httptest.NewServer(mux)
	return s
}

func (s *stubSidecar) Close()      { s.srv.Close() }
func (s *stubSidecar) URL() string { return s.srv.URL }

func (s *stubSidecar) setHealth(h HealthPayload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health = h
}

func (s *stubSidecar) setHealthStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthStatus = code
}

// setHealthRaw makes /health respond with body verbatim, bypassing JSON
// encoding of a HealthPayload entirely.
func (s *stubSidecar) setHealthRaw(body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthRaw = body
}

func (s *stubSidecar) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	code := s.healthStatus
	h := s.health
	raw := s.healthRaw
	s.mu.Unlock()
	if code != 0 && code != http.StatusOK {
		w.WriteHeader(code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if raw != nil {
		_, _ = w.Write(raw)
		return
	}
	_ = json.NewEncoder(w).Encode(h)
}

func (s *stubSidecar) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	h := s.health
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(StatusPayload{HealthPayload: h, UptimeSeconds: 12.5})
}

func (s *stubSidecar) setContracts(c ContractsPayload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contracts = c
}

func (s *stubSidecar) handleContracts(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	c := s.contracts
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c)
}

func (s *stubSidecar) setLeaseStatus(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leaseStatus = code
}

func (s *stubSidecar) handleLease(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	code := s.leaseStatus
	resp := s.leaseResponse
	s.mu.Unlock()
	if code != 0 && code != http.StatusOK {
		w.WriteHeader(code)
		return
	}
	var req LeaseWireRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.mu.Lock()
	s.leaseCalls = append(s.leaseCalls, req)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *stubSidecar) leaseCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.leaseCalls)
}

func (s *stubSidecar) setShutdownRequireToken(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shutdownRequireToken = tok
}

func (s *stubSidecar) handleShutdown(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	s.mu.Lock()
	s.shutdownCalls = append(s.shutdownCalls, auth)
	want := s.shutdownRequireToken
	s.mu.Unlock()
	if want != "" && auth != "Bearer "+want {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *stubSidecar) shutdownCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.shutdownCalls)
}

func (s *stubSidecar) handleRecommend(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimPrefix(r.URL.Path, "/v1/recommend/")
	s.mu.Lock()
	s.recommendCalls = append(s.recommendCalls, kind)
	refused := s.recommendRefused[kind]
	scripted, hasScript := s.recommendScript[kind]
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if refused {
		// Amendment A3.2: typed "kind not served" refusal.
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": KindNotServedCode})
		return
	}
	if hasScript {
		_ = json.NewEncoder(w).Encode(scripted)
		return
	}
	_ = json.NewEncoder(w).Encode(RecommendResponse{Confidence: 80, Backend: "heuristic", KindID: "branch_now"})
}

func (s *stubSidecar) setRecommend(kind string, resp RecommendResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recommendScript == nil {
		s.recommendScript = map[string]RecommendResponse{}
	}
	s.recommendScript[kind] = resp
}

func (s *stubSidecar) refuseRecommend(kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recommendRefused == nil {
		s.recommendRefused = map[string]bool{}
	}
	s.recommendRefused[kind] = true
}

func (s *stubSidecar) recommendCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recommendCalls)
}

// ---- label ingest (WP14; design §5.2 + Amendment A3.3, frozen) ----

// storedLabel is one row of the stub's mirror, keyed exactly like the real
// engine's ingest: (client, kind, features_hash, ts), higher revision wins.
type storedLabel struct {
	client string
	row    LabelWireRow
}

func labelKey(client string, r LabelWireRow) string {
	return fmt.Sprintf("%s|%s|%s|%d", client, r.Kind, r.FeaturesHash, r.TS)
}

// handleLabels implements POST /v1/labels/{kind}: revision upsert +
// cursor ack. The ack is the (ts, revision) of the LAST row of the batch
// the stub applied — or of the ackLimit-th row when a partial ack is
// scripted. Every request is recorded (labelPosts) for call-count proofs.
func (s *stubSidecar) handleLabels(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimPrefix(r.URL.Path, "/v1/labels/")
	var req LabelPushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.labelPosts = append(s.labelPosts, req)
	fail := s.labelsFailStatus
	limit := s.labelsAckLimit
	badAck := s.labelsBogusAck
	if fail != 0 {
		s.mu.Unlock()
		w.WriteHeader(fail)
		return
	}
	if s.labelMirror == nil {
		s.labelMirror = map[string]storedLabel{}
	}
	var resp LabelPushResponse
	rows := req.Rows
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	for _, row := range rows {
		k := labelKey(req.Client, row)
		prev, exists := s.labelMirror[k]
		switch {
		case !exists:
			s.labelMirror[k] = storedLabel{client: req.Client, row: row}
			resp.Applied++
		case row.Revision > prev.row.Revision:
			s.labelMirror[k] = storedLabel{client: req.Client, row: row}
			resp.Replaced++
		default:
			resp.Stale++
		}
	}
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		resp.Acked = LabelAck{TS: last.TS, Revision: last.Revision}
	}
	if badAck != nil {
		resp.Acked = *badAck
	}
	s.mu.Unlock()
	_ = kind
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *stubSidecar) setLabelsFail(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.labelsFailStatus = code
}

func (s *stubSidecar) setLabelsAckLimit(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.labelsAckLimit = n
}

func (s *stubSidecar) setLabelsBogusAck(a *LabelAck) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.labelsBogusAck = a
}

// dropLabelMirror simulates mirror loss (the engine's retained set wiped).
func (s *stubSidecar) dropLabelMirror() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.labelMirror = nil
}

func (s *stubSidecar) labelPostCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.labelPosts)
}

// labelPostSnapshot returns a copy of every recorded POST body.
func (s *stubSidecar) labelPostSnapshot() []LabelPushRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LabelPushRequest, len(s.labelPosts))
	copy(out, s.labelPosts)
	return out
}

// mirrorSnapshot returns the stub's stored labels keyed by
// (client|kind|features_hash|ts).
func (s *stubSidecar) mirrorSnapshot() map[string]LabelWireRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]LabelWireRow, len(s.labelMirror))
	for k, v := range s.labelMirror {
		out[k] = v.row
	}
	return out
}

func (s *stubSidecar) handleSystemOne(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(SystemOneResponse{Answer: "true", AnswerConfidence: 0.9, Confidence: 0.5})
}
