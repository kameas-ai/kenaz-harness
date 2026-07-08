package rpc

// contextbootstrap_integration_test.go — WP08.
//
// End-to-end lifecycle test for the context-bootstrap orchestration: the real
// engine (fake MCP pool + fake model) drives the real fleet-backed adapters
// (RecipeSource / ContextWriter / ProgressSink) against an httptest fleet
// server. Asserts the full fleet lifecycle and the privacy invariants.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/contextbootstrap"
	corecontexts "github.com/kameas-ai/kenaz-harness/core/contexts"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/zalando/go-keyring"
)

// ─── fakes ────────────────────────────────────────────────────────────────────

type fakeBootstrapPool struct {
	mu      sync.Mutex
	running map[string]bool
}

func (f *fakeBootstrapPool) Tools(context.Context) ([]contextbootstrap.MCPTool, error) {
	return nil, nil
}
func (f *fakeBootstrapPool) Call(_ context.Context, _, tool string, _ json.RawMessage) (json.RawMessage, error) {
	// Return three email-shaped items so the extraction has corroborating
	// sources. Body carries an "API key" phrase to prove quarantine + no-log.
	return json.RawMessage(`[
		{"id":"msg-1","from":"bob@example.com","body":"Project Apollo kickoff. secret sk-test-DEADBEEFDEADBEEF"},
		{"id":"msg-2","from":"carol@example.com","body":"Apollo milestone review with the team"},
		{"id":"msg-3","from":"dave@example.com","body":"Apollo launch checklist"}
	]`), nil
}
func (f *fakeBootstrapPool) IsRunning(_ context.Context, recipeID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[recipeID]
}

type fakeBootstrapModel struct{}

func (fakeBootstrapModel) Call(_ context.Context, _, _ string) (string, int, int, error) {
	return `{"nodes":[{"kind":"project","title":"Apollo","body":"Apollo is a recurring project across the team.","source_kind":"email","source_refs":["msg-1","msg-2","msg-3"],"corroborating_sources":["bob@example.com","carol@example.com","dave@example.com"],"corroborations":3}]}`, 10, 20, nil
}

// recordingAudit captures audit events race-safely.
type recordingAudit struct {
	mu     sync.Mutex
	events []contextaudit.Event
}

func (r *recordingAudit) Emit(_ context.Context, e contextaudit.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}
func (r *recordingAudit) kinds() []contextaudit.Kind {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]contextaudit.Kind, len(r.events))
	for i, e := range r.events {
		out[i] = e.Kind
	}
	return out
}

// recordedReq captures one outbound fleet request (method+path+body).
type recordedReq struct {
	method string
	path   string
	body   string
}

type fleetRecorder struct {
	mu   sync.Mutex
	reqs []recordedReq
}

func (fr *fleetRecorder) add(r recordedReq) {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	fr.reqs = append(fr.reqs, r)
}
func (fr *fleetRecorder) snapshot() []recordedReq {
	fr.mu.Lock()
	defer fr.mu.Unlock()
	return append([]recordedReq(nil), fr.reqs...)
}

// bootstrapRecipeJSON is the fleet recipe served in the integration test.
const bootstrapRecipeJSON = `{
	"version":"9.9.9",
	"connector_catalog":[
		{"id":"gmail","label":"Gmail","mcp_recipe_id":"gmail","read_only_tools":["list_messages"],"fetch_strategy":"list_recent_N:10","extraction_prompt":"Extract patterns.","max_items":10,"max_tokens":10000}
	],
	"extraction_prompts":{"pattern_extraction":"Extract patterns."},
	"context_taxonomy":{"node_kinds":["person","project"],"edge_kinds":[]},
	"confidence_rules":{"assert_threshold":0.8,"tentative_threshold":0.4,"trusted_person_weight":3,"min_corroborations":3}
}`

// newIntegrationFleet builds an httptest server + a capability-enabled
// BootstrapClient + fleet.Client pointed at it, recording every request.
func newIntegrationFleet(t *testing.T, rec *fleetRecorder) (*corefleet.BootstrapClient, *corefleet.Client, *httptest.Server) {
	t.Helper()
	return newIntegrationFleetWithHook(t, rec, nil)
}

