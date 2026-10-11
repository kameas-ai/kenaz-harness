package modelcalls_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
	"github.com/kameas-ai/kenaz-harness/core/llm/openrouter"
	"github.com/kameas-ai/kenaz-harness/core/modelcalls"
)

// generationBody is a recorded-shape OpenRouter /api/v1/generation
// response. origin and the model display fields are text the puller must
// never read; the SECRET markers prove it.
const generationBody = `{"data":{
  "id":"gen-stats-0001","total_cost":0.0042,"created_at":"2026-10-09T12:00:00Z",
  "model":"anthropic/claude-sonnet-4.5","origin":"https://SECRET-ORIGIN.example/chat",
  "usage":0.0042,"is_byok":false,"upstream_id":"msg_SECRET-UPSTREAM-ID","cache_discount":null,
  "app_id":null,"streamed":true,"cancelled":false,"provider_name":"Google AI Studio",
  "latency":812,"moderation_latency":null,"generation_time":1490.6,
  "finish_reason":"stop","native_finish_reason":"STOP",
  "tokens_prompt":100,"tokens_completion":20,
  "native_tokens_prompt":101,"native_tokens_completion":21,"native_tokens_reasoning":3,
  "native_tokens_cached":40,"num_media_prompt":null,"num_media_completion":null,
  "num_search_results":null,"external_user":"SECRET-EXTERNAL-USER"}}`

// fakeSource stands in for the LLM registry: it performs the REAL
// OpenRouter stats GET (openrouter.FetchGenerationStats) against the test
// server with a fixed test key, exactly as registry.FetchProviderStats
// does after resolving the profile's credential.
type fakeSource struct {
	endpoint string
}

func (f *fakeSource) FetchProviderStats(ctx context.Context, _ string, genID string) (genaitrace.RawProviderStats, error) {
	return openrouter.FetchGenerationStats(ctx, http.DefaultClient, f.endpoint, []byte("sk-or-test"), genID)
}

// statsServer replies with codes[i] for the i-th request (the last code
// repeats) and records each request.
type statsServer struct {
	mu    sync.Mutex
	codes []int
	reqs  []*http.Request
	at    []time.Time
	clock func() time.Time
}

func (s *statsServer) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	i := len(s.reqs)
	s.reqs = append(s.reqs, r)
	if s.clock != nil {
		s.at = append(s.at, s.clock())
	}
	code := s.codes[len(s.codes)-1]
	if i < len(s.codes) {
		code = s.codes[i]
	}
	s.mu.Unlock()
	if code != http.StatusOK {
		w.WriteHeader(code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(generationBody))
}

func (s *statsServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

func storedCall(t *testing.T, st *modelcalls.Store, span string) genaitrace.CallRecord {
	t.Helper()
	rec := fullRecord(span, time.UnixMilli(1_700_000_000_000))
	rec.ResponseID = "gen-stats-0001"
	rec.ProfileID = "or-profile"
	if err := st.Insert(context.Background(), []genaitrace.CallRecord{rec}); err != nil {
		t.Fatal(err)
	}
	return rec
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func viewOf(t *testing.T, st *modelcalls.Store, span string) modelcalls.CallView {
	t.Helper()
	got, err := st.ListBySessionHash(context.Background(), "0123456789abcdef", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got {
		if v.SpanID == span {
			return v
		}
	}
	t.Fatalf("span %s not stored", span)
	return modelcalls.CallView{}
}

// TestStatsPuller_RetriesThenMapsAllowlistedFields: the stats are not
// there yet (404, 404), then land; the puller retries with backoff, GETs
// {base}/api/v1/generation?id=<gen id> with the profile's key, and stores
// exactly the allowlisted numeric/enum fields — no text field of the
// response reaches the row.
func TestStatsPuller_RetriesThenMapsAllowlistedFields(t *testing.T) {
	t.Parallel()
	st, db := newStore(t)
	srv := &statsServer{codes: []int{404, 404, 200}}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()
	p := modelcalls.NewStatsPuller(modelcalls.StatsPullerOptions{
		Store: st, Source: &fakeSource{endpoint: ts.URL + "/api/v1/chat/completions"},
		Backoff: []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond},
	})
	p.Start()
	defer p.Stop()

	rec := storedCall(t, st, "span-stats")
	p.Consider(rec)
	waitFor(t, "stats fetched", func() bool { return p.Counters().Fetched == 1 })

	if srv.count() != 3 {
		t.Fatalf("requests = %d, want 3 (two 404 retries, then 200)", srv.count())
	}
	r := srv.reqs[2]
	if r.URL.Path != "/api/v1/generation" || r.URL.Query().Get("id") != "gen-stats-0001" ||
		r.Header.Get("Authorization") != "Bearer sk-or-test" || r.Method != http.MethodGet {
		t.Fatalf("request: %s %s auth=%q", r.Method, r.URL, r.Header.Get("Authorization"))
	}
	v := viewOf(t, st, "span-stats")
	if v.StatsState != modelcalls.StatsFetched || *v.ProviderLatencyMS != 812 || *v.ProviderGenTimeMS != 1490 ||
		*v.ProviderCostUSD != 0.0042 || *v.NativeInputTokens != 101 || *v.NativeOutputTokens != 21 ||
		*v.NativeReasoningToks != 3 || *v.NativeCachedTokens != 40 || v.NativeFinishReason != "stop" {
		t.Fatalf("mapped stats: %+v", v)
	}
	if dump := dumpTable(t, db); strings.Contains(dump, "SECRET") || strings.Contains(dump, "claude-sonnet") {
		t.Fatalf("a text field of the stats response reached the store: %s", dump)
	}
}

// TestStatsPuller_GivesUpAfterRetriesOrRefusal: a 404 that never clears
// is retried len(Backoff) times, then dropped (gave_up); a 401 is not
// retried at all.
func TestStatsPuller_GivesUpAfterRetriesOrRefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		code int
		reqs int
	}{{"404-forever", 404, 3}, {"401", 401, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st, _ := newStore(t)
			srv := &statsServer{codes: []int{tc.code}}
			ts := httptest.NewServer(http.HandlerFunc(srv.handler))
			defer ts.Close()
			p := modelcalls.NewStatsPuller(modelcalls.StatsPullerOptions{
				Store: st, Source: &fakeSource{endpoint: ts.URL + "/api/v1"},
				Backoff: []time.Duration{5 * time.Millisecond, 5 * time.Millisecond},
			})
			p.Start()
			defer p.Stop()
			p.Consider(storedCall(t, st, "span-x"))
			waitFor(t, "gave up", func() bool { return p.Counters().GaveUp == 1 })
			if srv.count() != tc.reqs {
				t.Fatalf("requests = %d, want %d", srv.count(), tc.reqs)
			}
			if v := viewOf(t, st, "span-x"); v.StatsState != modelcalls.StatsGaveUp || v.ProviderLatencyMS != nil {
				t.Fatalf("row: %+v", v)
			}
		})
	}
}

