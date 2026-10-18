package rpc

// chat-turn-integrity-01PMZ606 WP13 (UNIT-8, "no comment asserts an
// invariant nothing enforces") — tests pinning three of the findings
// this package's api.go wiring changed:
//
//   - CHAT-10: buildAutoTitleDeps (extracted from buildChatRunner) now
//     sets AutoTitleDeps.Audit — previously always left unset, so the
//     auto-title trigger's two failure-path diagnostics had no emitter.
//   - CHAT-11: chat_runner.go's AutonomyKnobs field carried a stale
//     "KNOWN GAP" comment claiming no production call site exists, and
//     — the part that actually mattered — that a global-only resolution
//     would be the only wireable shape without a signature change. Both
//     were false: computeAutonomyKnobs (extracted from newLLMStack's
//     autonomyKnobsProvider closure so it is unit-testable here) reads
//     all three layers (global/project/session) against a live
//     core.Core today.
//   - AN-12: corpus.Manager.SetEmbedder had zero callers anywhere,
//     including tests, so a provider selected after boot never reached
//     the live corpus.Manager. RefreshEmbedder + settings.API's
//     SetEmbedderNotifier close that gap.
//   - CK-09: SweepScheduler.SeedLastRun had zero callers anywhere,
//     including tests, so lastRun started zero on every boot and the
//     documented "once per day" sweep actually fired on every launch;
//     Stop() was never invoked either, leaking the scheduler's
//     goroutine + ticker past shutdown. sweepLastRunSidecar (load/save)
//     + the a.compactionScheduler field close both gaps.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	corecorpus "github.com/kameas-ai/kenaz-harness/core/corpus"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/session"
	autotitlepkg "github.com/kameas-ai/kenaz-harness/core/sessions/autotitle"
)

// fakeAutoTitleGenerator satisfies chat.AutoTitleGenerator and always
// fails — this test only needs buildAutoTitleDeps to WIRE Audit, not to
// drive a full successful generation.
type fakeAutoTitleGenerator struct{}

func (fakeAutoTitleGenerator) GenerateTitle(_ context.Context, _ autotitlepkg.Transcript) (string, error) {
	return "", errors.New("fakeAutoTitleGenerator: always fails")
}

// fakeContextAuditEmitter records every Emit call for inspection.
type fakeContextAuditEmitter struct {
	events []contextaudit.Event
}

func (f *fakeContextAuditEmitter) Emit(_ context.Context, e contextaudit.Event) error {
	f.events = append(f.events, e)
	return nil
}

// TestBuildAutoTitleDeps_WiresAudit pins CHAT-10: buildAutoTitleDeps
// must set Audit to a non-nil emitter that forwards onto the
// contextaudit.Emitter it was given, carrying the kind + payload
// through unchanged (module the JSON round trip contextaudit.Event
// requires).
//
// Mutation: drop the `Audit: autoTitleAuditEmitter{...}` line from
// buildAutoTitleDeps's returned literal. Must fail — deps.Audit stays
// nil and autotitle.go's `if deps.Audit != nil` guards skip every Emit
// call, so fake.events stays empty.
func TestBuildAutoTitleDeps_WiresAudit(t *testing.T) {
	t.Parallel()

	sessionMgr := session.NewManager(session.NewMemoryStore())
	fake := &fakeContextAuditEmitter{}

	deps := buildAutoTitleDeps(sessionMgr, fakeAutoTitleGenerator{}, nil, nil, fake)
	if deps == nil {
		t.Fatal("buildAutoTitleDeps returned nil with non-nil Manager + Generator")
	}
	if deps.Audit == nil {
		t.Fatal("deps.Audit is nil — CHAT-10 finding still open: the two " +
			"autotitle.go failure-path diagnostics have no emitter")
	}

	ctx := context.Background()
	deps.Audit.Emit(ctx, "session.auto_titled", map[string]any{
		"session_id": "s1",
		"error_kind": "list_messages_failed",
	})

	if len(fake.events) != 1 {
		t.Fatalf("fake.events = %d entries, want 1 — Audit.Emit did not "+
			"reach the underlying contextaudit.Emitter", len(fake.events))
	}
	got := fake.events[0]
	if string(got.Kind) != "session.auto_titled" {
		t.Errorf("Kind = %q, want %q", got.Kind, "session.auto_titled")
	}
	var payload map[string]any
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("payload did not round-trip through JSON: %v", err)
	}
	if payload["error_kind"] != "list_messages_failed" {
		t.Errorf("payload[error_kind] = %v, want %q", payload["error_kind"], "list_messages_failed")
	}
}

