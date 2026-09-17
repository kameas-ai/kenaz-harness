package rpc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// Every builtin the production wiring registers must be in fleet's compiled
// allowlist, or it is reported as external_tool. Unknown names default safe;
// this test is what makes a NEW builtin a deliberate addition.
func TestKnownBuiltinTools_CoverProductionRegistry(t *testing.T) {
	registry := toolloop.NewBuiltinRegistry()
	registerBuiltinTools(nil, registry, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	names := registry.Names()
	if len(names) == 0 {
		t.Fatal("fixture registered no builtins")
	}
	sort.Strings(names)
	for _, n := range names {
		if !corefleet.KnownBuiltinToolName(n) {
			t.Errorf("builtin %q is registered but missing from fleet's knownBuiltinTools", n)
		}
	}
}

// The adapter half of the chain: what agentgraph hands the observer (proven
// verbatim in core/agentgraph) → what reaches Fleet.
func TestFleetUsageObserver_InventedToolNameIsProjectedBeforeTheWire(t *testing.T) {
	var mu sync.Mutex
	var wire bytes.Buffer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		wire.Write(b)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	tok := enc(map[string]string{"alg": "none"}) + "." + enc(map[string]string{"sub": "u1"}) + ".s"

	consent, err := corefleet.NewTelemetryConsent(t.TempDir(), corefleet.TierReaderFunc(func() string { return "enterprise" }))
	if err != nil {
		t.Fatal(err)
	}
	if err := consent.SetLevel(corefleet.ConsentFull); err != nil {
		t.Fatal(err)
	}
	p := corefleet.NewFleetOTLPPipeline(nil)
	p.SetExportCadence(time.Hour, time.Hour)
	p.SetTelemetryOptIns(corefleet.TierOptInUpdates(corefleet.ConsentFull))
	p.SetLogLaneEnabled(true)
	api := &settings.API{}
	api.SetFleetOTLPPipeline(p, nil, nil, consent)
	ctx := context.Background()
	if err := p.Activate(ctx, srv.URL+"/otlp", nil, corefleet.IdentityAttrs{UserID: "u1", MachineID: "m1"},
		func() (string, error) { return tok, nil }, nil); err != nil {
		t.Fatal(err)
	}
	defer p.Deactivate(ctx)

	obs := newFleetUsageObserver(api)
	obs.ToolInvoked(ctx, "local-session", "kenaz__customer_secret_acme", time.Millisecond, false)
	obs.ToolInvoked(ctx, "local-session", "mcp__acme-payroll__export", time.Millisecond, true)
	obs.ToolInvoked(ctx, "local-session", "kenaz__bash", time.Millisecond, true)
	obs.TurnFailed(ctx, "local-session", "open /Users/alice/x: denied", true)
	fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p.Flush(fctx)

	mu.Lock()
	got := wire.Bytes()
	mu.Unlock()
	if !bytes.Contains(got, []byte("kenaz__bash")) || !bytes.Contains(got, []byte(corefleet.ExternalToolName)) {
		t.Fatalf("expected the known builtin and external_tool on the wire")
	}
	for _, leak := range []string{"customer_secret", "acme-payroll", "local-session", "/Users/alice", "denied"} {
		if bytes.Contains(got, []byte(leak)) {
			t.Errorf("%q reached the wire", leak)
		}
	}
}

func TestFleetUsageObserver_NilSafe(t *testing.T) {
	if newFleetUsageObserver(nil) != nil || turnUsageObserver(nil) != nil {
		t.Fatal("nil settings must yield nil observers (never a typed-nil interface)")
	}
	obs := newFleetUsageObserver(&settings.API{}) // fleet not wired
	ctx := context.Background()
	obs.ToolInvoked(ctx, "s", "t", 0, true)
	obs.TurnStarted(ctx, "s", "p")
	obs.TurnFailed(ctx, "s", "k", false)
	obs.LLMResponse(ctx, "s", 1, 1, 0)
}