// TestStatsPuller_RateLimited: eight eligible calls at one instant cost
// a burst of five GETs at once, then one per second (fake clock) — the
// token bucket, not the call rate, sets the request rate.
func TestStatsPuller_RateLimited(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	var mu sync.Mutex
	t0 := time.Unix(1_900_000_000, 0)
	now := t0
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	var slept atomic.Int64
	sleep := func(d time.Duration, _ <-chan struct{}) bool {
		slept.Add(int64(d))
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
		return true
	}
	srv := &statsServer{codes: []int{200}, clock: clock}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()
	p := modelcalls.NewStatsPuller(modelcalls.WithClock(modelcalls.StatsPullerOptions{
		Store: st, Source: &fakeSource{endpoint: ts.URL + "/api/v1/chat/completions"},
	}, clock, sleep))
	for i := 0; i < 8; i++ {
		rec := storedCall(t, st, "span-rl-"+string(rune('a'+i)))
		p.Consider(rec) // queued before Start: all eight arrive at t0
	}
	p.Start()
	defer p.Stop()
	waitFor(t, "eight fetches", func() bool { return p.Counters().Fetched == 8 })

	srv.mu.Lock()
	at := append([]time.Time(nil), srv.at...)
	srv.mu.Unlock()
	want := []time.Duration{0, 0, 0, 0, 0, time.Second, 2 * time.Second, 3 * time.Second}
	for i, w := range want {
		if got := at[i].Sub(t0); got != w {
			t.Fatalf("request %d at +%v, want +%v (all: %v)", i, got, w, at)
		}
	}
	if time.Duration(slept.Load()) != 3*time.Second {
		t.Fatalf("slept %v, want 3s", time.Duration(slept.Load()))
	}
}

// TestStatsPuller_SettingAndEligibility: with "Fetch OpenRouter generation
// stats" off nothing is fetched; and only a successful OpenRouter call
// with a generation id and a profile is ever eligible.
func TestStatsPuller_SettingAndEligibility(t *testing.T) {
	t.Parallel()
	st, _ := newStore(t)
	srv := &statsServer{codes: []int{200}}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()
	var enabled atomic.Bool
	p := modelcalls.NewStatsPuller(modelcalls.StatsPullerOptions{
		Store: st, Source: &fakeSource{endpoint: ts.URL + "/api/v1/chat/completions"},
		Enabled: enabled.Load,
	})
	p.Start()
	defer p.Stop()

	rec := storedCall(t, st, "span-gated")
	p.Consider(rec) // setting OFF
	notOR := rec
	notOR.Provider = "anthropic"
	failed := rec
	failed.OK = false
	noGen := rec
	noGen.ResponseID = ""
	noProf := rec
	noProf.ProfileID = ""
	enabled.Store(true)
	for _, r := range []genaitrace.CallRecord{notOR, failed, noGen, noProf} {
		p.Consider(r)
	}
	time.Sleep(50 * time.Millisecond)
	if srv.count() != 0 || p.Counters().Requests != 0 {
		t.Fatalf("an ineligible or setting-off call was fetched (%d requests)", srv.count())
	}
	p.Consider(rec) // ON + eligible
	waitFor(t, "fetch", func() bool { return p.Counters().Fetched == 1 })
}