// TestBuildAutoTitleDeps_NilDeps guards the existing nil-dependency
// short-circuit (sessionMgr or autoTitleGen missing) that this WP's
// refactor of buildChatRunner's inline block must preserve exactly.
func TestBuildAutoTitleDeps_NilDeps(t *testing.T) {
	t.Parallel()
	if got := buildAutoTitleDeps(nil, fakeAutoTitleGenerator{}, nil, nil, nil); got != nil {
		t.Errorf("buildAutoTitleDeps(nil sessionMgr) = %+v, want nil", got)
	}
	sessionMgr := session.NewManager(session.NewMemoryStore())
	if got := buildAutoTitleDeps(sessionMgr, nil, nil, nil, nil); got != nil {
		t.Errorf("buildAutoTitleDeps(nil autoTitleGen) = %+v, want nil", got)
	}
}

// TestComputeAutonomyKnobs_SessionScoped_NotGlobalOnly pins CHAT-11's
// corrected claim: computeAutonomyKnobs resolves per-project autonomy
// overrides against a live core.Core, not only the global Settings
// layer.
//
// Mutation: replace the `project` argument passed to
// resolveAutonomyKnobsWithSettingsFallback inside computeAutonomyKnobs
// with a zero autonomy.Layer{} (i.e. revert to global-only resolution,
// the shape chat_runner.go's old "KNOWN GAP" comment said was the only
// wireable one). MaxIterations must then fall back to the legacy
// Settings default and this test must fail.
func TestComputeAutonomyKnobs_SessionScoped_NotGlobalOnly(t *testing.T) {
	// NOTE: t.Parallel() omitted — sandboxUserConfigDir uses t.Setenv,
	// which is incompatible with parallel subtests.
	sandboxUserConfigDir(t)

	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c, WithSettingsStore(newTestStore(t)))
	t.Cleanup(api.Shutdown)

	ctx := context.Background()

	proj, err := c.ProjectManager().Create(ctx, "wp13-chat11-proj", "")
	if err != nil {
		t.Fatalf("project create: %v", err)
	}
	if err := c.ProjectManager().SetAutonomyProfile(ctx, proj.ID, autonomy.Layer{
		Overrides: map[autonomy.Knob]any{autonomy.KnobMaxIterations: 77},
	}); err != nil {
		t.Fatalf("SetAutonomyProfile (project): %v", err)
	}

	rec, err := c.SessionManager().CreateInProject(ctx, "wp13-chat11-sess", &proj.ID)
	if err != nil {
		t.Fatalf("session create: %v", err)
	}

	got := computeAutonomyKnobs(ctx, rec.ID, api.core, api.settingsImpl)
	if got.MaxIterations != 77 {
		t.Fatalf("MaxIterations = %d, want 77 (the project-layer override) — "+
			"computeAutonomyKnobs must resolve per-project autonomy against "+
			"the live session's project membership, not just the global "+
			"Settings layer (the corrected chat_runner.go doc comment's claim)",
			got.MaxIterations)
	}
	if got.SourceTrace[autonomy.KnobMaxIterations] != autonomy.SourceProject {
		t.Errorf("SourceTrace[MaxIterations] = %v, want SourceProject",
			got.SourceTrace[autonomy.KnobMaxIterations])
	}

	// Guard against vacuity: an empty sessionID must NOT pick up the
	// project override (proves the test is actually exercising session
	// lookup, not some other path that ignores sessionID entirely).
	baseline := computeAutonomyKnobs(ctx, "", api.core, api.settingsImpl)
	if baseline.MaxIterations == 77 {
		t.Fatalf("computeAutonomyKnobs(sessionID=\"\") also returned 77 — " +
			"the project override is leaking in independent of sessionID, " +
			"which would make the session-scoping assertion above vacuous")
	}
}

