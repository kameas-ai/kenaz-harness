package rpc

// WP02 (first-run-onboarding-01PMOB01): the load-bearing tests proving the
// chosen starter's system prompt actually reaches the model.
//
// C-002 / blind spot 1 (CLAUDE.md "Two blind spots the file-level scans
// cannot see"): a fixture built on session.NewMemoryStore() or
// attachments.NewMemoryStore() bypasses SQL encode/decode entirely and
// would pass even if the production SQL path were broken. Every test in
// this file drives real sqlite via storagesqlite.Open, session.NewSQLStore
// and attachments.NewSQLStore — never the Memory* stores.
//
// C-001: "the field is set" is not delivery. TestOnboardingPromptReaches
// GenerationRequest does not read session.Record.SystemPrompt (the legacy
// column session.Manager.SetSystemPrompt writes) — it drives an actual
// chat turn through the real production chat.ChatRunner (including the
// real chat.LLMProviderAdapter as env.LLM) and asserts on the composed
// corellm.GenerationRequest.System captured at the registry seam
// (recordingRegistry below), one layer past where
// core/agentgraph/internal/recorders.LLMProvider would capture — see the
// note above buildTestChatRunner for why that seam, not env.LLM, is the
// correct capture point for this WP.
//
// Read-side note: as of this WP, the resolved-attachments-to-system-prompt
// fold happens in core/rpc/views/agentgraph/chat/llm_provider_adapter.go's
// buildAttachmentsBlock (wired via Config.Attachments / WithAttachments).
// Before this WP, no code path folded session attachments into the
// agentgraph chat composition at all — SetSystemPrompt persisted correctly
// but nothing downstream ever read it back for a live chat turn. This test
// exercises that read path end-to-end, not just the write.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/attachments"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// ---- real-sqlite test chassis (C-002) --------------------------------------

// newSQLTestStores opens a real on-disk sqlite database in t.TempDir() and
// returns a session.Manager and attachments.Manager both backed by it — the
// same physical database production uses, never the Memory* fakes.
func newSQLTestStores(t *testing.T) (*session.Manager, *attachments.Manager) {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("storagesqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	sessMgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	attMgr := attachments.NewManager(attachments.NewSQLStore(db))
	return sessMgr, attMgr
}

// newTestSessionStarterAdapter builds the REAL onboardingSessionStarterAdapter
// (the production type this WP changed), wired against the real-sqlite
// managers above — the same wiring core/rpc/api.go performs, minus the
// *core.Core-derived fields this package's test harness cannot construct.
func newTestSessionStarterAdapter(sessMgr *session.Manager, attMgr *attachments.Manager) onboardingSessionStarterAdapter {
	sessAPI := sessions.NewManagerAPIWithAttachments(sessMgr, attMgr)
	return onboardingSessionStarterAdapter{
		sessionMgr:   sessMgr,
		systemPrompt: sessAPI,
	}
}

// ---- minimal production-shaped chat.ChatRunner harness ---------------------
//
// Every adapter type below is the REAL production type from core/rpc/api.go
// (sessionHistoryReader, chatSessionMessageReader, llmHistoryWriter,
// chatAttachmentsResolverAdapter, sessionProjectReader) — reachable directly
// because this test file lives in package rpc. The LLM seam itself is left
// as the REAL production chat.LLMProviderAdapter (chat_runner.go wires it as
// env.LLM unconditionally, including .WithAttachments(cfg.Attachments)); the
// capture point is one layer further down, at the corellm.Registry.Stream
// call LLMProviderAdapter.Generate makes once it has finished composing
// gen.System (llm_provider_adapter.go:533,670). This is the correct seam
// for AC-001's own wording — "the composed GenerationRequest's system
// position" is corellm.GenerationRequest.System, which only exists after
// LLMProviderAdapter's composeSystemPrompt call runs. Substituting env.LLM
// directly (bypassing LLMProviderAdapter) would capture the graph's static,
// pre-composition agentgraph.LLMRequest.SystemPrompt instead — the wrong
// layer for this WP, since attachment folding is what LLMProviderAdapter
// itself now does (buildAttachmentsBlock).

// recordingRegistry satisfies corellm.Registry and records every
// GenerationRequest handed to Stream — the load-bearing capture point for
// AC-001.
type recordingRegistry struct {
	mu    sync.Mutex
	calls []corellm.GenerationRequest
}

func (r *recordingRegistry) RegisterAdapter(corellm.ProviderAdapter)      {}
func (r *recordingRegistry) LoadProfiles([]corellm.ProviderProfile) error { return nil }
func (r *recordingRegistry) Evict(string) error                           { return nil }
func (r *recordingRegistry) Profile(string) (corellm.ProviderProfile, error) {
	// A resolution error here is tolerated by LLMProviderAdapter.Generate
	// (it only affects the retry-policy lookup, defaulting to package
	// defaults) — see llm_provider_adapter.go:653-655.
	return corellm.ProviderProfile{}, errors.New("recordingRegistry: no profile wired")
}
func (r *recordingRegistry) PreflightAll(context.Context) []corellm.PreflightResult { return nil }

func (r *recordingRegistry) Stream(_ context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req)
	r.mu.Unlock()
	return &fakeStream{}, nil
}

