package chat

// post_send_hook_integration_test.go — ledger #46 ("post_send hooks never
// fire — only 1 of 18 hook events works, and memory.persist rides on it").
//
// Prior investigation (kitty-specs/trust-surfaces-that-fire-01PMZ202)
// wired RunPostSend's plumbing (core/hooks/runner.go, the
// hooksRunnerAdapter translation in core/rpc/api.go) but its own WP12
// plan seated the fire call beside `a.hooks.RunPreSend` inside
// `(a *API).buildMessages` (core/rpc/views/llm/impl.go:638) — a method
// with ZERO production callers since the agent-kernel-graph-chat-
// migration cutover (commit f0b17126, 2026-04-27), four months before
// that mission's own investigation ran. A fire call planted there would
// have compiled, passed a direct-invocation unit test, and still never
// run in a shipped build — exactly the "vacuous test" shape CLAUDE.md's
// unwired-sweep doctrine warns about.
//
// These tests drive the REAL send path instead: API.StartStream calls
// ChatRunner.StartStream (this package), which is what a real Wails
// binding reaches. PostSendHook is registered on the same HookPostLLM
// boundary UsageHook already uses (chat_runner.go), fired from
// exec_state.go's sessionWriteExecutor after the assistant message is
// actually persisted — never from dead code.
//
// TestChatRunner_PostSendHook_FiresOnRealPath locks the wiring: a real
// StartStream call reaches a registered PostSendHook exactly once, with
// the real user/assistant turn text and finish reason.
//
// TestPostSendHook_MemoryPersist_WritesRealRow drives the full chain one
// level further: a REAL hooks.Runner + REAL hooks.Registry with a REAL
// saved post_send hook (kind=builtin, builtin=memory.persist) against a
// REAL corememory.NewChromemStore-backed file (the on-disk gob store
// production uses — core/memory has no sqlite backend, so this is that
// subsystem's equivalent of CLAUDE.md blind spot #2's "must drive real
// sqlite" rule: a real on-disk store round-tripped through a FRESH store
// instance, not the in-memory fakeStore core/hooks/memory_builtins_test.go
// uses for its own unit-level tests). Asserts an actual chunk was written
// to disk — not a "fired" flag.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/hooks"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	corememory "github.com/kameas-ai/kenaz-harness/core/memory"
)

// postSendStubRegistry is a corellm.Registry that returns a real
// Kind/Model on Profile() (unlike stubRegistry, which errors) so
// ProviderKind()/ActiveModelID() resolve to non-empty values the test
// can assert against, and hands out one scriptedTurn per Stream call
// (mirrors scriptedRegistry in moves_test.go, duplicated locally to
// avoid coupling this file to stubRegistry's erroring Profile()).
type postSendStubRegistry struct {
	mu    sync.Mutex
	turns []scriptedTurn
}

func (r *postSendStubRegistry) RegisterAdapter(_ corellm.ProviderAdapter)      {}
func (r *postSendStubRegistry) LoadProfiles(_ []corellm.ProviderProfile) error { return nil }
func (r *postSendStubRegistry) Evict(_ string) error                           { return nil }
func (r *postSendStubRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{ID: "profile-1", Kind: "anthropic", Model: "claude-test-model"}, nil
}
func (r *postSendStubRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult { return nil }
func (r *postSendStubRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.turns) == 0 {
		return nil, fmt.Errorf("postSendStubRegistry: out of turns")
	}
	t := r.turns[0]
	r.turns = r.turns[1:]
	ch := make(chan corellm.StreamEvent, len(t.deltas))
	for _, d := range t.deltas {
		ch <- d
	}
	close(ch)
	return &scriptedStream{events: ch, resp: t.resp}, nil
}

// postSendCall is one recorded PostSendHookFunc invocation.
type postSendCall struct {
	sessionID, userTurn, assistantTurn, providerKind, modelID, finishReason string
}

// postSendRecorder is a race-safe fake: PostSendHook fires from the
// kernel's execution goroutine (HookManager.FirePostHooks's doc comment
// is explicit about this — "callbacks are run synchronously on the
// kernel's execution goroutine"), and the test body reads back through
// snapshot() after waitForClosed, from a different goroutine. CLAUDE.md's
// race-discipline pattern: mutex + snapshot helper, all test-side reads
// through snapshot().
type postSendRecorder struct {
	mu    sync.Mutex
	calls []postSendCall
}

