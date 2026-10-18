package mlsidecar

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// stubSidecar is a test-only HTTP server speaking the kenaz-ml engine's
// wire shapes: /health, /status, /v1/contracts, /v1/clients/lease,
// /v1/admin/shutdown, /v1/recommend/{kind}, /v1/labels/{kind},
// /v1/systemone. It is the CONTRACT MIRROR of the real engine
// (kenaz-ml two-client-engine-01MSK2EN, routes.py + advice/dispatch.py
// + advice/label_log.py, interop ruling 2026-09-30): status codes, error
// envelopes and field names follow the engine, not this package's own
// guesses — a drift between the two is a bug in whichever side moved,
// and the 2026-09-30 interop review found six. Keep it in lockstep with
// the engine's docs/openapi.json. No Python, no real network — httptest
// only.
type stubSidecar struct {
	mu sync.Mutex

	health       HealthPayload
	healthStatus int // 0 defaults to 200; non-2xx simulates "unreachable"/refused
	// healthRaw, when non-nil, is written to the /health response VERBATIM
	// instead of encoding `health` — used by the security-review's
	// bare-{} regression pin to send the reviewer's EXACT exploit bytes
	// rather than a Go zero-value struct that merely encodes similarly.
	healthRaw []byte
	// down makes /health drop the connection (transport error) — 'nothing
	// usable listening', distinct from an HTTP error status.
	down bool

	contracts ContractsPayload

	leaseStatus   int // 0 defaults to 200; 404 simulates a legacy pre-lease engine
	leaseResponse LeaseWireResponse
	leaseCalls    []LeaseWireRequest

	shutdownRequireToken string // "" accepts any Authorization header
	shutdownCalls        []string

	// /v1/recommend scripting (WP15). recommendRefused maps a kind to the
	// typed refusal reason the engine answers it with (kind_not_served,
	// laya_backend_not_installed, contract_mismatch, ...).
	recommendCalls   []string
	recommendBodies  []RecommendRequest
	recommendRefused map[string]string
	recommendScript  map[string]RecommendResponse
	recommendRaw     map[string][]byte // verbatim body, bypassing JSON encoding

	// /v1/labels ingest (WP14): the mirror + fault knobs.
	labelMirror      map[string]storedLabel
	labelPosts       []LabelPushRequest
	labelsFailStatus int                 // non-2xx simulates an engine error (409/404 carry the refusal envelope)
	labelsAckLimit   int                 // >0: apply/ack only the first N rows of a batch
	labelsBogusAck   *LabelAck           // non-nil: respond with this ack regardless
	labelsRefuseRev  map[int64]string    // revision -> PERMANENT per-row refusal reason (A4: acked past)
	labelsNullAck    bool                // emulate a pre-A4 engine that acks nothing past a leading refused row
	labelsContract   map[string][]string // kind -> contract feature names (engine _contract_refusal)

	srv *httptest.Server
}

func newStubSidecar() *stubSidecar {
	s := &stubSidecar{
		leaseResponse: LeaseWireResponse{
			LifecycleProtocol: 1, SidecarVersion: "1.0.0",
			ContractVersions:  map[string][]string{"branch_now": {"0123456789abcdef"}},
			IncompatibleKinds: map[string]string{},
			Client:            "harness", LiveLeases: 1, ImplicitLeaseSec: 90, IdleExitSec: 120, Managed: true,
		},
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

// setDown makes /health drop the connection without a response, so the
// client sees a transport error — modeling "nothing (usable) listening",
// distinct from an HTTP error status (ErrUnusableResponse: a live
// process whose answer this client cannot read).
func (s *stubSidecar) setDown(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = down
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
	down := s.down
	s.mu.Unlock()
	if down {
		hj, ok := w.(http.Hijacker)
		if !ok {
			panic("stubSidecar.setDown: ResponseWriter is not a Hijacker")
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			conn.Close()
		}
		return
	}
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

// handleStatus mirrors the engine's /status: the workbench poller
// readout, with NO identity fields (design Amendment A4).
func (s *stubSidecar) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"mode": "local", "cursor": nil, "latest_predictions": []any{}, "poller_running": false})
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
	// The engine: no/blank bearer -> 401, wrong token -> 403, both with
	// {"error": <reason>, "detail": ...}; success -> 202
	// {"status": "shutting_down"}.
	if want != "" && auth != "Bearer "+want {
		code, reason := http.StatusForbidden, "token_mismatch"
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")) == "" {
			code, reason = http.StatusUnauthorized, "no_token"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": reason, "detail": "shutdown refused: " + reason})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "shutting_down"})
}

// writeRefusal writes the engine's typed-refusal envelope: a top-level
// {"error": <code>} (what newStatusError parses) PLUS the
// {"refusal": {kind_id, reason, detail}} detail (dispatch.RecommendRefusal).
func writeRefusal(w http.ResponseWriter, status int, kind, reason, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":   reason,
		"refusal": map[string]string{"kind_id": kind, "reason": reason, "detail": detail},
	})
}