func (r *recordingRegistry) snapshot() []corellm.GenerationRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]corellm.GenerationRequest, len(r.calls))
	copy(out, r.calls)
	return out
}

// fakeStream is a minimal corellm.Stream: no deltas, one terminal
// text-only, FinishReason "stop" response. Text-only + no tool_calls means
// the agent_loop's routing condition (next == "continue") is false, so the
// loop terminates after exactly one iteration — matching
// chat_runner_integration_test.go's TestChatGraph_SingleTurnNoTool.
type fakeStream struct{}

func (f *fakeStream) Events() <-chan corellm.StreamEvent {
	ch := make(chan corellm.StreamEvent)
	close(ch)
	return ch
}
func (f *fakeStream) Cancel() error { return nil }
func (f *fakeStream) Final() (corellm.Response, error) {
	return corellm.Response{
		Content:      []corellm.ContentBlock{corellm.ContentBlockFromText("ok")},
		FinishReason: "stop",
	}, nil
}

// recordingBroker is a minimal chat.Broker recording fake used to detect
// stream completion (llm:stream-closed).
type recordingBroker struct {
	mu     sync.Mutex
	topics []string
}

func (b *recordingBroker) Emit(topic string, _ any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.topics = append(b.topics, topic)
}

func (b *recordingBroker) sawClosed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range b.topics {
		if t == "llm:stream-closed" {
			return true
		}
	}
	return false
}

// waitForStreamClosed polls the broker for llm:stream-closed up to 2s —
// StartStream drives the kernel run on a background goroutine (chat_runner.go
// StartStream returns immediately; driveRun runs async).
func waitForStreamClosed(t *testing.T, b *recordingBroker) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b.sawClosed() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not observe llm:stream-closed within 2s")
}

// buildTestChatRunner wires a *chat.ChatRunner against the real-sqlite
// sessMgr/attMgr, the real bundled chat_default.yaml graph
// (core/rpc/views/agentgraph library — embedded via go:embed, so this
// resolves identically regardless of the test's working directory), and the
// recordingRegistry in place of a network-calling registry. This mirrors
// core/rpc/api.go's production chat-runner wiring (buildChatRunner) as
// closely as a package-rpc test can, reusing the SAME unexported adapter
// types (sessionHistoryReader, chatSessionMessageReader, llmHistoryWriter,
// chatAttachmentsResolverAdapter) production uses — and, crucially, the REAL
// chat.LLMProviderAdapter as env.LLM (no EnvDefaults override), since that
// adapter is what folds attachments into the composed system prompt.
func buildTestChatRunner(t *testing.T, sessMgr *session.Manager, attMgr *attachments.Manager, reg *recordingRegistry) (*chat.ChatRunner, *recordingBroker) {
	t.Helper()

	graphMgr, err := graphview.NewManager()
	if err != nil {
		t.Fatalf("graphview.NewManager: %v", err)
	}

	historyAdapter := &sessionHistoryReader{mgr: sessMgr}
	historyReader := chatSessionMessageReader{
		inner:            historyAdapter,
		moveFidelityDial: func() bool { return false },
	}
	historyWriter := &llmHistoryWriter{inner: historyAdapter}

	reader := &sessionProjectReader{mgr: sessMgr}
	attResolver := &chatAttachmentsResolverAdapter{mgr: attMgr, reader: reader}

	broker := &recordingBroker{}

	runner, err := chat.New(chat.Config{
		Kernel:        graphMgr.Kernel(),
		Registry:      reg,
		Broker:        broker,
		History:       historyReader,
		HistoryWriter: historyWriter,
		GraphLoader: func() (coreag.Graph, error) {
			g, gerr := graphMgr.LoadGraphSpec("chat_default")
			if gerr != nil {
				return g, gerr
			}
			// Default (routing gate OFF) — matches core/rpc/api.go's
			// production GraphLoader when the routing flag is unset.
			g = coreag.GateAgenticTurnRouting(g, false)
			return g, nil
		},
		MaxTurns:    func() int { return 5 },
		Attachments: attResolver,
	})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}
	return runner, broker
}

