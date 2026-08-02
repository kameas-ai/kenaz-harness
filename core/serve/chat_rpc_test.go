package serve_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/kameas-ai/kenaz-harness/core"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	"github.com/kameas-ai/kenaz-harness/core/serve"
)

// chat_rpc_test.go covers the served-mode chat surface end to end through
// the REAL HTTP + WebSocket server: a browser-shaped client creates a
// session, appends a user turn, watches assistant tokens arrive over the
// WebSocket, and stops the stream.
//
// Scope note (deliberate, not an oversight): the token chunks are driven
// by publishing onto the API's EventBus — the exact seam the production
// chat runner writes to (StreamBroker → MultiEmitter → busEmitter → bus,
// see core/rpc/api.go where the broker is constructed). Everything
// downstream of that seam is the real code path: real bus, real
// subscription, real backpressure queue, real WS frames. Standing up a
// fake upstream provider to exercise the connector as well belongs to the
// connector's own tests, not to a test of the served transport.

// newChatHarness starts a served harness backed by a REAL core (so
// sessions and messages actually persist) in a temp data dir.
func newChatHarness(t *testing.T, opts ...serve.ServerOption) (api *rpc.API, baseURL string, cancel context.CancelFunc) {
	t.Helper()

	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api = rpc.New(c)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, ctxCancel := context.WithCancel(context.Background())
	srv := serve.New(api, addr, "tok", nil, nil, opts...)

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	waitForListener(t, addr)

	return api, "http://" + addr, func() {
		ctxCancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("server did not shut down in time")
		}
	}
}

func waitForListener(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s never accepted a connection", addr)
}

// rpcCall performs a served RPC and decodes the result into out.
// A non-empty error envelope fails the test.
func rpcCall(t *testing.T, baseURL, method string, params any, out any) {
	t.Helper()
	if err := rpcCallErr(t, baseURL, method, params, out); err != "" {
		t.Fatalf("%s returned an error: %s", method, err)
	}
}

// rpcCallErr is rpcCall but returns the error envelope instead of failing,
// for tests that assert on the failure itself.
func rpcCallErr(t *testing.T, baseURL, method string, params any, out any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"method": method, "params": params})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp := authedPost(t, baseURL, "tok", string(raw))
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s: expected 200, got %d: %s", method, resp.StatusCode, b)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if derr := json.NewDecoder(resp.Body).Decode(&envelope); derr != nil {
		t.Fatalf("%s: decode: %v", method, derr)
	}
	if envelope.Error != "" {
		return envelope.Error
	}
	if out != nil && len(envelope.Result) > 0 {
		if uerr := json.Unmarshal(envelope.Result, out); uerr != nil {
			t.Fatalf("%s: unmarshal result %s: %v", method, envelope.Result, uerr)
		}
	}
	return ""
}

// dialChatWS opens the Sessions_Stream WebSocket and consumes the
// mandatory initial snapshot so the caller starts from a clean frame
// boundary.
func dialChatWS(t *testing.T, baseURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws"
	cfg, err := websocket.NewConfig(wsURL, "http://localhost")
	if err != nil {
		t.Fatalf("ws config: %v", err)
	}
	cfg.Header.Set("Authorization", "Bearer tok")
	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	if err := websocket.JSON.Send(ws, map[string]any{
		"method": "Sessions_Stream",
		"params": map[string]any{},
	}); err != nil {
		t.Fatalf("ws send: %v", err)
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var snapshot wsTestFrame
	if err := websocket.JSON.Receive(ws, &snapshot); err != nil {
		t.Fatalf("initial snapshot: %v", err)
	}
	if snapshot.Event != "sessions:snapshot" {
		t.Fatalf("expected sessions:snapshot first, got %q", snapshot.Event)
	}
	return ws
}

type wsTestFrame struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}

// readUntil reads frames until one matches want, ignoring others (the
// stream also carries sessions:update frames triggered by session writes).
func readUntil(t *testing.T, ws *websocket.Conn, want string, timeout time.Duration) wsTestFrame {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = ws.SetReadDeadline(deadline)
		var f wsTestFrame
		if err := websocket.JSON.Receive(ws, &f); err != nil {
			t.Fatalf("waiting for %q: receive: %v", want, err)
		}
		if f.Error != "" {
			t.Fatalf("waiting for %q: frame error: %s", want, f.Error)
		}
		if f.Event == want {
			return f
		}
	}
	t.Fatalf("never received a %q frame within %s", want, timeout)
	return wsTestFrame{}
}