// wp13CorpusTestAPI boots a real Core + rpc.API over a temp DataDir,
// with the settings store sandboxed the same way searchWiringAPI does.
func wp13CorpusTestAPI(t *testing.T) *API {
	t.Helper()
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c, WithSettingsStore(newTestStore(t)))
	t.Cleanup(api.Shutdown)
	return api
}

// TestRefreshEmbedder_SetEmbedderConfig_WiresLiveCorpusManager pins
// AN-12 end-to-end: Settings_SetEmbedderConfig (the RPC the embedder-
// config panel calls) must reach the live corpus.Manager's embedder
// without a restart.
//
// The realistic bug shape: at boot there is no eligible provider
// profile, so corpus.Manager's embedder is nil (SetEmbedder never
// called — its own doc says "the chassis calls this once the LLM stack
// reports a working embedder"; nothing did). The user then adds a
// profile (api.personalStore.Add, mirroring the LLM Connector settings
// write) and selects it via SetEmbedderConfig. Before this WP,
// SetEmbedder had zero callers anywhere in the tree (including tests),
// so the corpus.Manager stayed frozen on the boot-time nil embedder for
// the rest of the process lifetime.
//
// IngestPath's embedder-nil check is synchronous (it runs before the
// goroutine that would actually call Embed against the network), so
// this test never makes a real network call — the path handed to
// IngestPath does not need to exist.
//
// Mutation: remove the `settingsImpl.SetEmbedderNotifier(a.RefreshEmbedder)`
// wiring in New(). Must fail — post-config IngestPath keeps returning
// ErrEmbedderUnavailable.
func TestRefreshEmbedder_SetEmbedderConfig_WiresLiveCorpusManager(t *testing.T) {
	t.Setenv("WP13_AN12_TEST_CRED", "test-value")
	api := wp13CorpusTestAPI(t)

	if api.corpusMgr == nil {
		t.Fatal("api.corpusMgr is nil — test is vacuous (needs a real DataDir)")
	}
	if api.personalStore == nil {
		t.Fatal("api.personalStore is nil — test is vacuous (needs a real DataDir)")
	}

	ctx := context.Background()
	corp, err := api.corpusMgr.CreateCorpus(ctx, "wp13-an12", corecorpus.ScopeGlobal, "", "")
	if err != nil {
		t.Fatalf("CreateCorpus: %v", err)
	}

	// Pre-condition: no eligible profile exists yet, so the boot-time
	// embedder is Noop/nil and IngestPath refuses synchronously.
	if _, err := api.corpusMgr.IngestPath(ctx, corp.ID, "/wp13-an12-nonexistent", corecorpus.IngestOptions{Recursive: true}); !errors.Is(err, corecorpus.ErrEmbedderUnavailable) {
		t.Fatalf("pre-config IngestPath err = %v, want ErrEmbedderUnavailable "+
			"(test is vacuous unless boot starts with no eligible embedder)", err)
	}

	// The user adds a provider profile (mirrors the LLM Connector
	// settings write — same personal.Store instance New() wired) and
	// then selects it in the embedder-config panel.
	profile := corellm.ProviderProfile{
		ID:    "wp13-an12-profile",
		Kind:  "custom_openai_compatible",
		Model: "test-model",
		// Endpoint is never dialed by this test — IngestPath's embedder
		// check is synchronous and runs before any network call.
		Endpoint: "http://127.0.0.1:1/v1",
		Cred:     corellm.CredentialReference{Kind: "env", Locator: "WP13_AN12_TEST_CRED"},
	}
	if err := api.personalStore.Add(profile); err != nil {
		t.Fatalf("personalStore.Add: %v", err)
	}
	if err := api.Settings().SetEmbedderConfig(ctx, profile.ID, ""); err != nil {
		t.Fatalf("SetEmbedderConfig: %v", err)
	}

	// Post-condition: RefreshEmbedder must have pushed a non-nil
	// embedder onto the SAME corpus.Manager instance.
	if _, err := api.corpusMgr.IngestPath(ctx, corp.ID, "/wp13-an12-nonexistent", corecorpus.IngestOptions{Recursive: true}); errors.Is(err, corecorpus.ErrEmbedderUnavailable) {
		t.Fatalf("post-config IngestPath err = ErrEmbedderUnavailable — " +
			"SetEmbedderConfig did not reach the live corpus.Manager " +
			"(SetEmbedderNotifier wiring missing or RefreshEmbedder no-op)")
	}
}