func (s *stubSidecar) shutdownCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.shutdownCalls)
}

func (s *stubSidecar) handleRecommend(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimPrefix(r.URL.Path, "/v1/recommend/")
	var body RecommendRequest
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	s.recommendCalls = append(s.recommendCalls, kind)
	s.recommendBodies = append(s.recommendBodies, body)
	reason, refused := s.recommendRefused[kind]
	scripted, hasScript := s.recommendScript[kind]
	s.mu.Unlock()
	if refused {
		// Amendment A3.2: every typed refusal is HTTP 422 with the
		// refusal envelope (dispatch.REFUSAL_STATUS_CODE).
		writeRefusal(w, http.StatusUnprocessableEntity, kind, reason, "kind unavailable: "+reason)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	s.mu.Lock()
	raw, hasRaw := s.recommendRaw[kind]
	s.mu.Unlock()
	if hasRaw {
		_, _ = w.Write(raw)
		return
	}
	if hasScript {
		_ = json.NewEncoder(w).Encode(scripted)
		return
	}
	yes := true
	_ = json.NewEncoder(w).Encode(RecommendResponse{Decision: &yes, Confidence: 80, Backend: "heuristic", KindID: kind,
		FeatureContractVersion: "0123456789abcdef", Model: "fixture/heuristic", Rung: "heuristic",
		CheckpointProvenance: "local", Generation: "0", Unbenchmarked: true})
}

func (s *stubSidecar) setRecommend(kind string, resp RecommendResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recommendScript == nil {
		s.recommendScript = map[string]RecommendResponse{}
	}
	s.recommendScript[kind] = resp
}

func (s *stubSidecar) setRecommendRaw(kind string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recommendRaw == nil {
		s.recommendRaw = map[string][]byte{}
	}
	s.recommendRaw[kind] = body
}

func (s *stubSidecar) refuseRecommend(kind string) {
	s.refuseRecommendWith(kind, KindNotServedCode)
}

func (s *stubSidecar) refuseRecommendWith(kind, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recommendRefused == nil {
		s.recommendRefused = map[string]string{}
	}
	s.recommendRefused[kind] = reason
}

// recommendBodySnapshot returns a copy of every recorded recommend body.
func (s *stubSidecar) recommendBodySnapshot() []RecommendRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RecommendRequest, len(s.recommendBodies))
	copy(out, s.recommendBodies)
	return out
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

// labelWireResponse is the engine's LabelBatchResponse as it goes on the
// wire: acked is a POINTER so a batch whose first row is refused answers
// `"acked": null`, exactly like the engine (a Go LabelAck value would
// encode {"ts":0,"revision":0} and hide the null path from every test).
type labelWireResponse struct {
	Acked    *LabelAck         `json:"acked"`
	Applied  int               `json:"applied"`
	Replaced int               `json:"replaced"`
	Stale    int               `json:"stale"`
	Refused  int               `json:"refused"`
	Refusals []LabelRowRefusal `json:"refusals"`
	// The engine's extras the harness does not read.
	RetainedAppended int  `json:"retained_appended"`
	RetainedRebuilt  bool `json:"retained_rebuilt"`
}

