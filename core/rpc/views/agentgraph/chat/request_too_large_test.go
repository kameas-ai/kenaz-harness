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
		"This endpoint's maximum context length is 131072 tokens.":                                      131072,
		"maximum context length is 128,000 tokens":                                                      128000,
		"This model's maximum context length is 8192 tokens. However, you requested 9000 tokens":        8192,
		"maximum context length is 131072 tokens. However, you requested about 224798 tokens":           131072,
		"the request exceeds the available context size (n_keep: 5000 >= n_ctx: 4096)":                  4096,
		"requested 250000 tokens, limit 131072":                                                         131072,
		"prompt is too long":                                                                            0,
		"max_tokens: 64000 > 8192, which is the maximum allowed number of output tokens for this model": 0,
	}
	for msg, want := range cases {
		if got := providerWindowFromError(&corellm.ErrInvalidRequest{Status: 400, Message: msg}); got != want {
			t.Errorf("%q: got %d, want %d", msg, got, want)
		}
	}
}

func TestIsContextWindowRejection(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"This endpoint's maximum context length is 131072 tokens.":                                true,
		"prompt is too long: 210000 tokens > 200000 maximum":                                      true,
		"error code context_length_exceeded":                                                      true,
		"The input token count (1048577) exceeds the maximum number of tokens allowed (1048576).": true,
		"the request exceeds the available context size (4096 tokens), try increasing it":         true,
		// Output-limit errors mention tokens but are not window overflows.
		"max_tokens is too large: 100000. This model supports at most 16384 completion tokens": false,
		"max_tokens: 64000 > 8192, which is the maximum allowed number of output tokens":       false,
	}
	for msg, want := range cases {
		if got := isContextWindowRejection(&corellm.ErrInvalidRequest{Status: 400, Message: msg}); got != want {
			t.Errorf("%q: got %v, want %v", msg, got, want)
		}
	}
}

// A fresh session whose tool returned a huge result inside the run is a
// full conversation — the composed history (classic fidelity) omits the
// tool rows, but the model saw them, so the verdict must not blame the
// request shape.
func TestClassifyRequestTooLarge_CountsThisTurnsToolOutput(t *testing.T) {
	t.Parallel()
	r := &ChatRunner{cfg: Config{History: staticHistoryReader{msgs: []coreag.Message{{Role: "user", Content: "fetch that page"}}}}}
	overflow := &corellm.ErrInvalidRequest{Status: 400, Message: "maximum context length is 131072 tokens"}

	if v := r.classifyRequestTooLarge(context.Background(), "s", "p", "m", overflow, nil); v == nil {
		t.Fatal("empty turn: want the request-too-large verdict")
	}
	huge := strings.Repeat("page content words here ", 30000) // ~150k tokens
	if v := r.classifyRequestTooLarge(context.Background(), "s", "p", "m", overflow, []string{huge}); v != nil {
		t.Fatalf("turn with a ~150k-token tool result got verdict %+v; it is a full conversation", v)
	}
	if v := r.classifyRequestTooLarge(context.Background(), "s", "p", "m",
		&corellm.ErrInvalidRequest{Status: 400, Message: "max_tokens: 64000 > 8192"}, nil); v != nil {
		t.Fatalf("an output-limit 400 got the request-too-large verdict: %+v", v)
	}
}

// Under "moves" fidelity (the default) the composed history already
// carries this turn's tool rows; they must not be counted twice. A 20k
// tool result on a 131k window is a small conversation in both modes.
func TestClassifyRequestTooLarge_TurnRowsCountedOnceInEitherFidelity(t *testing.T) {
	t.Parallel()
	result := strings.Repeat("tool output words ", 6700) // well under a quarter of 131k
	overflow := &corellm.ErrInvalidRequest{Status: 400, Message: "maximum context length is 131072 tokens"}
	user := coreag.Message{Role: "user", Content: "fetch that page"}

	movesHistory := []coreag.Message{user, {Role: "tool", Content: result, ToolCallID: "c1"}}
	classicHistory := []coreag.Message{user}
	for name, hist := range map[string][]coreag.Message{"moves": movesHistory, "classic": classicHistory} {
		r := &ChatRunner{cfg: Config{History: staticHistoryReader{msgs: hist}}}
		v := r.classifyRequestTooLarge(context.Background(), "s", "p", "m", overflow, []string{result})
		if v == nil {
			t.Fatalf("%s fidelity: a ~20k-token turn on a 131k window got no request-too-large verdict (double count?)", name)
		}
		if once := countHistoryTokens([]coreag.Message{user, {Role: "tool", Content: result}}); v.HistoryTokens != once {
			t.Errorf("%s fidelity: HistoryTokens = %d, want %d (the tool result counted exactly once)", name, v.HistoryTokens, once)
		}
	}
}

func TestTurnJournal_TurnContentIncludesToolRows(t *testing.T) {
	t.Parallel()
	j := newTurnJournal(&recordingHistoryWriter{}, nil, "s", "span", nil)
	call := coreag.ToolCall{ID: "c1", Name: "kenaz__web_fetch"}
	j.RecordToolCall(context.Background(), call)
	j.RecordToolResult(context.Background(), call, coreag.ToolResult{Content: "BIG PAGE"})
	got := strings.Join(j.TurnContent(), "|")
	if !strings.Contains(got, "BIG PAGE") {
		t.Fatalf("TurnContent = %q, want the tool result", got)
	}
}