// TestRefreshEmbedder_NilSafe guards the boundary conditions
// RefreshEmbedder's own nil-checks claim to handle, so a future edit
// that drops one of them fails loudly instead of panicking in
// production on a degraded (nil-Core / no-DataDir) boot.
func TestRefreshEmbedder_NilSafe(t *testing.T) {
	t.Parallel()
	var nilAPI *API
	nilAPI.RefreshEmbedder() // must not panic

	api := New(nil, WithSettingsStore(newTestStore(t)))
	t.Cleanup(api.Shutdown)
	if api.corpusMgr != nil {
		t.Fatal("expected nil corpusMgr for a nil-Core chassis — test assumption changed")
	}
	api.RefreshEmbedder() // must not panic even though corpusMgr is nil
}

// TestSweepLastRunSidecar_RoundTrip pins the persistence half of CK-09:
// a saved timestamp reads back exactly, and a missing file degrades to
// the zero time (SweepScheduler.Start's own catch-up logic already
// treats zero as "overdue", which is the correct first-ever-launch
// behavior).
func TestSweepLastRunSidecar_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if got := loadSweepLastRunSidecar(dir); !got.IsZero() {
		t.Fatalf("loadSweepLastRunSidecar(no file) = %v, want zero time", got)
	}

	want := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	saveSweepLastRunSidecar(dir, want)

	got := loadSweepLastRunSidecar(dir)
	if !got.Equal(want) {
		t.Fatalf("loadSweepLastRunSidecar after save = %v, want %v", got, want)
	}

	// Empty dataDir (degraded/test boot) must not panic and must
	// degrade to zero, matching the nil-DataDir chassis path.
	if got := loadSweepLastRunSidecar(""); !got.IsZero() {
		t.Errorf("loadSweepLastRunSidecar(\"\") = %v, want zero", got)
	}
	saveSweepLastRunSidecar("", want) // must not panic
}

// TestBuildCompactionWiring_SeedsSchedulerFromSidecar pins the wiring
// half of CK-09: buildCompactionWiring must call SeedLastRun with
// whatever loadSweepLastRunSidecar returns BEFORE returning the
// scheduler — the SweepScheduler package itself already proves
// (session_scheduler_test.go) that a seeded recent LastRun suppresses
// Start's catch-up sweep, so pinning that the seed reaches the
// scheduler at construction time is the missing half this WP closes.
//
// Mutation: remove the `scheduler.SeedLastRun(loadSweepLastRunSidecar(dataDir))`
// line from buildCompactionWiring. Must fail — LastRun() stays zero
// even though a sidecar file with a recent timestamp exists.
func TestBuildCompactionWiring_SeedsSchedulerFromSidecar(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()

	seeded := time.Now().UTC().Add(-1 * time.Hour)
	saveSweepLastRunSidecar(dataDir, seeded)

	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	// Force the session manager to exist (buildCompactionWiring bails
	// out early otherwise) without booting the full *API.
	if c.SessionManager() == nil {
		t.Fatal("SessionManager() is nil — test is vacuous")
	}

	settingsImpl := settings.NewAPI(newTestStore(t))
	_, scheduler, _, _ := buildCompactionWiring(c, nil, settingsImpl)
	if scheduler == nil {
		t.Fatal("buildCompactionWiring returned a nil scheduler — test is vacuous")
	}

	got := scheduler.LastRun()
	if got.IsZero() {
		t.Fatal("scheduler.LastRun() is zero even though a sidecar with a " +
			"recent timestamp was present before construction — SeedLastRun " +
			"was not called (CK-09 still open)")
	}
	if !got.Equal(seeded) {
		t.Errorf("scheduler.LastRun() = %v, want the seeded value %v", got, seeded)
	}
}
