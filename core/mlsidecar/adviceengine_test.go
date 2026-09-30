package mlsidecar

// adviceengine_test.go — WP15 end-to-end proofs: advice.SidecarAdvisor
// over the REAL Client (AdviceEngine) against the stub sidecar, wrapped by
// the real label CaptureAdvisor over real sqlite — so "honest fields flow
// into captured label rows" is proven on the production composition, not
// a fake.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/advice/labels"
)

// newHangingRecommendServer serves /v1/contracts normally (every kind
// available) and hangs /v1/recommend/* until the CLIENT abandons the
// request (r.Context() done — the server-side reflection of the advisor
// budget cancelling the call) or 10s pass.
func newHangingRecommendServer(released chan<- struct{}) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/contracts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ContractsPayload{Kinds: map[string]KindContract{
			"ae_hung": {ContractVersion: 1, Backend: "classic", Available: true},
		}})
	})
	mux.HandleFunc("/v1/recommend/", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			select {
			case released <- struct{}{}:
			default:
			}
		case <-time.After(10 * time.Second):
		}
	})
	return httptest.NewServer(mux)
}

type toyFeatures struct {
	N int `json:"n"`
}

type flagProbe struct{ healthy atomic.Bool }

func (p *flagProbe) Healthy() bool    { return p.healthy.Load() }
func (p *flagProbe) Identity() string { return "kenaz-ml-sidecar@stub" }

func toyKind(id string) advice.AdviceKind {
	return advice.AdviceKind{
		ID:            id,
		PromptVersion: "v1",
		SafetyClass:   advice.SafetyReversible,
		Extract:       func(input any) (advice.Features, error) { return input, nil },
		RenderPrompt:  func(advice.Features) (string, string) { return "s", "u" },
	}
}

type adviceRig struct {
	stub     *stubSidecar
	probe    *flagProbe
	adv      *advice.SidecarAdvisor
	kind     advice.AdviceKind
	fallback atomic.Int32
}

func newAdviceRig(t *testing.T, kindID string) *adviceRig {
	t.Helper()
	r := &adviceRig{stub: newStubSidecar(), probe: &flagProbe{}, kind: toyKind(kindID)}
	t.Cleanup(r.stub.Close)
	r.probe.healthy.Store(true)
	r.stub.setContracts(ContractsPayload{Kinds: map[string]KindContract{
		kindID: {ContractVersion: 2, Backend: "classic", Available: true},
	}})
	fb := advice.NewHeuristicAdvisor()
	fb.RegisterHeuristic(kindID, func(advice.Features) (bool, int, string, error) {
		r.fallback.Add(1)
		return false, 41, "heuristic/toy-v1", nil
	})
	r.adv = advice.NewSidecarAdvisor(AdviceEngine{Client: NewClient(r.stub.URL(), nil)}, r.probe, fb)
	return r
}

func (r *adviceRig) recommend(n int) (advice.Recommendation, error) {
	return r.adv.Recommend(context.Background(), r.kind, toyFeatures{N: n}, advice.SessionContext{SessionID: "sess"})
}

func TestAdviceEngine_ServedKind_EngineFieldsCarriedVerbatim(t *testing.T) {
	r := newAdviceRig(t, "ae_served")
	yes, score := true, 80
	r.stub.setRecommend("ae_served", RecommendResponse{
		Decision: &yes, Score: &score, Confidence: 87, KindID: "ae_served", FeatureContractVersion: 2,
		Model: "kenaz-ml/laya-deadbeef", Rung: "laya", Backend: "laya", ModelIDSha8: "deadbeef",
		CheckpointProvenance: "base", Generation: 4, Unbenchmarked: true,
	})
	rec, err := r.recommend(1)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Decision || rec.Confidence != 87 || rec.Model != "kenaz-ml/laya-deadbeef" || rec.Rung != advice.ModelRung("laya") || !rec.Unbenchmarked {
		t.Errorf("got %+v", rec)
	}
	if r.fallback.Load() != 0 {
		t.Error("fallback ran though the engine served")
	}
}

func TestAdviceEngine_KindNotServedRefusal_FallsThrough(t *testing.T) {
	r := newAdviceRig(t, "ae_refused")
	r.stub.refuseRecommend("ae_refused") // contracts still advertise it: the refusal is the truth
	rec, err := r.recommend(1)
	if err != nil {
		t.Fatalf("a typed refusal surfaced as an error: %v", err)
	}
	if rec.Model != "heuristic/toy-v1" || rec.Rung != advice.RungHeuristic {
		t.Errorf("got %+v, want the local heuristic's answer", rec)
	}
	if r.stub.recommendCallCount() != 1 {
		t.Errorf("engine Recommend calls = %d, want exactly the one that was refused", r.stub.recommendCallCount())
	}
}

func TestAdviceEngine_ContractsDoNotListKind_NoRecommendTraffic(t *testing.T) {
	r := newAdviceRig(t, "ae_unlisted")
	r.stub.setContracts(ContractsPayload{Kinds: map[string]KindContract{"other": {Available: true}}})
	if _, err := r.recommend(1); err != nil {
		t.Fatal(err)
	}
	if r.stub.recommendCallCount() != 0 {
		t.Errorf("Recommend sent for a kind the contracts do not list")
	}
}