func (r *postSendRecorder) hook() PostSendHookFunc {
	return func(_ context.Context, sessionID, userTurn, assistantTurn, providerKind, modelID, finishReason string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.calls = append(r.calls, postSendCall{sessionID, userTurn, assistantTurn, providerKind, modelID, finishReason})
	}
}

func (r *postSendRecorder) snapshot() []postSendCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]postSendCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// TestChatRunner_PostSendHook_FiresOnRealPath drives a real, single-turn
// StartStream call (the production send path, not buildMessages) and
// asserts a registered PostSendHook fires exactly once with the real
// session id, the real user/assistant turn text, and the real finish
// reason — not a synthetic payload constructed and handed to Fire
// directly (CLAUDE.md testing rule 3: that shape "proves nothing... that
// is exactly what already passes today for post_send", per the mission's
// own WP12 note).
func TestChatRunner_PostSendHook_FiresOnRealPath(t *testing.T) {
	t.Parallel()

	const userMsg = "what is the weather like in nyc today"
	const assistantMsg = "it is sunny and 72 degrees in nyc today, with light winds"

	reg := &postSendStubRegistry{turns: []scriptedTurn{
		textTurnWithUsage(assistantMsg, 10, 20, 0),
	}}
	rec := &postSendRecorder{}
	broker := &recordingBroker{}
	graph := loadProductionChatGraph(t)

	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      reg,
		Broker:        broker,
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		PostSendHook:  rec.hook(),
		// Wiring PostSendHook makes chat_runner.go construct a real
		// coreag.HookManager (same as UsageHook — see moves_usage_test.go's
		// noopMemoryStore comment): the ask node's HookManager.Fire call
		// writes to env.Memory unconditionally and panics on a nil
		// MemoryStore interface. Production always wires a real store
		// before this point; tests need this harmless stand-in.
		EnvDefaults: func(env *coreag.Env) { env.Memory = noopMemoryStore{} },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-post-send", "", userMsg); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Fatalf("Reason = %q, want non-error; msg=%q", closed.Reason, closed.Message)
	}

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("PostSendHook fired %d times, want 1; calls=%+v", len(calls), calls)
	}
	c := calls[0]
	if c.sessionID != "session-post-send" {
		t.Errorf("sessionID = %q, want %q", c.sessionID, "session-post-send")
	}
	if c.userTurn != userMsg {
		t.Errorf("userTurn = %q, want %q", c.userTurn, userMsg)
	}
	if c.assistantTurn != assistantMsg {
		t.Errorf("assistantTurn = %q, want %q", c.assistantTurn, assistantMsg)
	}
	if c.finishReason != "stop" {
		t.Errorf("finishReason = %q, want %q", c.finishReason, "stop")
	}
	if c.providerKind != "anthropic" {
		t.Errorf("providerKind = %q, want %q", c.providerKind, "anthropic")
	}
	if c.modelID != "claude-test-model" {
		t.Errorf("modelID = %q, want %q", c.modelID, "claude-test-model")
	}
}

