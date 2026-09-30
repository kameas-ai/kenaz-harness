package mlsidecar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RecommendResponse{Confidence: 80, Backend: "heuristic", KindID: "branch_now"})
}

func (s *stubSidecar) handleSystemOne(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(SystemOneResponse{Answer: "true", AnswerConfidence: 0.9, Confidence: 0.5})
}
