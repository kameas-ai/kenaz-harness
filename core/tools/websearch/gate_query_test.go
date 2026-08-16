package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// The query leg of a web search — the request that carries the user's
// search terms to duckduckgo.com / wikipedia.org — used to bypass the
// Cedar gate entirely. Only Fetcher (the leg that retrieves a result
// PAGE) consulted it, so a `forbid network_request` policy naming those
// hosts stopped nothing that mattered.
//
// These tests assert the observable refusal, not the presence of a
// field: with a deny policy the backend returns a policy error AND the
// upstream server receives zero requests.

const denyNetworkPolicy = `
forbid (
    principal == User::"local",
    action == Action::"network_request",
    resource
);
`

// denyNetworkGate builds a real Cedar engine whose only policy forbids
// network_request. Using the engine (rather than a stub Gate) keeps the
// test honest about the whole path: policy TEXT → PolicySet → Evaluate →
// Deny → PolicyDeniedError.
func denyNetworkGate(t *testing.T) cedar.Gate {
	t.Helper()
	eng, err := cedar.NewEngine(cedar.Options{})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	if err := eng.SetPolicyText("deny_network.cedar", []byte(denyNetworkPolicy)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	return eng
}

// defaultBundleGate is the shipped posture: the embedded bundle, which
// permits network_request. It guards against the opposite regression —
// wiring a real engine where AllowAll stood must not start denying
// traffic users rely on.
func defaultBundleGate(t *testing.T) cedar.Gate {
	t.Helper()
	eng, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	return eng
}

// countingServer returns a server plus a counter of requests it saw.
func countingServer(t *testing.T, body string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestDuckDuckGoBackend_DenyPolicyBlocksTheQueryRequest(t *testing.T) {
	t.Parallel()
	srv, hits := countingServer(t, "<html></html>")

	b := NewDuckDuckGoBackend(
		WithDuckDuckGoEndpoint(srv.URL),
		WithDuckDuckGoClient(srv.Client()),
		WithDuckDuckGoGate(denyNetworkGate(t)),
	)

	_, err := b.Search(context.Background(), "how to leak my search terms", SearchOpts{MaxResults: 3})
	if err == nil {
		t.Fatal("Search succeeded under a forbid network_request policy")
	}
	var denied *cedar.PolicyDeniedError
	if !asPolicyDenied(err, &denied) {
		t.Fatalf("err = %v (%T); want *cedar.PolicyDeniedError", err, err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("upstream saw %d request(s); a denied search must emit none", got)
	}
}

func TestWikipediaBackend_DenyPolicyBlocksTheQueryRequest(t *testing.T) {
	t.Parallel()
	srv, hits := countingServer(t, `["q",["T"],["D"],["https://en.wikipedia.org/wiki/T"]]`)

	b := NewWikipediaBackend(
		WithWikipediaEndpoint(srv.URL),
		WithWikipediaClient(srv.Client()),
		WithWikipediaGate(denyNetworkGate(t)),
	)

	_, err := b.Search(context.Background(), "anything", SearchOpts{MaxResults: 3})
	if err == nil {
		t.Fatal("Search succeeded under a forbid network_request policy")
	}
	var denied *cedar.PolicyDeniedError
	if !asPolicyDenied(err, &denied) {
		t.Fatalf("err = %v (%T); want *cedar.PolicyDeniedError", err, err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("upstream saw %d request(s); a denied search must emit none", got)
	}
}

// The no-over-blocking half. Wiring a real engine where AllowAll used to
// sit is the risk this change carries: the shipped bundle must still let
// a normal search through.
func TestBackends_DefaultShippedPolicyStillPermitsTheQuery(t *testing.T) {
	t.Parallel()

	t.Run("duckduckgo", func(t *testing.T) {
		t.Parallel()
		srv, hits := countingServer(t, ddgOneResultHTML)
		b := NewDuckDuckGoBackend(
			WithDuckDuckGoEndpoint(srv.URL),
			WithDuckDuckGoClient(srv.Client()),
			WithDuckDuckGoGate(defaultBundleGate(t)),
		)
		hitsOut, err := b.Search(context.Background(), "kenaz", SearchOpts{MaxResults: 3})
		if err != nil {
			t.Fatalf("Search under the shipped default bundle: %v", err)
		}
		if got := hits.Load(); got != 1 {
			t.Fatalf("upstream saw %d request(s); want 1", got)
		}
		if len(hitsOut) == 0 {
			t.Fatal("no results parsed; the gate must not swallow the response")
		}
	})

	t.Run("wikipedia", func(t *testing.T) {
		t.Parallel()
		srv, hits := countingServer(t, `["q",["Title"],["Desc"],["https://en.wikipedia.org/wiki/Title"]]`)
		b := NewWikipediaBackend(
			WithWikipediaEndpoint(srv.URL),
			WithWikipediaClient(srv.Client()),
			WithWikipediaGate(defaultBundleGate(t)),
		)
		hitsOut, err := b.Search(context.Background(), "kenaz", SearchOpts{MaxResults: 3})
		if err != nil {
			t.Fatalf("Search under the shipped default bundle: %v", err)
		}
		if got := hits.Load(); got != 1 {
			t.Fatalf("upstream saw %d request(s); want 1", got)
		}
		if len(hitsOut) == 0 {
			t.Fatal("no results parsed; the gate must not swallow the response")
		}
	})
}

// A nil gate stays ungated — the nil-core / test posture every caller
// below the chassis relies on.
func TestBackends_NilGateIsUngated(t *testing.T) {
	t.Parallel()
	srv, hits := countingServer(t, ddgOneResultHTML)
	b := NewDuckDuckGoBackend(
		WithDuckDuckGoEndpoint(srv.URL),
		WithDuckDuckGoClient(srv.Client()),
	)
	if _, err := b.Search(context.Background(), "kenaz", SearchOpts{}); err != nil {
		t.Fatalf("Search with no gate: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("upstream saw %d request(s); want 1", got)
	}
}

const ddgOneResultHTML = `<html><body>
<div class="result">
  <a class="result__a" href="https://example.com/a">Example A</a>
  <div class="result__snippet">snippet a</div>
</div>
</body></html>`

// asPolicyDenied is errors.As without importing errors into every
// assertion above.
func asPolicyDenied(err error, target **cedar.PolicyDeniedError) bool {
	for cur := err; cur != nil; {
		if pd, ok := cur.(*cedar.PolicyDeniedError); ok {
			*target = pd
			return true
		}
		u, ok := cur.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		cur = u.Unwrap()
	}
	return false
}