// TestPostSendHook_MemoryPersist_WritesRealRow drives the whole chain:
// real StartStream -> real PostSendHook -> real hooks.Runner ->
// hooks.RunPostSend -> the memory.persist builtin -> a REAL
// corememory.NewChromemStore file on disk. The assertion re-opens a FRESH
// store instance against the same path (rather than reading back through
// the original in-process Store) so a pass actually proves the write
// round-tripped through gob encode/decode on disk, not just that the
// in-memory slice the hook wrote to still holds the value.
func TestPostSendHook_MemoryPersist_WritesRealRow(t *testing.T) {
	t.Parallel()

	// memory.persist's default thresholds (core/hooks/memory_builtins.go's
	// DefaultConfig) require >=80 user chars and >=200 assistant chars, or
	// it silently no-ops — both turns here clear that bar deliberately, so
	// a failure proves the wiring is broken, not that the builtin's own
	// content filter tripped.
	userMsg := strings.Repeat("what should I cook for dinner tonight given what is in my fridge? ", 2)
	assistantMsg := strings.Repeat(
		"try a simple stir fry: saute your vegetables in a hot pan with a little oil, "+
			"add a protein of your choice, and finish with soy sauce and garlic. ", 3)

	storePath := filepath.Join(t.TempDir(), "memory.gob")
	store, err := corememory.NewChromemStore(storePath)
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}

	builtins := hooks.NewBuiltinRegistry()
	hooks.RegisterMemoryBuiltins(builtins, hooks.MemoryDeps{
		Store:    store,
		Embedder: fakePostSendEmbedder{},
	})
	registry, err := hooks.NewRegistry("")
	if err != nil {
		t.Fatalf("hooks.NewRegistry: %v", err)
	}
	if err := registry.Add(hooks.Hook{
		ID: "h-memory-persist", Name: "memory persist", Event: hooks.EventPostSend,
		Kind: hooks.KindBuiltin, Enabled: true, Builtin: hooks.BuiltinMemoryPersist,
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	hookRunner := hooks.NewRunner(hooks.Config{Registry: registry, Builtins: builtins})

	// This closure is deliberately the SAME translation core/rpc/api.go's
	// postSendHookFn performs (llm.PostSendHookEvent's field names), not
	// an independently invented one — a divergent translation here would
	// prove the builtin works without proving the wiring this ledger item
	// is about is correct.
	postSendHook := func(ctx context.Context, sessionID, userTurn, assistantTurn, providerKind, modelID, finishReason string) {
		hookRunner.RunPostSend(ctx, hooks.PostSendEvent{
			SessionID:     sessionID,
			UserTurn:      userTurn,
			AssistantTurn: assistantTurn,
			Model:         modelID,
			Kind:          providerKind,
			FinishReason:  finishReason,
		})
	}

	reg := &postSendStubRegistry{turns: []scriptedTurn{
		textTurnWithUsage(assistantMsg, 10, 20, 0),
	}}
	broker := &recordingBroker{}
	graph := loadProductionChatGraph(t)

	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      reg,
		Broker:        broker,
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		PostSendHook:  postSendHook,
		// See the matching comment in TestChatRunner_PostSendHook_FiresOnRealPath.
		EnvDefaults: func(env *coreag.Env) { env.Memory = noopMemoryStore{} },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-memory-persist", "", userMsg); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Fatalf("Reason = %q, want non-error; msg=%q", closed.Reason, closed.Message)
	}

	// Blocker 4 (review of finding #61, 2026-09-11): RunPostSend dispatches
	// the memory.persist embed+write asynchronously through hookRunner's
	// worker pool (core/hooks/fire.go) — waitForClosed only proves the
	// chat turn itself finished, not that the detached post_send dispatch
	// it kicked off has completed. Without draining the pool here, the
	// re-open below raced the write with no happens-before guarantee;
	// the reviewer ran it 50+ times under -race -count=50
	// GOMAXPROCS=1 and it passed every time by scheduling luck alone.
	// hookRunner.Shutdown() blocks until every in-flight dispatch (this
	// one included) returns, mirroring the same drain
	// core/hooks/runner_test.go's TestRunner_BuiltinPostSendFiresOnce
	// already does before its own post-dispatch assertion.
	hookRunner.Shutdown()

	// Re-open a FRESH store instance against the same on-disk path — proves
	// the write survived a real gob encode -> file rename -> decode
	// round-trip, not just that the original in-process Store still holds
	// it in RAM.
	reopened, err := corememory.NewChromemStore(storePath)
	if err != nil {
		t.Fatalf("re-open NewChromemStore: %v", err)
	}
	chunks, err := reopened.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1 — memory.persist should have written exactly one chunk to disk; chunks=%+v", len(chunks), chunks)
	}
	ch := chunks[0]
	if ch.SessionID != "session-memory-persist" {
		t.Errorf("chunk.SessionID = %q, want %q", ch.SessionID, "session-memory-persist")
	}
	if ch.SourceTurn != "post_send" {
		t.Errorf("chunk.SourceTurn = %q, want %q", ch.SourceTurn, "post_send")
	}
	if !strings.Contains(ch.Content, "stir fry") {
		t.Errorf("chunk.Content = %q, want it to contain the real assistant turn text", ch.Content)
	}
	if !strings.Contains(ch.Content, "cook for dinner") {
		t.Errorf("chunk.Content = %q, want it to contain the real user turn text", ch.Content)
	}
}

// fakePostSendEmbedder returns a deterministic non-empty vector.
// Embedding is a numeric transform, not a persistence layer — a fake
// here does not hide the class of defect CLAUDE.md's real-sqlite rule
// guards against (only the Store side of this test is required to be
// real; see the file doc comment).
type fakePostSendEmbedder struct{}

func (fakePostSendEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.1, 0.2, 0.3}
	}
	return out, nil
}