// handleLabels implements POST /v1/labels/{kind} the way the engine's
// label_log.ingest does (design Amendments A3.3 + A4): rows are processed
// IN THE ORDER SENT; each is applied (new key), replaced (higher
// revision), stale, or — per labelsRefuseRev — PERMANENTLY refused. A
// per-row refusal can never be fixed by a re-send, so the ack advances
// past it and it is reported in refusals: acked is the (ts, revision) of
// the LAST row of the batch. (labelsNullAck emulates a pre-A4 engine that
// answered acked:null when a refused row led the batch.) labelsFailStatus
// simulates a whole-batch refusal (409 contract/names mismatch, 404
// unknown kind — both with the refusal envelope) or an I/O error (503).
// Every request is recorded (labelPosts) for call-count proofs.
func (s *stubSidecar) handleLabels(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimPrefix(r.URL.Path, "/v1/labels/")
	var req LabelPushRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	s.mu.Lock()
	s.labelPosts = append(s.labelPosts, req)
	fail := s.labelsFailStatus
	limit := s.labelsAckLimit
	badAck := s.labelsBogusAck
	refuse := s.labelsRefuseRev
	if fail != 0 {
		s.mu.Unlock()
		switch fail {
		case http.StatusConflict:
			writeRefusal(w, fail, kind, "contract_mismatch", "features do not match contract")
		case http.StatusNotFound:
			writeRefusal(w, fail, kind, "unknown_kind", "unknown kind "+kind)
		case http.StatusServiceUnavailable:
			writeRefusal(w, fail, kind, "io_error", "label log I/O failure")
		default:
			w.WriteHeader(fail)
		}
		return
	}
	if names, has := s.labelsContract[kind]; has {
		// The engine's batch-level contract check (label_log
		// _contract_refusal): an unexpected feature name — or, for a
		// features_complete row, a missing one — refuses the WHOLE batch
		// 409 names_mismatch, nothing written.
		known := map[string]bool{}
		for _, n := range names {
			known[n] = true
		}
		for _, row := range req.Rows {
			var feats map[string]any
			_ = json.Unmarshal(row.Features, &feats)
			bad := false
			for n := range feats {
				if !known[n] {
					bad = true
				}
			}
			if row.FeaturesComplete {
				for _, n := range names {
					if _, ok := feats[n]; !ok {
						bad = true
					}
				}
			}
			if bad {
				s.mu.Unlock()
				writeRefusal(w, http.StatusConflict, kind, "names_mismatch", "features do not match contract")
				return
			}
		}
	}
	if s.labelMirror == nil {
		s.labelMirror = map[string]storedLabel{}
	}
	resp := labelWireResponse{Refusals: []LabelRowRefusal{}}
	rows := req.Rows
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	nullAck := s.labelsNullAck
	for i, row := range rows {
		if reason, bad := refuse[row.Revision]; bad {
			resp.Refused++
			resp.Refusals = append(resp.Refusals, LabelRowRefusal{Index: i, FeaturesHash: row.FeaturesHash, TS: row.TS, Revision: row.Revision, Reason: reason})
			if nullAck && resp.Acked == nil {
				break
			}
			resp.Acked = &LabelAck{TS: row.TS, Revision: row.Revision}
			continue
		}
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
		resp.Acked = &LabelAck{TS: row.TS, Revision: row.Revision}
	}
	if badAck != nil {
		a := *badAck
		resp.Acked = &a
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// setLabelsContract installs kind's contract feature names; pushes are
// then checked like the engine's batch-level names check.
func (s *stubSidecar) setLabelsContract(kind string, names []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.labelsContract == nil {
		s.labelsContract = map[string][]string{}
	}
	s.labelsContract[kind] = names
}

// setLabelsNullAck makes the stub behave like a pre-A4 engine: a batch
// whose first row is refused answers acked:null.
func (s *stubSidecar) setLabelsNullAck(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.labelsNullAck = on
}

// setLabelsRefuseRevision makes the stub PERMANENTLY refuse the row with
// revision rev (a per-row refusal, e.g. "features_invalid") on every push.
func (s *stubSidecar) setLabelsRefuseRevision(rev int64, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.labelsRefuseRev == nil {
		s.labelsRefuseRev = map[int64]string{}
	}
	if reason == "" {
		delete(s.labelsRefuseRev, rev)
		return
	}
	s.labelsRefuseRev[rev] = reason
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
