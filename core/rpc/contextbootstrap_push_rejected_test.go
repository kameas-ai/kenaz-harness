package rpc

// contextbootstrap_push_rejected_test.go — review R3(b). The bootstrap
// engine's direct /context/push must not record a node fleet rejected
// per-item (kenaz-fleet PR #173 `rejected[]`) as published, nor count it
// toward onboarding's context_synced.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/contextbootstrap"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

// bootstrapPushEmitter is a race-safe audit sink.
type bootstrapPushEmitter struct {
	mu  sync.Mutex
	evs []contextaudit.Event
}

func (e *bootstrapPushEmitter) Emit(_ context.Context, ev contextaudit.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.evs = append(e.evs, ev)
	return nil
}

func (e *bootstrapPushEmitter) kinds() []contextaudit.Kind {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]contextaudit.Kind, 0, len(e.evs))
	for _, ev := range e.evs {
		out = append(out, ev.Kind)
	}
	return out
}

// bootstrapPushFleet answers /context/push with a fixed body and counts
// onboarding PATCHes (handler goroutine writes, test reads: mutex).
type bootstrapPushFleet struct {
	mu         sync.Mutex
	pushBody   string
	onboarding int
}

func (f *bootstrapPushFleet) onboardingCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.onboarding
}

func newBootstrapPushRig(t *testing.T, pushBody string) (*bootstrapContextWriter, *bootstrapPushFleet, *bootstrapPushEmitter) {
	t.Helper()
	f := &bootstrapPushFleet{pushBody: pushBody}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch r.URL.Path {
		case "/api/v1/context/push":
			f.mu.Lock()
			body := f.pushBody
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		case "/api/v1/me/onboarding":
			f.mu.Lock()
			f.onboarding++
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"schema":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL,
	})
	corefleet.SetExternalTokenSource(func() string { return "tok" })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })

	client := corefleet.NewClientForTesting(srv.URL)
	caps := corefleet.NewCapabilityPoller(nil, t.TempDir())
	caps.ForceSetCurrentForTesting(corefleet.Capabilities{
		Tier:      "team",
		Enabled:   map[corefleet.Capability]bool{corefleet.CapContextBootstrap: true},
		FetchedAt: time.Now(),
		Source:    "test",
	})
	em := &bootstrapPushEmitter{}
	w := newBootstrapContextWriter(nil, client, corefleet.NewBootstrapClient(client, caps), em)
	return w, f, em
}

func bootstrapTestNode() contextbootstrap.ExtractedNode {
	return contextbootstrap.ExtractedNode{
		Kind: "project", Title: "Apollo", Body: "b", ConnectorID: "gmail",
		SourceKind: "email", SourceRef: "msg-1", Confidence: 0.9,
	}
}

func TestBootstrapPush_RejectedNode_AuditsRejectionNotPublish(t *testing.T) {
	n := bootstrapTestNode()
	id := bootstrapNodeID(n)
	w, f, em := newBootstrapPushRig(t,
		`{"accepted_nodes":0,"accepted_edges":0,"conflicts":[],"rejected":[{"id":"`+id+`","kind":"node","reason":"not_permitted"}]}`)

	if ok := w.pushNodeToFleet(context.Background(), n); ok {
		t.Fatal("pushNodeToFleet = true for a node fleet rejected per-item")
	}
	kinds := em.kinds()
	if len(kinds) != 1 || kinds[0] != contextaudit.KindFleetContextPushRejected {
		t.Fatalf("audit kinds = %v, want exactly [%s] (never %s)", kinds,
			contextaudit.KindFleetContextPushRejected, contextaudit.KindFleetContextPublished)
	}
	time.Sleep(100 * time.Millisecond) // the onboarding PATCH is async when it fires
	if got := f.onboardingCalls(); got != 0 {
		t.Fatalf("onboarding context_synced PATCHed %d times for a rejected node, want 0", got)
	}
}

func TestBootstrapPush_Accepted_StillPublishesAndSignalsOnboarding(t *testing.T) {
	w, f, em := newBootstrapPushRig(t, `{"accepted_nodes":1,"accepted_edges":0,"conflicts":[]}`)
	if ok := w.pushNodeToFleet(context.Background(), bootstrapTestNode()); !ok {
		t.Fatal("pushNodeToFleet = false for an accepted push")
	}
	kinds := em.kinds()
	if len(kinds) != 1 || kinds[0] != contextaudit.KindFleetContextPublished {
		t.Fatalf("audit kinds = %v, want [%s]", kinds, contextaudit.KindFleetContextPublished)
	}
	deadline := time.Now().Add(2 * time.Second)
	for f.onboardingCalls() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := f.onboardingCalls(); got != 1 {
		t.Fatalf("onboarding PATCH count = %d, want 1", got)
	}
}