// ─── the headline flow ────────────────────────────────────────────────────

// TestServedChat_CreateSendStreamStop walks the whole conversation: create
// a session, append the user turn, receive streamed assistant tokens over
// the WebSocket, then stop the stream. Before this surface existed a
// served harness could do NONE of these — it was the default app in every
// workbench and could not hold a conversation.
func TestServedChat_CreateSendStreamStop(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	// 1. Create.
	var created struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	rpcCall(t, baseURL, "Sessions_Create", map[string]any{"name": "served chat"}, &created)
	if created.ID == "" {
		t.Fatal("Sessions_Create returned no session id")
	}

	// The new session must be visible to a plain list read, i.e. it was
	// really persisted rather than echoed back.
	var listed []struct {
		ID string `json:"id"`
	}
	rpcCall(t, baseURL, "Sessions_List", map[string]any{}, &listed)
	found := false
	for _, s := range listed {
		if s.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("session %s missing from Sessions_List", created.ID)
	}

	// 2. Append the user turn.
	var userMsg struct {
		ID      string `json:"id"`
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	rpcCall(t, baseURL, "Sessions_AppendMessage", map[string]any{
		"id": created.ID, "role": "user", "content": "what is 2+2?",
	}, &userMsg)
	if userMsg.Role != "user" || userMsg.Content != "what is 2+2?" {
		t.Fatalf("unexpected persisted user message: %+v", userMsg)
	}

	var active struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	rpcCall(t, baseURL, "Sessions_ListMessagesActive", map[string]any{"id": created.ID}, &active)
	if len(active.Messages) != 1 || active.Messages[0].Content != "what is 2+2?" {
		t.Fatalf("ListMessagesActive did not return the appended turn: %+v", active.Messages)
	}

	// 3. Watch assistant tokens arrive over the WebSocket.
	ws := dialChatWS(t, baseURL)
	defer ws.Close() //nolint:errcheck

	const subID = "sub-42"
	go func() {
		time.Sleep(30 * time.Millisecond)
		for _, tok := range []string{"2", "+", "2 = 4"} {
			api.EventBus().Publish("llm:stream-chunk", llmview.StreamChunkPayload{
				SubID:     subID,
				SessionID: created.ID,
				Chunk:     corellm.StreamEvent{Kind: corellm.StreamText, Text: tok},
			})
		}
		api.EventBus().Publish("llm:stream-closed", llmview.StreamClosedPayload{
			SubID:     subID,
			SessionID: created.ID,
			Reason:    "completed",
		})
	}()

	var assembled string
	for range 3 {
		f := readUntil(t, ws, "llm:stream-chunk", 5*time.Second)
		var chunk llmview.StreamChunkPayload
		if err := json.Unmarshal(f.Data, &chunk); err != nil {
			t.Fatalf("unmarshal chunk %s: %v", f.Data, err)
		}
		if chunk.SubID != subID {
			t.Errorf("chunk sub_id = %q, want %q", chunk.SubID, subID)
		}
		if chunk.SessionID != created.ID {
			t.Errorf("chunk session_id = %q, want %q", chunk.SessionID, created.ID)
		}
		assembled += chunk.Chunk.Text
	}
	if assembled != "2+2 = 4" {
		t.Errorf("assembled stream = %q, want %q — tokens were lost or reordered", assembled, "2+2 = 4")
	}

	closedFrame := readUntil(t, ws, "llm:stream-closed", 5*time.Second)
	var closed llmview.StreamClosedPayload
	if err := json.Unmarshal(closedFrame.Data, &closed); err != nil {
		t.Fatalf("unmarshal closed %s: %v", closedFrame.Data, err)
	}
	if closed.Reason != "completed" {
		t.Errorf("close reason = %q, want completed", closed.Reason)
	}

	// 4. Stop reaches the connector. With no provider configured the chat
	// runner rejects the unknown subscription — the point is that the
	// method is dispatched for real and its error surfaces honestly rather
	// than being swallowed into a fake success.
	if errStr := rpcCallErr(t, baseURL, "LLM_StopStream", map[string]any{"subId": subID}, nil); errStr == "" {
		t.Error("LLM_StopStream reported success for a subscription the backend never issued")
	}
}

// TestServedChat_StartStreamDrivesTheRealChatRunner is the deepest
// integration this file reaches: the served LLM_StartStream really enters
// the chat runner, which really opens a subscription, fails on the
// unresolvable provider, and reports that failure back over the WebSocket
// as an llm:stream-closed frame correlated to the subscription id the
// browser was handed.
//
// It is a failure path only because a passing path would need a live
// upstream model. What it pins is the wiring: dispatch → gate → connector
// → broker → bus → WS → browser, with the sub_id correlation the frontend
// filters on. A transport that faked the start or swallowed the outcome
// would leave the chat surface spinning forever, which is precisely the
// bug class this PR exists to close.
func TestServedChat_StartStreamDrivesTheRealChatRunner(t *testing.T) {
	_, baseURL, cancel := newChatHarness(t)
	defer cancel()

	var created struct {
		ID string `json:"id"`
	}
	rpcCall(t, baseURL, "Sessions_Create", map[string]any{"name": "s"}, &created)
	rpcCall(t, baseURL, "Sessions_AppendMessage", map[string]any{
		"id": created.ID, "role": "user", "content": "hello",
	}, nil)

	ws := dialChatWS(t, baseURL)
	defer ws.Close() //nolint:errcheck

	var subID string
	errStr := rpcCallErr(t, baseURL, "LLM_StartStream", map[string]any{
		"profileId": "no-such-profile",
		"sessionId": created.ID,
	}, &subID)
	if strings.Contains(errStr, "not ported to served mode") {
		t.Fatalf("LLM_StartStream is not wired into served dispatch: %s", errStr)
	}
	if errStr != "" {
		// A synchronous rejection is also honest wiring — the connector
		// refused before opening a subscription. Nothing more to assert.
		return
	}
	if subID == "" {
		t.Fatal("LLM_StartStream returned neither a subscription id nor an error")
	}

	// The turn cannot succeed (no such provider), so the runner must close
	// the stream and say so. Silence here would be the wedged-UI bug.
	f := readUntil(t, ws, "llm:stream-closed", 20*time.Second)
	var closed llmview.StreamClosedPayload
	if err := json.Unmarshal(f.Data, &closed); err != nil {
		t.Fatalf("unmarshal closed %s: %v", f.Data, err)
	}
	if closed.SubID != subID {
		t.Errorf("close frame sub_id = %q, want %q — the frontend filters on this and would ignore the frame", closed.SubID, subID)
	}
	if closed.Reason == "completed" {
		t.Errorf("a turn against a non-existent provider reported reason=completed: %+v", closed)
	}

	// Regression guard, found by smoking the real served binary: dispatch
	// used to hand the chat runner the HTTP REQUEST context. The runner
	// streams in a goroutine that outlives the POST, so the turn was
	// cancelled the moment the response was written — every turn died on
	// its first history read with "context canceled" and the browser
	// watched a stream that could never produce a token. The turn must
	// fail for a REAL reason, not because its transport hung up on it.
	if strings.Contains(closed.Message, "context canceled") {
		t.Errorf("the turn was killed by its own transport, not by the backend: %+v", closed)
	}
}

// TestServedChat_SecretsNeverCrossTheWire is the hygiene guard: a served
// response or WS frame must never carry credential bytes. The harness
// runs inside a VM whose browser is reachable from the host, so a leak
// here is a leak onto a network, not just into a log.
func TestServedChat_SecretsNeverCrossTheWire(t *testing.T) {
	const secret = "sk-ant-super-secret-value"
	t.Setenv("ANTHROPIC_API_KEY", secret)

	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	var created struct {
		ID string `json:"id"`
	}
	rpcCall(t, baseURL, "Sessions_Create", map[string]any{"name": "s"}, &created)

	// Every ported read, as raw bytes.
	for _, call := range []struct {
		method string
		params any
	}{
		{"LLM_ListProviders", map[string]any{}},
		{"Sessions_List", map[string]any{}},
		{"Sessions_Get", map[string]any{"id": created.ID}},
		{"Sessions_ListMessagesActive", map[string]any{"id": created.ID}},
		{"Sessions_GetUsage", map[string]any{"id": created.ID}},
		{"Projects_List", map[string]any{}},
	} {
		raw, _ := json.Marshal(map[string]any{"method": call.method, "params": call.params})
		resp := authedPost(t, baseURL, "tok", string(raw))
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close() //nolint:errcheck
		if err != nil {
			t.Fatalf("%s: read body: %v", call.method, err)
		}
		if strings.Contains(string(body), secret) {
			t.Fatalf("%s leaked credential bytes onto the served wire", call.method)
		}
	}

	// And the WS path.
	ws := dialChatWS(t, baseURL)
	defer ws.Close() //nolint:errcheck

	go func() {
		time.Sleep(30 * time.Millisecond)
		api.EventBus().Publish("llm:stream-chunk", llmview.StreamChunkPayload{
			SubID:     "s1",
			SessionID: created.ID,
			Chunk:     corellm.StreamEvent{Kind: corellm.StreamText, Text: "hello"},
		})
	}()
	f := readUntil(t, ws, "llm:stream-chunk", 5*time.Second)
	if strings.Contains(string(f.Data), secret) {
		t.Fatal("credential bytes leaked onto a WebSocket frame")
	}
}

// TestServedChat_ForwardsInteractiveGateTopics pins the topics a turn
// needs in order to be answerable from inside a workbench. A tool call
// raises a permission-pending event and then BLOCKS on the user's answer;
// if the event never reaches the browser the turn hangs with no
// explanation, which reads to the user as "the harness is broken".
func TestServedChat_ForwardsInteractiveGateTopics(t *testing.T) {
	for _, topic := range []string{
		rpc.TopicBashPermissionPending,
		rpc.TopicFSPermissionPending,
		rpc.TopicCredPermissionPending,
		rpc.TopicToolPermissionPending,
		rpc.TopicSessionUsageUpdated,
		rpc.TopicLLMFallbackAttempted,
	} {
		t.Run(topic, func(t *testing.T) {
			api, baseURL, cancel := newChatHarness(t)
			defer cancel()

			ws := dialChatWS(t, baseURL)
			defer ws.Close() //nolint:errcheck

			go func() {
				time.Sleep(30 * time.Millisecond)
				api.EventBus().Publish(topic, map[string]any{"marker": topic})
			}()

			f := readUntil(t, ws, topic, 5*time.Second)
			if !strings.Contains(string(f.Data), topic) {
				t.Errorf("payload was not forwarded verbatim: %s", f.Data)
			}
		})
	}
}

// ─── backpressure ─────────────────────────────────────────────────────────

// TestServedChat_SlowConsumerIsToldItWasTruncated is the backpressure
// contract test.
//
// The bus drops events for slow subscribers, so a naive token pipe would
// hand a browser a truncated answer that LOOKS complete. The policy in
// wsstream.go says: never block the bus, bound the per-client queue, and
// when the queue overflows tell THAT client, visibly, that it missed
// data.
//
// The test builds a genuinely stalled consumer: it stops reading the
// socket entirely, so the server's writes back up through the kernel
// buffers, the (deliberately tiny) queue fills, and frames get dropped.
// When the client resumes reading it must be told — a run that ends with
// the client happily reading chunks and NO truncation notice is exactly
// the silent-loss bug this guards against.
func TestServedChat_SlowConsumerIsToldItWasTruncated(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t, serve.WithStreamQueueCap(4))
	defer cancel()

	ws := dialChatWS(t, baseURL)
	defer ws.Close() //nolint:errcheck

	// Big payloads so the socket + kernel buffers fill quickly and the
	// writer really blocks, rather than absorbing everything.
	fat := strings.Repeat("x", 64*1024)

	// The client is NOT reading during this loop — that is the stall.
	for i := range 200 {
		api.EventBus().Publish("llm:stream-chunk", llmview.StreamChunkPayload{
			SubID: fmt.Sprintf("sub-%d", i),
			Chunk: corellm.StreamEvent{Kind: corellm.StreamText, Text: fat},
		})
	}

	// Now drain. Somewhere in the backlog the server must tell us it could
	// not deliver everything.
	deadline := time.Now().Add(20 * time.Second)
	var notice serve.StreamTruncatedPayload
	sawNotice := false
	for !sawNotice && time.Now().Before(deadline) {
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		var f wsTestFrame
		if err := websocket.JSON.Receive(ws, &f); err != nil {
			t.Fatalf("draining backlog: %v", err)
		}
		if f.Event == serve.TopicStreamTruncated {
			if err := json.Unmarshal(f.Data, &notice); err != nil {
				t.Fatalf("unmarshal truncation notice %s: %v", f.Data, err)
			}
			sawNotice = true
		}
	}

	if !sawNotice {
		t.Fatal("a stalled client was silently starved of stream frames — no truncation notice arrived")
	}
	if notice.Dropped == 0 {
		t.Error("truncation notice reported zero dropped frames")
	}
	if notice.Reason == "" {
		t.Error("truncation notice carries no machine-readable reason")
	}
	// The whole point of the notice is that the user can act on it from
	// inside the VM. "Run the desktop app" is not an action available to
	// someone whose harness IS the workbench.
	if notice.Message == "" || strings.Contains(strings.ToLower(notice.Message), "desktop app") {
		t.Errorf("truncation copy is not actionable from inside a workbench: %q", notice.Message)
	}
}