// ---- WP02 acceptance tests --------------------------------------------------

// TestOnboardingPromptReachesGenerationRequest is AC-001 / FR-003 — the
// load-bearing test. Drives restartPhase2's production adapter
// (onboardingSessionStarterAdapter.StartOnboardingSession) against real
// sqlite, then issues a real chat turn for the returned session id through
// the production chat.ChatRunner, and asserts the starter's system-prompt
// text is present in the composed agentgraph.LLMRequest's SystemPrompt —
// captured at the LLM-provider seam. Asserting session.Record.SystemPrompt
// (the legacy column) would be C-001's explicitly-insufficient shortcut;
// this test never reads that field.
func TestOnboardingPromptReachesGenerationRequest(t *testing.T) {
	sessMgr, attMgr := newSQLTestStores(t)
	adapter := newTestSessionStarterAdapter(sessMgr, attMgr)

	starter := harnessmcp.Starter{
		ID:           "code",
		Title:        "Set me up for code work",
		SystemPrompt: "You are the harness's onboarding agent for software engineering.",
	}

	ctx := context.Background()
	sessionID, err := adapter.StartOnboardingSession(ctx, starter)
	if err != nil {
		t.Fatalf("StartOnboardingSession: %v", err)
	}
	if sessionID == "" {
		t.Fatal("StartOnboardingSession returned empty session id")
	}

	reg := &recordingRegistry{}
	runner, broker := buildTestChatRunner(t, sessMgr, attMgr, reg)

	subID, err := runner.StartStream(ctx, "test-profile", sessionID, "", "let's get started")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if subID == "" {
		t.Fatal("StartStream returned empty sub id")
	}
	waitForStreamClosed(t, broker)

	calls := reg.snapshot()
	if len(calls) == 0 {
		t.Fatal("recordingRegistry.Stream was never called — the chat turn never reached the registry seam")
	}
	got := calls[0].System
	if !strings.Contains(got, starter.SystemPrompt) {
		t.Errorf("composed GenerationRequest.System does not contain the starter's prompt.\ngot:  %q\nwant substring: %q", got, starter.SystemPrompt)
	}
}

// TestLegacyColumnOnlyDoesNotReachGenerationRequest is a standing regression
// pin for tasks.md's Mutation A ("replace the sessions-view call with
// a.sessionMgr.SetSystemPrompt(...). Must fail."), distinct from the
// manually-applied mutation drill recorded in the mission report (which
// edits onboarding_wiring.go itself and re-runs
// TestOnboardingPromptReachesGenerationRequest). This test instead
// constructs the mutated OUTCOME directly — a session whose prompt was
// written only via session.Manager.SetSystemPrompt (the legacy column) —
// and asserts the composed request does NOT see it, so a future edit that
// reintroduces the legacy-column shortcut is caught by CI on every run, not
// only when someone repeats the manual drill.
func TestLegacyColumnOnlyDoesNotReachGenerationRequest(t *testing.T) {
	sessMgr, attMgr := newSQLTestStores(t)

	starterPrompt := "You are the harness's onboarding agent for software engineering."
	ctx := context.Background()
	rec, err := sessMgr.CreateWithKind(ctx, "Set me up for code work", nil, session.SessionKindOnboarding)
	if err != nil {
		t.Fatalf("CreateWithKind: %v", err)
	}
	// The mutation under proof: write ONLY the legacy column, exactly what
	// a.sessionMgr.SetSystemPrompt(...) would do if WP02's adapter called
	// the manager directly instead of the attachments-aware sessions view.
	if err := sessMgr.SetSystemPrompt(ctx, rec.ID, starterPrompt, "system"); err != nil {
		t.Fatalf("SetSystemPrompt (legacy column): %v", err)
	}

	reg := &recordingRegistry{}
	runner, broker := buildTestChatRunner(t, sessMgr, attMgr, reg)

	_, err = runner.StartStream(ctx, "test-profile", rec.ID, "", "let's get started")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	waitForStreamClosed(t, broker)

	calls := reg.snapshot()
	if len(calls) == 0 {
		t.Fatal("recordingRegistry.Stream was never called")
	}
	// The legacy-column write must NOT reach the composed system prompt —
	// the production read path (Attachments_ListResolved via
	// buildAttachmentsBlock) never looks at session.Record.SystemPrompt.
	if strings.Contains(calls[0].System, starterPrompt) {
		t.Errorf("legacy-column-only prompt reached the composed GenerationRequest.System — "+
			"the attachments seam is supposed to be the only read path.\ngot: %q", calls[0].System)
	}
}

