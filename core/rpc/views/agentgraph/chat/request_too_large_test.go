package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// conversationFillingWindow is a persisted history large relative to
// overflowError()'s 8192-token window — a conversation that genuinely
// outgrew the model, for which compaction (and ErrSessionFull) is the
// right answer.
func conversationFillingWindow() []coreag.Message {
	long := strings.Repeat("the conversation keeps going with more words ", 800)
	return []coreag.Message{
		{Role: "user", Content: long},
		{Role: "assistant", Content: long},
		{Role: "user", Content: "and one more"},
	}
}

// alwaysOverflowLLM rejects every request for its size.
type alwaysOverflowLLM struct{}

func (alwaysOverflowLLM) Generate(context.Context, coreag.LLMRequest) (coreag.LLMResponse, error) {
	return coreag.LLMResponse{}, &corellm.ErrInvalidRequest{
		Status:  400,
		Message: "This endpoint's maximum context length is 131072 tokens. However, you requested about 224798 tokens",
	}
}

func runToClose(t *testing.T, history []coreag.Message) (StreamClosedPayload, *fakeCompactionEngine) {
	t.Helper()
	broker := &recordingBroker{}
	engine := &fakeCompactionEngine{}
	graph := overflowModelGraph()
	runner, err := New(Config{
		Kernel:                coreag.NewKernel(),
		Registry:              stubRegistry{},
		Broker:                broker,
		HistoryWriter:         &recordingHistoryWriter{},
		History:               staticHistoryReader{msgs: history},
		GraphLoader:           func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:              func() int { return 25 },
		Compaction:            &CompactionDeps{Engine: engine},
		MaxOverflowRecoveries: func() int { return 1 },
		EnvDefaults:           func(env *coreag.Env) { env.LLM = alwaysOverflowLLM{} },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "aion-labs/aion-2.0", testTurn("ping")); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range broker.snapshot() {
			if closed, ok := e.payload.(StreamClosedPayload); ok {
				return closed, engine
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no stream-closed payload; events = %+v", broker.snapshot())
	return StreamClosedPayload{}, nil
}

// dogfood 2026-10-08 round 2: a brand-new session (one short user
// message) rejected for context length is the REQUEST's fault (tool
// schemas + system prompt), not the conversation's — the close must say
// so, name the model and window, and not attempt compaction.
func TestChatRunner_OverflowOnEmptyHistory_IsRequestTooLarge(t *testing.T) {
	t.Parallel()
	closed, engine := runToClose(t, []coreag.Message{{Role: "user", Content: "ping"}})

	if closed.ErrorKind != StreamClosedErrorKindRequestTooLarge {
		t.Fatalf("ErrorKind = %q, want %q (message %q)", closed.ErrorKind, StreamClosedErrorKindRequestTooLarge, closed.Message)
	}
	if closed.Message == compaction.ErrSessionFull.Error() {
		t.Fatalf("message is the session-full copy; the conversation is empty")
	}
	for _, want := range []string{"Request too large for aion-labs/aion-2.0", "131072-token window", "disable tools"} {
		if !strings.Contains(closed.Message, want) {
			t.Errorf("message %q missing %q", closed.Message, want)
		}
	}
	if closed.FailureCode != StreamClosedErrorKindRequestTooLarge {
		t.Errorf("FailureCode = %q, want %q", closed.FailureCode, StreamClosedErrorKindRequestTooLarge)
	}
	if got := engine.compactCount(); got != 0 {
		t.Errorf("Compact calls = %d, want 0 — there is nothing to compact", got)
	}
}

// The genuinely-full conversation keeps ErrSessionFull after the
// recovery budget is spent.
func TestChatRunner_OverflowOnFullHistory_KeepsSessionFull(t *testing.T) {
	t.Parallel()
	// The window the provider names here is 131072; make the history a
	// large fraction of it.
	long := strings.Repeat("the conversation keeps going with more words ", 9000)
	closed, engine := runToClose(t, []coreag.Message{
		{Role: "user", Content: long}, {Role: "assistant", Content: long}, {Role: "user", Content: "more"},
	})
	if closed.ErrorKind != StreamClosedErrorKindSessionFull {
		t.Fatalf("ErrorKind = %q, want %q (message %q)", closed.ErrorKind, StreamClosedErrorKindSessionFull, closed.Message)
	}
	if got := engine.compactCount(); got == 0 {
		t.Errorf("Compact calls = 0; a full conversation must still attempt overflow recovery")
	}
}

func TestProviderWindowFromError(t *testing.T) {
	t.Parallel()
	cases := map[string]int{
		"This endpoint's maximum context length is 131072 tokens.": 131072,
		"maximum context length is 128,000 tokens":                  128000,
		"prompt is too long":                                         0,
	}
	for msg, want := range cases {
		if got := providerWindowFromError(&corellm.ErrInvalidRequest{Status: 400, Message: msg}); got != want {
			t.Errorf("%q: got %d, want %d", msg, got, want)
		}
	}
}