// TestServedChat_HealthyConsumerIsNeverTruncated is the other half of the
// contract: the backpressure machinery must not cost correctness in the
// normal case. A client that keeps up receives every chunk, in order,
// with no truncation notice at all.
func TestServedChat_HealthyConsumerIsNeverTruncated(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t)
	defer cancel()

	ws := dialChatWS(t, baseURL)
	defer ws.Close() //nolint:errcheck

	const want = 300
	go func() {
		time.Sleep(30 * time.Millisecond)
		for i := range want {
			api.EventBus().Publish("llm:stream-chunk", llmview.StreamChunkPayload{
				SubID: "sub",
				Chunk: corellm.StreamEvent{Kind: corellm.StreamText, Text: fmt.Sprintf("%d,", i)},
			})
		}
	}()

	var got int
	var assembled strings.Builder
	deadline := time.Now().Add(20 * time.Second)
	for got < want && time.Now().Before(deadline) {
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		var f wsTestFrame
		if err := websocket.JSON.Receive(ws, &f); err != nil {
			t.Fatalf("receive: %v", err)
		}
		if f.Event == serve.TopicStreamTruncated {
			t.Fatalf("a client that kept up was told the stream was truncated: %s", f.Data)
		}
		if f.Event != "llm:stream-chunk" {
			continue
		}
		var chunk llmview.StreamChunkPayload
		if err := json.Unmarshal(f.Data, &chunk); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		assembled.WriteString(chunk.Chunk.Text)
		got++
	}
	if got != want {
		t.Fatalf("received %d of %d chunks", got, want)
	}

	var expected strings.Builder
	for i := range want {
		fmt.Fprintf(&expected, "%d,", i)
	}
	if assembled.String() != expected.String() {
		t.Error("chunks arrived out of order or with gaps")
	}
}