func TestAdviceEngine_GarbageBodyAndServerError_FallThrough(t *testing.T) {
	r := newAdviceRig(t, "ae_garbage")
	r.stub.setRecommendRaw("ae_garbage", []byte("<html>not json</html>"))
	rec, err := r.recommend(1)
	if err != nil || rec.Model != "heuristic/toy-v1" {
		t.Fatalf("garbage body: %+v, %v; want the fallback", rec, err)
	}
	// A valid-JSON-but-empty object is malformed too (no decision).
	r.stub.setRecommendRaw("ae_garbage", []byte(`{}`))
	rec, err = r.recommend(2)
	if err != nil || rec.Model != "heuristic/toy-v1" {
		t.Fatalf("empty object: %+v, %v; want the fallback", rec, err)
	}
}

func TestAdviceEngine_StatusErrorIsTyped(t *testing.T) {
	stub := newStubSidecar()
	defer stub.Close()
	stub.refuseRecommend("k")
	_, err := NewClient(stub.URL(), nil).Recommend(context.Background(), "k", RecommendRequest{})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusConflict || se.Code != KindNotServedCode || !errors.Is(err, ErrKindNotServed) {
		t.Fatalf("err = %#v, want a *StatusError{409, kind_not_served} satisfying ErrKindNotServed", err)
	}
	// A plain non-2xx keeps the old message shape and is NOT kind-not-served.
	stub.setLabelsFail(http.StatusInternalServerError)
	_, err = NewClient(stub.URL(), nil).PushLabels(context.Background(), "k", LabelPushRequest{})
	if err == nil || errors.Is(err, ErrKindNotServed) {
		t.Fatalf("500 err = %v", err)
	}
}

func TestAdviceEngine_HungEngine_DefaultBudgetBoundsTheRealHTTPCall(t *testing.T) {
	// The stub's /health is irrelevant here; the recommend handler of a
	// purpose-built server hangs until the client's ctx is cancelled.
	released := make(chan struct{}, 1)
	srv := newHangingRecommendServer(released)
	defer srv.Close()
	kindID := "ae_hung"
	fb := advice.NewHeuristicAdvisor()
	fb.RegisterHeuristic(kindID, func(advice.Features) (bool, int, string, error) { return false, 41, "heuristic/toy-v1", nil })
	probe := &flagProbe{}
	probe.healthy.Store(true)
	adv := advice.NewSidecarAdvisor(AdviceEngine{Client: NewClient(srv.URL, &http.Client{Timeout: time.Minute})}, probe, fb)

	done := make(chan struct{})
	var rec advice.Recommendation
	var err error
	go func() {
		rec, err = adv.Recommend(context.Background(), toyKind(kindID), toyFeatures{N: 1}, advice.SessionContext{SessionID: "s"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(4 * time.Second): // the handler hangs far longer; only the 800ms budget can release the call
		t.Fatal("the real HTTP call was not bounded by the advisor budget")
	}
	if err != nil || rec.Model != "heuristic/toy-v1" {
		t.Fatalf("got %+v, %v; want the fallback after the budget expired", rec, err)
	}
}

// TestAdviceEngine_CapturedLabelRowCarriesEngineModelAndRung is the
// flip-back measurement's dependency: the engine's Model/Rung land in the
// advice_labels row the production CaptureAdvisor writes, and a later
// engine outage's fallback rows are labeled as the heuristic they are.
func TestAdviceEngine_CapturedLabelRowCarriesEngineModelAndRung(t *testing.T) {
	h := openPushHarness(t, t.TempDir())
	defer h.close(t)
	r := newAdviceRig(t, "ae_capture")
	yes := true
	r.stub.setRecommend("ae_capture", RecommendResponse{
		Decision: &yes, Confidence: 91, KindID: "ae_capture", Model: "kenaz-ml/classic-cafef00d",
		Rung: "classic", Backend: "classic",
	})
	capture := labels.NewCaptureAdvisor(r.adv, h.store, nil)
	ctx := context.Background()
	sess := advice.SessionContext{SessionID: "sess"}

	if _, err := capture.Recommend(ctx, r.kind, toyFeatures{N: 1}, sess); err != nil {
		t.Fatal(err)
	}
	r.probe.healthy.Store(false)
	if _, err := capture.Recommend(ctx, r.kind, toyFeatures{N: 2}, sess); err != nil {
		t.Fatal(err)
	}

	rows, err := h.store.PendingSince(ctx, "ae_capture", 0, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("captured rows = %d, err %v; want 2", len(rows), err)
	}
	if rows[0].ModelID != "kenaz-ml/classic-cafef00d" || rows[0].Rung != "classic" {
		t.Errorf("engine-served row = model %q rung %q, want the engine's own", rows[0].ModelID, rows[0].Rung)
	}
	if rows[1].ModelID != "heuristic/toy-v1" || rows[1].Rung != string(advice.RungHeuristic) {
		t.Errorf("fallback-served row = model %q rung %q, want the heuristic's", rows[1].ModelID, rows[1].Rung)
	}
}