// newIntegrationFleetWithHook is like newIntegrationFleet but calls hook (if
// non-nil) after each request is recorded. The hook runs under the recorder
// mutex so it sees a consistent snapshot; it must not block.
func newIntegrationFleetWithHook(t *testing.T, rec *fleetRecorder, hook func(recordedReq)) (*corefleet.BootstrapClient, *corefleet.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rr := recordedReq{method: r.Method, path: r.URL.Path, body: string(body)}
		rec.add(rr)
		if hook != nil {
			hook(rr)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/context/bootstrap/recipe":
			_, _ = w.Write([]byte(bootstrapRecipeJSON))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/bootstrap":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(corefleet.StartBootstrapResponse{RunID: "run-int", Status: "running", RecipeVersion: "9.9.9"})
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/v1/context/bootstrap/"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/push":
			_ = json.NewEncoder(w).Encode(corefleet.ContextPushResult{AcceptedNodes: 1})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/me/onboarding":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	// Install the in-memory go-keyring mock so SaveTokens works on hosts
	// without a system keychain — the self-hosted Linux ARM64 CI runners have
	// no D-Bus / GNOME keyring ("The name is not activatable"), which made the
	// real SaveTokens fail there while passing on the macOS keychain locally.
	// Matches the per-test convention in core/rpc/keychain_test.go.
	keyring.MockInit()

	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC(),
	})
	if err := corefleet.SaveTokens(corefleet.TokenSet{
		AccessToken: "at-int", RefreshToken: "rt-int", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	client := corefleet.NewClientForTesting(srv.URL)
	caps := corefleet.NewCapabilityPoller(client, t.TempDir())
	caps.ForceSetCurrentForTesting(corefleet.Capabilities{
		Tier:      "pro",
		Enabled:   map[corefleet.Capability]bool{corefleet.CapContextBootstrap: true},
		FetchedAt: time.Now(), Source: "test",
	})
	return corefleet.NewBootstrapClient(client, caps), client, srv
}

// buildIntegrationImpl assembles a contextBootstrapImpl with the REAL
// fleet-backed adapters but a fake pool + fake model in the engine.
func buildIntegrationImpl(t *testing.T, lib *corecontexts.Library, fleetBoot *corefleet.BootstrapClient, fleetClient *corefleet.Client, audit contextaudit.Emitter) *contextBootstrapImpl {
	t.Helper()
	recipe := newBootstrapRecipeSource(fleetBoot)
	writer := newBootstrapContextWriter(lib, fleetClient, fleetBoot, audit)
	progress := newBootstrapProgressSink(nil, fleetBoot) // nil broker (no Wails in tests)
	pool := &fakeBootstrapPool{running: map[string]bool{"gmail": true}}
	model := newBootstrapModelCaller(fakeBootstrapModel{})

	eng := contextbootstrap.New(contextbootstrap.Config{
		RecipeSource: recipe,
		Pool:         pool,
		Model:        model,
		Writer:       writer,
		Progress:     progress,
	})
	return &contextBootstrapImpl{
		engine: eng, recipe: recipe, writer: writer, progress: progress,
		pool: pool, fleetBoot: fleetBoot, audit: audit,
	}
}

// ─── tests ────────────────────────────────────────────────────────────────────

// Not parallel: mutates the shared keychain singleton (SaveTokens/ClearTokens).
func TestContextBootstrap_FullLifecycle(t *testing.T) {
	rec := &fleetRecorder{}
	// contextSyncedCh is closed by the mock HTTP server when it receives
	// the PATCH /me/onboarding {context_synced:true} request, replacing
	// the fragile 150 ms sleep that was previously here.
	contextSyncedCh := make(chan struct{}, 1)
	audit := &recordingAudit{}
	fleetBoot, fleetClient, _ := newIntegrationFleetWithHook(t, rec, func(r recordedReq) {
		if r.method == http.MethodPatch && r.path == "/api/v1/me/onboarding" &&
			strings.Contains(r.body, `"context_synced":true`) {
			select {
			case contextSyncedCh <- struct{}{}:
			default:
			}
		}
	})

	lib, err := corecontexts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}
	impl := buildIntegrationImpl(t, lib, fleetBoot, fleetClient, audit)

	res, err := impl.StartRun(context.Background(), StartBootstrapRunRequest{
		ConsentedSources: []string{"gmail"},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if res.RunID != "run-int" || !res.FleetBacked {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.RecipeVersion != "9.9.9" {
		t.Errorf("recipe version = %q, want 9.9.9 (fleet recipe)", res.RecipeVersion)
	}

	// Wait for the async context_synced PATCH goroutine to fire.
	select {
	case <-contextSyncedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for context_synced PATCH /me/onboarding")
	}

	reqs := rec.snapshot()
	// Assert the full lifecycle appears in the outbound requests.
	assertHit(t, reqs, http.MethodGet, "/api/v1/context/bootstrap/recipe")
	assertHit(t, reqs, http.MethodPost, "/api/v1/context/bootstrap")
	assertHit(t, reqs, http.MethodPost, "/api/v1/context/push")
	assertHit(t, reqs, http.MethodPatch, "/api/v1/context/bootstrap/run-int")
	assertHit(t, reqs, http.MethodPatch, "/api/v1/me/onboarding")

	// A completed PATCH must be present.
	if !anyReq(reqs, func(r recordedReq) bool {
		return r.method == http.MethodPatch && r.path == "/api/v1/context/bootstrap/run-int" && strings.Contains(r.body, `"status":"completed"`)
	}) {
		t.Error("no PATCH with status=completed found")
	}
	// context_synced=true must be PATCHed.
	if !anyReq(reqs, func(r recordedReq) bool {
		return r.path == "/api/v1/me/onboarding" && strings.Contains(r.body, `"context_synced":true`)
	}) {
		t.Error("no PATCH /me/onboarding with context_synced=true")
	}

	// The /context/push body must carry source_kind + provenance + confidence.
	pushBody := ""
	for _, r := range reqs {
		if r.path == "/api/v1/context/push" {
			pushBody = r.body
		}
	}
	if pushBody == "" {
		t.Fatal("no /context/push body captured")
	}
	for _, want := range []string{`"source_kind"`, `"provenance"`, `"confidence"`, `"classification":"personal"`} {
		if !strings.Contains(pushBody, want) {
			t.Errorf("push body missing %s: %s", want, pushBody)
		}
	}

	// PRIVACY: no third-party credential bytes in ANY outbound fleet payload.
	for _, r := range reqs {
		if strings.Contains(r.body, "sk-test-DEADBEEFDEADBEEF") {
			t.Errorf("credential leaked into fleet payload %s %s", r.method, r.path)
		}
	}

	// The node must be written locally.
	tree, err := lib.Tree()
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if !treeHasBootstrapNode(tree) {
		t.Error("no bootstrap node written to the local Library")
	}

	// Audit: KindContextBootstrapRun fired at least twice (started + completed).
	runEvents := 0
	for _, k := range audit.kinds() {
		if k == contextaudit.KindContextBootstrapRun {
			runEvents++
		}
	}
	if runEvents < 2 {
		t.Errorf("KindContextBootstrapRun fired %d times, want >= 2", runEvents)
	}
}

func TestContextBootstrap_FleetDisabled_LocalOnly(t *testing.T) {
	t.Parallel()
	audit := &recordingAudit{}
	// Nil fleet client → fleet path disabled; engine still runs locally.
	fleetBoot := corefleet.NewBootstrapClient(nil, nil)

	lib, err := corecontexts.Open(t.TempDir())
	if err != nil {
		t.Fatalf("NewLibrary: %v", err)
	}
	// Local recipe source (no fleet) via the adapter's fallback.
	impl := buildIntegrationImpl(t, lib, fleetBoot, nil, audit)

	res, err := impl.StartRun(context.Background(), StartBootstrapRunRequest{
		ConsentedSources: []string{"outlook", "gmail", "slack"},
	})
	if err != nil {
		t.Fatalf("StartRun (fleet disabled): %v", err)
	}
	if res.FleetBacked {
		t.Error("FleetBacked should be false when fleet is disabled")
	}
	// Local recipe version is whatever the embedded fixture declares (non-empty).
	if res.RecipeVersion == "" {
		t.Error("expected a local recipe version")
	}

	// Health must return a zero rollup (no panic, no fleet call).
	h, err := impl.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.TotalNodes != 0 || h.NodesBySourceKind == nil {
		t.Errorf("expected zero-value health rollup, got %+v", h)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func assertHit(t *testing.T, reqs []recordedReq, method, path string) {
	t.Helper()
	if !anyReq(reqs, func(r recordedReq) bool { return r.method == method && r.path == path }) {
		t.Errorf("expected outbound %s %s, not found", method, path)
	}
}

func anyReq(reqs []recordedReq, pred func(recordedReq) bool) bool {
	for _, r := range reqs {
		if pred(r) {
			return true
		}
	}
	return false
}

func treeHasBootstrapNode(n corecontexts.Node) bool {
	if strings.HasPrefix(n.Path, "bootstrap") && n.Kind == corecontexts.KindFile {
		return true
	}
	for _, c := range n.Children {
		if treeHasBootstrapNode(c) {
			return true
		}
	}
	return false
}