// TestServedChat_SlowConsumerNeverStallsTheBus guards the other failure
// mode: one stalled browser must not wedge harness-wide event delivery.
// A second, healthy client attached to the same bus keeps receiving while
// the first one is stuck.
func TestServedChat_SlowConsumerNeverStallsTheBus(t *testing.T) {
	api, baseURL, cancel := newChatHarness(t, serve.WithStreamQueueCap(4))
	defer cancel()

	stalled := dialChatWS(t, baseURL)
	defer stalled.Close() //nolint:errcheck
	healthy := dialChatWS(t, baseURL)
	defer healthy.Close() //nolint:errcheck

	// The healthy client drains continuously — that is what makes it
	// healthy — and reports when it sees the sentinel.
	sawSentinel := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		notified := false
		for {
			_ = healthy.SetReadDeadline(time.Now().Add(30 * time.Second))
			var f wsTestFrame
			if err := websocket.JSON.Receive(healthy, &f); err != nil {
				return
			}
			if f.Event == "llm:stream-closed" && !notified {
				notified = true
				close(sawSentinel)
			}
		}
	}()

	fat := strings.Repeat("y", 64*1024)

	// Wedge the first client by never reading from it.
	published := make(chan struct{})
	go func() {
		defer close(published)
		for range 200 {
			api.EventBus().Publish("llm:stream-chunk", llmview.StreamChunkPayload{
				SubID: "wedge",
				Chunk: corellm.StreamEvent{Kind: corellm.StreamText, Text: fat},
			})
		}
	}()

	select {
	case <-published:
	case <-time.After(10 * time.Second):
		t.Fatal("Publish blocked: a single stalled WebSocket client stalled the whole event bus")
	}

	// And the healthy client is still being served. The sentinel is
	// re-published on a tick because the healthy client's own queue is
	// also capped at 4 for this test, so a single sentinel racing the tail
	// of the burst can legitimately be dropped — the property under test
	// is liveness (delivery resumes), not exactly-once delivery.
	stopSentinel := make(chan struct{})
	sentinelDone := make(chan struct{})
	go func() {
		defer close(sentinelDone)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			api.EventBus().Publish("llm:stream-closed", llmview.StreamClosedPayload{
				SubID:  "wedge",
				Reason: "completed",
			})
			select {
			case <-stopSentinel:
				return
			case <-tick.C:
			}
		}
	}()

	select {
	case <-sawSentinel:
	case <-time.After(30 * time.Second):
		t.Error("a healthy client stopped receiving because a different client was stalled")
	}
	close(stopSentinel)
	<-sentinelDone

	_ = healthy.Close()
	<-readerDone
}