// TestOnboardingPromptIsResolvedAttachment is AC-002 / FR-004: with the
// attachments store present, the delivered prompt is retrievable via
// Attachments_ListResolved as a position-0 inline session-scope attachment.
// Mutation (manual, recorded in the mission report): write only the legacy
// column (core/rpc/onboarding_wiring.go reverted to call
// a.sessionMgr.SetSystemPrompt directly) — must fail, since ListResolved
// never reads that column.
func TestOnboardingPromptIsResolvedAttachment(t *testing.T) {
	sessMgr, attMgr := newSQLTestStores(t)
	adapter := newTestSessionStarterAdapter(sessMgr, attMgr)

	starter := harnessmcp.Starter{
		ID:           "code",
		Title:        "Set me up for code work",
		SystemPrompt: "You are the harness's onboarding agent.",
	}

	ctx := context.Background()
	sessionID, err := adapter.StartOnboardingSession(ctx, starter)
	if err != nil {
		t.Fatalf("StartOnboardingSession: %v", err)
	}

	reader := &sessionProjectReader{mgr: sessMgr}
	resolved, err := attMgr.ListResolved(ctx, reader, sessionID)
	if err != nil {
		t.Fatalf("ListResolved: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("ListResolved returned %d attachments, want 1: %+v", len(resolved), resolved)
	}
	att := resolved[0]
	if att.Content != starter.SystemPrompt {
		t.Errorf("attachment Content = %q, want %q", att.Content, starter.SystemPrompt)
	}
	if att.Kind != attachments.KindSystem {
		t.Errorf("attachment Kind = %q, want %q", att.Kind, attachments.KindSystem)
	}
	if att.Position != 0 {
		t.Errorf("attachment Position = %d, want 0", att.Position)
	}
	if att.ScopeKind != attachments.ScopeKindSession || att.ScopeID != sessionID {
		t.Errorf("attachment scope = (%q, %q), want (session, %q)", att.ScopeKind, att.ScopeID, sessionID)
	}
}

// TestEmptyStarterPromptWritesNothing: a starter with no SystemPrompt
// creates the session and persists no attachment. Mutation: drop the
// `starter.SystemPrompt == ""` guard in StartOnboardingSession — must fail
// (an empty-content attachment would appear).
func TestEmptyStarterPromptWritesNothing(t *testing.T) {
	sessMgr, attMgr := newSQLTestStores(t)
	adapter := newTestSessionStarterAdapter(sessMgr, attMgr)

	starter := harnessmcp.Starter{ID: "chat", Title: "Just chat", SystemPrompt: ""}

	ctx := context.Background()
	sessionID, err := adapter.StartOnboardingSession(ctx, starter)
	if err != nil {
		t.Fatalf("StartOnboardingSession: %v", err)
	}
	if sessionID == "" {
		t.Fatal("expected a session to be created even with an empty prompt")
	}

	reader := &sessionProjectReader{mgr: sessMgr}
	resolved, err := attMgr.ListResolved(ctx, reader, sessionID)
	if err != nil {
		t.Fatalf("ListResolved: %v", err)
	}
	if len(resolved) != 0 {
		t.Errorf("ListResolved returned %d attachments for an empty-prompt starter, want 0: %+v", len(resolved), resolved)
	}
}

// TestStartOnboardingSession_NilSystemPromptDeliverer verifies the adapter
// fails loudly (rather than silently dropping the prompt) when the
// chassis wired sessionMgr but not systemPrompt — a construction-site bug,
// not a runtime one, but one a nil-check turns into a clear error instead
// of a quiet no-op.
func TestStartOnboardingSession_NilSystemPromptDeliverer(t *testing.T) {
	sessMgr, _ := newSQLTestStores(t)
	adapter := onboardingSessionStarterAdapter{sessionMgr: sessMgr}

	starter := harnessmcp.Starter{ID: "code", Title: "Set me up for code work", SystemPrompt: "be helpful"}
	_, err := adapter.StartOnboardingSession(context.Background(), starter)
	if err == nil {
		t.Fatal("expected an error when systemPrompt deliverer is nil but the starter carries a prompt")
	}
}
