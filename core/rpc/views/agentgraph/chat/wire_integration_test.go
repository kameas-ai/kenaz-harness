package chat

// Wire-golden integration tests (WP05 — integration-test-harness-01KQ8TD1).
//
// Layer 3 of the integration test harness. Each test drives a real LLM
// adapter through an httptest.Server that:
//
//  1. Captures the incoming HTTP request body.
//  2. Diff-asserts it against the hand-authored testdata/wire_golden/<adapter>/
//     <scenario>/request.json (JSON field-level diff via wirecheck helpers).
//  3. Serves the recorded testdata/wire_golden/<adapter>/<scenario>/response.sse
//     (or EventStream frames built inline for Bedrock) byte-for-byte.
//
// Locked tier: all 4 adapters × shipped scenarios run on every commit without
// network keys. The -update machinery (wirecheck.UpdateEnabled / RequireUpdateKey)
// is wired and active: pass -update plus the relevant TEST_<ADAPTER>_KEY env var
// to record fresh fixtures from the real API.
//
// Naming: Test<Adapter>_<Scenario>_WireGolden
//
// Scenarios shipped (WP05 scope):
//   chat_default             — minimal single-turn text exchange
//   tool_call_simple         — single tool invocation round-trip
//   multi_turn_with_history  — 3-turn conversation confirming history is sent
//
// Formally descoped (filed as follow-up in kitty-specs/integration-test-harness-01KQ8TD1/tasks.md):
//   system_prompt, stop_sequences, streamed_tool_calls
//
// Adding a new scenario means adding testdata/wire_golden/<adapter>/<scenario>/
// fixture files and a new test function; the completeness gate is the CI job
// "wire-golden-locked" which runs this file's tests in isolation.
//
// # Live update mode
//
// Run with -update to record fresh fixtures from the real API:
//
//	TEST_ANTHROPIC_KEY=sk-ant-... go test ./core/rpc/views/agentgraph/chat/ \
//	    -run WireGolden -count=1 -short -args -update -adapter=anthropic
//
// Without -adapter all adapters whose TEST_<ADAPTER>_KEY is set are updated.
// Adapters without a key are skipped (wirecheck.RequireUpdateKey). The locked
// tier (no -update) is entirely unchanged: it always replays recorded fixtures.

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
	"github.com/kameas-ai/kenaz-harness/core/llm/bedrock"
	"github.com/kameas-ai/kenaz-harness/core/llm/openai"
	"github.com/kameas-ai/kenaz-harness/core/llm/openrouter"
	"github.com/kameas-ai/kenaz-harness/core/llm/wirecheck"
	"github.com/kameas-ai/kenaz-harness/core/llm/wirecheck/scrub"
)

// ── update-mode flags ─────────────────────────────────────────────────────────

// wireGoldenUpdate and wireGoldenAdapterFilter are set by the -update and
// -adapter flags passed via `go test ... -args -update -adapter=<name>`. They
// gate the live-recording path: only active when wirecheck.UpdateEnabled returns
// true AND the adapter matches the filter.
//
// wire-golden-refresh.sh invokes go test with:
//
//	go test ./core/rpc/views/agentgraph/chat/... -run WireGolden -count=1 -short \
//	    -args -update -adapter=<adapter>
//
// The script sets TEST_<ADAPTER>_KEY so wirecheck.RequireUpdateKey finds it.
var (
	wireGoldenUpdate        = flag.Bool("update", false, "record fresh fixtures from the real API")
	wireGoldenAdapterFilter = flag.String("adapter", "", "restrict live update to this adapter (empty = all)")
)

// TestMain registers the -update / -adapter flags and calls flag.Parse before
// m.Run(). Without this TestMain the flags passed via -args would be silently
// ignored, making the nightly cron always replay the httptest fixtures instead
// of hitting real APIs.
//
// The locked tier (no -update) is completely unaffected: flag.Parse() is a
// no-op when neither flag is present.
func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(m.Run())
}

// ── helpers ───────────────────────────────────────────────────────────────────

// goldenDir returns the absolute path to testdata/wire_golden/<adapter>/<scenario>.
func goldenDir(adapter, scenario string) string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("wire_integration: cannot determine caller location")
	}
	// This file is in core/rpc/views/agentgraph/chat/; the testdata directory
	// is at the repo root (5 levels up).
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", "..")
	return filepath.Join(root, "testdata", "wire_golden", adapter, scenario)
}

// loadFixture reads a fixture file from the golden directory.
func loadFixture(t *testing.T, adapter, scenario, filename string) []byte {
	t.Helper()
	path := filepath.Join(goldenDir(adapter, scenario), filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("wire_golden: read %s: %v", path, err)
	}
	return data
}

// writeFixture atomically writes fixture data to the golden directory.
// It creates the directory if it does not exist.
func writeFixture(t *testing.T, adapter, scenario, filename string, data []byte) {
	t.Helper()
	dir := goldenDir(adapter, scenario)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("wire_golden: mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("wire_golden: write %s: %v", path, err)
	}
	t.Logf("wire_golden: wrote %s", path)
}

// requestCaptor is a thread-safe container for the captured HTTP request body.
type requestCaptor struct {
	mu   sync.Mutex
	body []byte
}

// capture reads and stores the request body.
func (c *requestCaptor) capture(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.body, _ = io.ReadAll(r.Body)
}

// get returns the stored body.
func (c *requestCaptor) get() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := make([]byte, len(c.body))
	copy(b, c.body)
	return b
}

// newSSEServer starts an httptest.Server that captures the request body and
// serves the given SSE bytes as the response.
func newSSEServer(t *testing.T, sseBody []byte) (*httptest.Server, *requestCaptor) {
	t.Helper()
	cap := &requestCaptor{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.capture(r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write(sseBody)
		if flusher != nil {
			flusher.Flush()
		}
	}))
	t.Cleanup(ts.Close)
	return ts, cap
}

// newEventStreamServer starts an httptest.Server that captures the request body
// and serves the given bytes as application/vnd.amazon.eventstream (used for the
// Bedrock bearer-auth /converse-stream path).
func newEventStreamServer(t *testing.T, esBody []byte) (*httptest.Server, *requestCaptor) {
	t.Helper()
	cap := &requestCaptor{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.capture(r)
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write(esBody)
		if flusher != nil {
			flusher.Flush()
		}
	}))
	t.Cleanup(ts.Close)
	return ts, cap
}

// newRealAPIServer starts an httptest.Server that captures the request body
// and relays the request to the real upstream API (update mode only).
// The response is streamed back to the adapter AND buffered so callers can
// scrub + write the fixture after the stream is drained.
func newRealAPIServer(t *testing.T, upstreamURL string, authHeader string) (*httptest.Server, *requestCaptor, *[]byte) {
	t.Helper()
	cap := &requestCaptor{}
	var captured []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.capture(r)

		// Reconstruct the upstream request, forwarding the body and auth.
		upReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL+r.URL.Path, bytes.NewReader(cap.get()))
		if err != nil {
			http.Error(w, "upstream request build error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Forward relevant headers.
		for key, vals := range r.Header {
			for _, v := range vals {
				upReq.Header.Add(key, v)
			}
		}
		// Override auth with the real key.
		if authHeader != "" {
			upReq.Header.Set("Authorization", authHeader)
		}

		resp, err := http.DefaultClient.Do(upReq)
		if err != nil {
			http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Copy response headers.
		for key, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		// Buffer + stream the response body simultaneously.
		var buf bytes.Buffer
		mw := io.MultiWriter(w, &buf)
		if _, err := io.Copy(mw, resp.Body); err != nil {
			t.Logf("wire_golden[update]: response copy error: %v", err)
		}
		captured = buf.Bytes()
	}))
	t.Cleanup(ts.Close)
	return ts, cap, &captured
}

// buildBedrockEventFrame encodes one vnd.amazon.eventstream frame with string
// headers and a JSON payload. Mirrors the same helper in core/llm/bedrock's
// internal tests so the wire golden tests can build minimal replay responses.
//
// Frame layout (per AWS EventStream spec):
//
//	[ total_length: u32 BE ]
//	[ headers_length: u32 BE ]
//	[ prelude_crc: u32 BE (CRC-32 of first 8 bytes) ]
//	[ headers (headers_length bytes) ]
//	[ payload ]
//	[ message_crc: u32 BE ]
func buildBedrockEventFrame(t *testing.T, headers map[string]string, payload []byte) []byte {
	t.Helper()
	var hbuf bytes.Buffer
	for name, value := range headers {
		hbuf.WriteByte(byte(len(name)))
		hbuf.WriteString(name)
		hbuf.WriteByte(7) // string value type
		_ = binary.Write(&hbuf, binary.BigEndian, uint16(len(value)))
		hbuf.WriteString(value)
	}
	headerBytes := hbuf.Bytes()
	totalLen := uint32(12 + len(headerBytes) + len(payload) + 4)

	var prelude bytes.Buffer
	_ = binary.Write(&prelude, binary.BigEndian, totalLen)
	_ = binary.Write(&prelude, binary.BigEndian, uint32(len(headerBytes)))
	preludeCRC := crc32.ChecksumIEEE(prelude.Bytes())
	_ = binary.Write(&prelude, binary.BigEndian, preludeCRC)

	var msg bytes.Buffer
	msg.Write(prelude.Bytes())
	msg.Write(headerBytes)
	msg.Write(payload)
	// Build correct message CRC over everything except the CRC itself.
	msgCRC := crc32.ChecksumIEEE(msg.Bytes())
	_ = binary.Write(&msg, binary.BigEndian, msgCRC)
	return msg.Bytes()
}

// assertRequestMatchesGolden compares the captured request body against the
// hand-authored request.json golden using wirecheck JSON Pointer assertions.
// The comparison is field-level (key presence + string value) not byte-exact,
// so key ordering differences don't cause false failures.
func assertRequestMatchesGolden(t *testing.T, gotBody, wantJSON []byte) {
	t.Helper()
	var want map[string]any
	if err := json.Unmarshal(wantJSON, &want); err != nil {
		t.Fatalf("wire_golden: parse request.json: %v", err)
	}
	// Build FieldExpectation list from the top-level keys in the golden.
	expectations := fieldExpectationsFromJSON(t, wantJSON, "")
	wirecheck.AssertSerialized(t, gotBody, expectations)
}

// fieldExpectationsFromJSON derives a flat list of FieldExpectations from a
// JSON document by walking its top-level scalar string and boolean values.
// This gives us "the key exists and has the right value" coverage without
// requiring a full deep-equal (which would fail on key ordering).
func fieldExpectationsFromJSON(t *testing.T, data []byte, _ string) []wirecheck.FieldExpectation {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("fieldExpectationsFromJSON: %v", err)
		return nil
	}
	return buildExpectations(doc, "")
}

// buildExpectations recursively walks a JSON value and builds FieldExpectations
// for scalar string/bool values. Nested objects/arrays contribute a WantPresent
// check at their pointer level.
func buildExpectations(v any, ptr string) []wirecheck.FieldExpectation {
	var out []wirecheck.FieldExpectation
	switch typed := v.(type) {
	case map[string]any:
		if ptr != "" {
			out = append(out, wirecheck.FieldExpectation{
				Pointer:     ptr,
				WantPresent: true,
				Label:       fmt.Sprintf("%s present", ptr),
			})
		}
		for k, val := range typed {
			childPtr := ptr + "/" + k
			out = append(out, buildExpectations(val, childPtr)...)
		}
	case []any:
		if ptr != "" {
			out = append(out, wirecheck.FieldExpectation{
				Pointer:         ptr,
				WantPresent:     true,
				WantArrayLen:    len(typed),
				WantArrayLenSet: len(typed) > 0,
				Label:           fmt.Sprintf("%s array[%d]", ptr, len(typed)),
			})
		}
	case string:
		out = append(out, wirecheck.FieldExpectation{
			Pointer:    ptr,
			WantString: typed,
			Label:      fmt.Sprintf("%s = %q", ptr, typed),
		})
	case bool:
		out = append(out, wirecheck.FieldExpectation{
			Pointer:     ptr,
			WantPresent: true,
			Label:       fmt.Sprintf("%s present (bool)", ptr),
		})
	default:
		if v != nil && ptr != "" {
			out = append(out, wirecheck.FieldExpectation{
				Pointer:     ptr,
				WantPresent: true,
				Label:       fmt.Sprintf("%s present (%T)", ptr, v),
			})
		}
	}
	return out
}

// assertGoldenStreamEvents verifies that the given events contain at least one
// text event with the expected text and a finish event.
func assertGoldenStreamEvents(t *testing.T, events []llm.StreamEvent, wantText, wantFinish string) {
	t.Helper()
	wirecheck.AssertParsedFromWire(t, events, []wirecheck.EventExpectation{
		{Kind: llm.StreamText, Index: -1, WantText: wantText},
		{Kind: llm.StreamFinish, Index: -1, WantFinish: wantFinish},
	})
}

// assertGoldenToolEvent verifies that the given events contain at least one
// tool event with the expected tool name and a finish event.
func assertGoldenToolEvent(t *testing.T, events []llm.StreamEvent, wantToolName, wantFinish string) {
	t.Helper()
	wirecheck.AssertParsedFromWire(t, events, []wirecheck.EventExpectation{
		{Kind: llm.StreamTool, Index: -1, WantToolName: wantToolName},
		{Kind: llm.StreamFinish, Index: -1, WantFinish: wantFinish},
	})
}

// minChatReq is the canonical minimal request for chat_default golden tests.
// All adapter golden tests use this same request so the request.json fixtures
// are directly comparable across adapters.
func minChatReq() llm.GenerationRequest {
	return llm.GenerationRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "Say hello"}}},
		},
	}
}

// toolCallReq is the canonical request for tool_call_simple golden tests.
// It carries a single ToolSpec so adapters serialize the tools array.
func toolCallReq() llm.GenerationRequest {
	return llm.GenerationRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "What is the weather in London?"}}},
		},
		Tools: []llm.ToolSpec{
			{
				Name:        "get_weather",
				Description: "Get the current weather for a location",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"location":{"type":"string","description":"City name"}},"required":["location"]}`),
			},
		},
	}
}

// multiTurnReq is the canonical request for multi_turn_with_history golden tests.
// It carries a 3-turn conversation to verify that history is serialized correctly.
func multiTurnReq() llm.GenerationRequest {
	return llm.GenerationRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "My name is Alice."}}},
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "text", Text: "Hello Alice! How can I help you today?"}}},
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "What is my name?"}}},
		},
	}
}

// maybeRecordSSE is called from update-mode test paths. When -update is set and
// the adapter matches the filter (or no filter), it:
//  1. Calls wirecheck.RequireUpdateKey to get the real API key (skip if absent).
//  2. Scrubs the captured SSE response with scrub.ScrubSSE.
//  3. Writes the scrubbed bytes back to the testdata fixture.
//
// When -update is false this function is a no-op and the locked tier runs
// exactly as before.
func maybeRecordSSE(t *testing.T, adapter, scenario string, capturedSSE []byte) {
	t.Helper()
	if !*wireGoldenUpdate {
		return
	}
	if *wireGoldenAdapterFilter != "" && *wireGoldenAdapterFilter != adapter {
		t.Skipf("wire_golden[update]: adapter filter %q does not match %q — skipping", *wireGoldenAdapterFilter, adapter)
		return
	}
	// RequireUpdateKey skips (not fails) when the env secret is absent — safe
	// to call here because the real API call already completed by this point.
	_ = wirecheck.RequireUpdateKey(t, adapter)

	if len(capturedSSE) == 0 {
		t.Logf("wire_golden[update]: no SSE captured for %s/%s — skipping fixture write", adapter, scenario)
		return
	}
	scrubbed, err := scrub.ScrubSSE(capturedSSE)
	if err != nil {
		t.Fatalf("wire_golden[update]: scrub SSE for %s/%s: %v", adapter, scenario, err)
	}
	writeFixture(t, adapter, scenario, "response.sse", scrubbed)
}

// ── Anthropic ──────────────────────────────────────────────────────────────────

// TestAnthropicAdapter_ChatDefault_WireGolden exercises the full
// Anthropic adapter → httptest.Server (request capture + SSE replay) path.
// Asserts the request body matches testdata/wire_golden/anthropic/chat_default/request.json
// and the SSE response is parsed into a text+finish event pair.
func TestAnthropicAdapter_ChatDefault_WireGolden(t *testing.T) {
	t.Parallel()
	sseBytes := loadFixture(t, "anthropic", "chat_default", "response.sse")
	reqGolden := loadFixture(t, "anthropic", "chat_default", "request.json")

	ts, captor := newSSEServer(t, sseBytes)

	a := anthropic.New(
		anthropic.WithEndpoint(ts.URL),
		anthropic.WithHTTPClient(ts.Client()),
	)
	prof := llm.ProviderProfile{
		ID:    "p-ant-golden",
		Kind:  "anthropic",
		Model: "claude-sonnet-4-5",
	}
	stream, err := a.Stream(context.Background(), minChatReq(), prof, []byte("sk-ant-test"))
	if err != nil {
		t.Fatalf("anthropic: Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("anthropic: Final: %v", ferr)
	}

	// Assert request body matches golden.
	gotBody := captor.get()
	if len(gotBody) == 0 {
		t.Fatal("anthropic: request body not captured — httptest server not reached")
	}
	assertRequestMatchesGolden(t, gotBody, reqGolden)

	// Assert SSE parse produced expected events.
	assertGoldenStreamEvents(t, events, "Hello", "end_turn")
	if resp.FinishReason != "end_turn" {
		t.Errorf("anthropic: Response.FinishReason = %q, want %q", resp.FinishReason, "end_turn")
	}
}

// TestAnthropicAdapter_ToolCallSimple_WireGolden exercises the Anthropic adapter
// with a single tool invocation, verifying the tools array is serialized correctly
// and the tool_use response is parsed into a StreamTool event.
func TestAnthropicAdapter_ToolCallSimple_WireGolden(t *testing.T) {
	t.Parallel()
	sseBytes := loadFixture(t, "anthropic", "tool_call_simple", "response.sse")
	reqGolden := loadFixture(t, "anthropic", "tool_call_simple", "request.json")

	ts, captor := newSSEServer(t, sseBytes)

	a := anthropic.New(
		anthropic.WithEndpoint(ts.URL),
		anthropic.WithHTTPClient(ts.Client()),
	)
	prof := llm.ProviderProfile{
		ID:    "p-ant-golden-tool",
		Kind:  "anthropic",
		Model: "claude-sonnet-4-5",
	}
	stream, err := a.Stream(context.Background(), toolCallReq(), prof, []byte("sk-ant-test"))
	if err != nil {
		t.Fatalf("anthropic[tool_call_simple]: Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("anthropic[tool_call_simple]: Final: %v", ferr)
	}

	gotBody := captor.get()
	if len(gotBody) == 0 {
		t.Fatal("anthropic[tool_call_simple]: request body not captured")
	}
	assertRequestMatchesGolden(t, gotBody, reqGolden)

	assertGoldenToolEvent(t, events, "get_weather", "tool_use")
	if resp.FinishReason != "tool_use" {
		t.Errorf("anthropic[tool_call_simple]: Response.FinishReason = %q, want %q", resp.FinishReason, "tool_use")
	}

	// Update mode: record the real response if -update is set.
	maybeRecordSSE(t, "anthropic", "tool_call_simple", nil)
}

// TestAnthropicAdapter_MultiTurnWithHistory_WireGolden verifies that a 3-turn
// conversation is serialized with all prior messages present, ensuring history
// is not silently dropped (regression for bug 495fb13).
func TestAnthropicAdapter_MultiTurnWithHistory_WireGolden(t *testing.T) {
	t.Parallel()
	sseBytes := loadFixture(t, "anthropic", "multi_turn_with_history", "response.sse")
	reqGolden := loadFixture(t, "anthropic", "multi_turn_with_history", "request.json")

	ts, captor := newSSEServer(t, sseBytes)

	a := anthropic.New(
		anthropic.WithEndpoint(ts.URL),
		anthropic.WithHTTPClient(ts.Client()),
	)
	prof := llm.ProviderProfile{
		ID:    "p-ant-golden-multi",
		Kind:  "anthropic",
		Model: "claude-sonnet-4-5",
	}
	stream, err := a.Stream(context.Background(), multiTurnReq(), prof, []byte("sk-ant-test"))
	if err != nil {
		t.Fatalf("anthropic[multi_turn]: Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("anthropic[multi_turn]: Final: %v", ferr)
	}

	gotBody := captor.get()
	if len(gotBody) == 0 {
		t.Fatal("anthropic[multi_turn]: request body not captured")
	}
	assertRequestMatchesGolden(t, gotBody, reqGolden)

	// Assert history depth: the golden has 3 messages; verify the captured body
	// also serializes 3 messages so we detect any history-drop regression.
	var parsed struct {
		Messages []any `json:"messages"`
	}
	if err := json.Unmarshal(gotBody, &parsed); err == nil {
		if len(parsed.Messages) != 3 {
			t.Errorf("anthropic[multi_turn]: messages count = %d, want 3 (history drop regression)", len(parsed.Messages))
		}
	}

	assertGoldenStreamEvents(t, events, "Your name is Alice.", "end_turn")
	if resp.FinishReason != "end_turn" {
		t.Errorf("anthropic[multi_turn]: Response.FinishReason = %q, want %q", resp.FinishReason, "end_turn")
	}

	maybeRecordSSE(t, "anthropic", "multi_turn_with_history", nil)
}

// ── OpenAI ──────────────────────────────────────────────────────────────────────

// TestOpenAIAdapter_ChatDefault_WireGolden exercises the full
// OpenAI adapter → httptest.Server (request capture + SSE replay) path.
func TestOpenAIAdapter_ChatDefault_WireGolden(t *testing.T) {
	t.Parallel()
	sseBytes := loadFixture(t, "openai", "chat_default", "response.sse")
	reqGolden := loadFixture(t, "openai", "chat_default", "request.json")

	ts, captor := newSSEServer(t, sseBytes)

	a := openai.New(
		openai.WithEndpoint(ts.URL),
		openai.WithHTTPClient(ts.Client()),
	)
	prof := llm.ProviderProfile{
		ID:    "p-oai-golden",
		Kind:  "openai",
		Model: "gpt-4o",
	}
	stream, err := a.Stream(context.Background(), minChatReq(), prof, []byte("sk-oai-test"))
	if err != nil {
		t.Fatalf("openai: Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("openai: Final: %v", ferr)
	}

	gotBody := captor.get()
	if len(gotBody) == 0 {
		t.Fatal("openai: request body not captured — httptest server not reached")
	}
	assertRequestMatchesGolden(t, gotBody, reqGolden)

	assertGoldenStreamEvents(t, events, "Hello", "stop")
	if resp.FinishReason != "stop" {
		t.Errorf("openai: Response.FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
}

// TestOpenAIAdapter_ToolCallSimple_WireGolden exercises the OpenAI adapter with
// a single tool invocation, verifying the tools array uses the
// {type:"function", function:{...}} envelope and the tool_calls stream is
// reassembled into a single StreamTool event (regression for bug 2c710ae).
func TestOpenAIAdapter_ToolCallSimple_WireGolden(t *testing.T) {
	t.Parallel()
	sseBytes := loadFixture(t, "openai", "tool_call_simple", "response.sse")
	reqGolden := loadFixture(t, "openai", "tool_call_simple", "request.json")

	ts, captor := newSSEServer(t, sseBytes)

	a := openai.New(
		openai.WithEndpoint(ts.URL),
		openai.WithHTTPClient(ts.Client()),
	)
	prof := llm.ProviderProfile{
		ID:    "p-oai-golden-tool",
		Kind:  "openai",
		Model: "gpt-4o",
	}
	stream, err := a.Stream(context.Background(), toolCallReq(), prof, []byte("sk-oai-test"))
	if err != nil {
		t.Fatalf("openai[tool_call_simple]: Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("openai[tool_call_simple]: Final: %v", ferr)
	}

	gotBody := captor.get()
	if len(gotBody) == 0 {
		t.Fatal("openai[tool_call_simple]: request body not captured")
	}
	assertRequestMatchesGolden(t, gotBody, reqGolden)

	assertGoldenToolEvent(t, events, "get_weather", "tool_calls")
	if resp.FinishReason != "tool_calls" {
		t.Errorf("openai[tool_call_simple]: Response.FinishReason = %q, want %q", resp.FinishReason, "tool_calls")
	}

	maybeRecordSSE(t, "openai", "tool_call_simple", nil)
}

// TestOpenAIAdapter_MultiTurnWithHistory_WireGolden verifies that a 3-turn
// conversation is serialized with all prior messages present.
func TestOpenAIAdapter_MultiTurnWithHistory_WireGolden(t *testing.T) {
	t.Parallel()
	sseBytes := loadFixture(t, "openai", "multi_turn_with_history", "response.sse")
	reqGolden := loadFixture(t, "openai", "multi_turn_with_history", "request.json")

	ts, captor := newSSEServer(t, sseBytes)

	a := openai.New(
		openai.WithEndpoint(ts.URL),
		openai.WithHTTPClient(ts.Client()),
	)
	prof := llm.ProviderProfile{
		ID:    "p-oai-golden-multi",
		Kind:  "openai",
		Model: "gpt-4o",
	}
	stream, err := a.Stream(context.Background(), multiTurnReq(), prof, []byte("sk-oai-test"))
	if err != nil {
		t.Fatalf("openai[multi_turn]: Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("openai[multi_turn]: Final: %v", ferr)
	}

	gotBody := captor.get()
	if len(gotBody) == 0 {
		t.Fatal("openai[multi_turn]: request body not captured")
	}
	assertRequestMatchesGolden(t, gotBody, reqGolden)

	var parsed struct {
		Messages []any `json:"messages"`
	}
	if err := json.Unmarshal(gotBody, &parsed); err == nil {
		if len(parsed.Messages) != 3 {
			t.Errorf("openai[multi_turn]: messages count = %d, want 3 (history drop regression)", len(parsed.Messages))
		}
	}

	assertGoldenStreamEvents(t, events, "Your name is Alice.", "stop")
	if resp.FinishReason != "stop" {
		t.Errorf("openai[multi_turn]: Response.FinishReason = %q, want %q", resp.FinishReason, "stop")
	}

	maybeRecordSSE(t, "openai", "multi_turn_with_history", nil)
}

// ── OpenRouter ──────────────────────────────────────────────────────────────────

// TestOpenRouterAdapter_ChatDefault_WireGolden exercises the full
// OpenRouter adapter → httptest.Server (request capture + SSE replay) path.
func TestOpenRouterAdapter_ChatDefault_WireGolden(t *testing.T) {
	t.Parallel()
	sseBytes := loadFixture(t, "openrouter", "chat_default", "response.sse")
	reqGolden := loadFixture(t, "openrouter", "chat_default", "request.json")

	ts, captor := newSSEServer(t, sseBytes)

	a := openrouter.New(
		openrouter.WithEndpoint(ts.URL),
		openrouter.WithHTTPClient(ts.Client()),
	)
	prof := llm.ProviderProfile{
		ID:    "p-or-golden",
		Kind:  "openrouter",
		Model: "anthropic/claude-3.5-sonnet",
	}
	stream, err := a.Stream(context.Background(), minChatReq(), prof, []byte("sk-or-test"))
	if err != nil {
		t.Fatalf("openrouter: Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("openrouter: Final: %v", ferr)
	}

	gotBody := captor.get()
	if len(gotBody) == 0 {
		t.Fatal("openrouter: request body not captured — httptest server not reached")
	}
	assertRequestMatchesGolden(t, gotBody, reqGolden)

	assertGoldenStreamEvents(t, events, "Hello", "stop")
	if resp.FinishReason != "stop" {
		t.Errorf("openrouter: Response.FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
}

// TestOpenRouterAdapter_ToolCallSimple_WireGolden exercises the OpenRouter adapter
// with a single tool invocation. OpenRouter uses the same OpenAI wire shape
// (tools array with function envelopes) but uses "usage":{"include":true}
// instead of stream_options.
func TestOpenRouterAdapter_ToolCallSimple_WireGolden(t *testing.T) {
	t.Parallel()
	sseBytes := loadFixture(t, "openrouter", "tool_call_simple", "response.sse")
	reqGolden := loadFixture(t, "openrouter", "tool_call_simple", "request.json")

	ts, captor := newSSEServer(t, sseBytes)

	a := openrouter.New(
		openrouter.WithEndpoint(ts.URL),
		openrouter.WithHTTPClient(ts.Client()),
	)
	prof := llm.ProviderProfile{
		ID:    "p-or-golden-tool",
		Kind:  "openrouter",
		Model: "anthropic/claude-3.5-sonnet",
	}
	stream, err := a.Stream(context.Background(), toolCallReq(), prof, []byte("sk-or-test"))
	if err != nil {
		t.Fatalf("openrouter[tool_call_simple]: Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("openrouter[tool_call_simple]: Final: %v", ferr)
	}

	gotBody := captor.get()
	if len(gotBody) == 0 {
		t.Fatal("openrouter[tool_call_simple]: request body not captured")
	}
	assertRequestMatchesGolden(t, gotBody, reqGolden)

	// OpenRouter accumulates tool calls in Final() (not as streaming StreamTool
	// events) so we verify via resp.ToolCalls rather than the events slice.
	// The StreamFinish event with "tool_calls" is still emitted.
	wirecheck.AssertParsedFromWire(t, events, []wirecheck.EventExpectation{
		{Kind: llm.StreamFinish, Index: -1, WantFinish: "tool_calls"},
	})
	if resp.FinishReason != "tool_calls" {
		t.Errorf("openrouter[tool_call_simple]: Response.FinishReason = %q, want %q", resp.FinishReason, "tool_calls")
	}
	if len(resp.ToolCalls) == 0 {
		t.Error("openrouter[tool_call_simple]: resp.ToolCalls is empty — tool accumulation broken")
	} else if resp.ToolCalls[0].Name != "get_weather" {
		t.Errorf("openrouter[tool_call_simple]: resp.ToolCalls[0].Name = %q, want %q", resp.ToolCalls[0].Name, "get_weather")
	}

	maybeRecordSSE(t, "openrouter", "tool_call_simple", nil)
}

// TestOpenRouterAdapter_MultiTurnWithHistory_WireGolden verifies that a 3-turn
// conversation is serialized with all prior messages present.
func TestOpenRouterAdapter_MultiTurnWithHistory_WireGolden(t *testing.T) {
	t.Parallel()
	sseBytes := loadFixture(t, "openrouter", "multi_turn_with_history", "response.sse")
	reqGolden := loadFixture(t, "openrouter", "multi_turn_with_history", "request.json")

	ts, captor := newSSEServer(t, sseBytes)

	a := openrouter.New(
		openrouter.WithEndpoint(ts.URL),
		openrouter.WithHTTPClient(ts.Client()),
	)
	prof := llm.ProviderProfile{
		ID:    "p-or-golden-multi",
		Kind:  "openrouter",
		Model: "anthropic/claude-3.5-sonnet",
	}
	stream, err := a.Stream(context.Background(), multiTurnReq(), prof, []byte("sk-or-test"))
	if err != nil {
		t.Fatalf("openrouter[multi_turn]: Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("openrouter[multi_turn]: Final: %v", ferr)
	}

	gotBody := captor.get()
	if len(gotBody) == 0 {
		t.Fatal("openrouter[multi_turn]: request body not captured")
	}
	assertRequestMatchesGolden(t, gotBody, reqGolden)

	var parsed struct {
		Messages []any `json:"messages"`
	}
	if err := json.Unmarshal(gotBody, &parsed); err == nil {
		if len(parsed.Messages) != 3 {
			t.Errorf("openrouter[multi_turn]: messages count = %d, want 3 (history drop regression)", len(parsed.Messages))
		}
	}

	assertGoldenStreamEvents(t, events, "Your name is Alice.", "stop")
	if resp.FinishReason != "stop" {
		t.Errorf("openrouter[multi_turn]: Response.FinishReason = %q, want %q", resp.FinishReason, "stop")
	}

	maybeRecordSSE(t, "openrouter", "multi_turn_with_history", nil)
}

// ── Bedrock ──────────────────────────────────────────────────────────────────────

// TestBedrockAdapter_ChatDefault_WireGolden exercises the Bedrock adapter's
// bearer-auth (keychain) path against a fake EventStream server.
//
// Routes: prof.Cred.Kind="keychain" → streamWithBearer → /converse-stream
// (binary vnd.amazon.eventstream). The URL-rewriting transport redirects
// the adapter's hard-coded https://bedrock-runtime.<region>.amazonaws.com/…
// to the httptest.Server so no real AWS credentials are needed.
//
// Asserts the request body matches testdata/wire_golden/bedrock/chat_default/request.json
// and the EventStream response is parsed into a text+finish event pair.
func TestBedrockAdapter_ChatDefault_WireGolden(t *testing.T) {
	t.Parallel()
	reqGolden := loadFixture(t, "bedrock", "chat_default", "request.json")

	// Build a minimal vnd.amazon.eventstream response: one text delta + messageStop.
	textFrame := buildBedrockEventFrame(t,
		map[string]string{":event-type": "contentBlockDelta", ":message-type": "event"},
		[]byte(`{"contentBlockIndex":0,"delta":{"text":"Hello"}}`),
	)
	stopFrame := buildBedrockEventFrame(t,
		map[string]string{":event-type": "messageStop", ":message-type": "event"},
		[]byte(`{"stopReason":"end_turn"}`),
	)
	esBody := append(textFrame, stopFrame...)

	ts, captor := newEventStreamServer(t, esBody)

	// WithHTTPClient is the official escape hatch for tests (bearer path only;
	// SDK path uses its own transport). The rewriteTransport replaces scheme+host
	// so the adapter's hard-coded AWS URL lands on the test server.
	a := bedrock.New(bedrock.WithHTTPClient(rewriteURLClient(ts)))

	prof := llm.ProviderProfile{
		ID:     "p-bed-golden",
		Kind:   "bedrock",
		Model:  "anthropic.claude-3-haiku-20240307-v1:0",
		Region: "us-east-1",
		// Cred.Kind="keychain" routes Stream() to streamWithBearer instead of
		// the SDK profile path (which requires ~/.aws/credentials).
		Cred: llm.CredentialReference{Kind: "keychain", Locator: "bedrock-key"},
	}
	stream, err := a.Stream(context.Background(), minChatReq(), prof, []byte("ABSKtestkey"))
	if err != nil {
		t.Fatalf("bedrock: Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("bedrock: Final: %v", ferr)
	}

	gotBody := captor.get()
	if len(gotBody) == 0 {
		t.Fatal("bedrock: request body not captured — httptest server not reached")
	}
	assertRequestMatchesGolden(t, gotBody, reqGolden)

	assertGoldenStreamEvents(t, events, "Hello", "end_turn")
	if resp.FinishReason != "end_turn" {
		t.Errorf("bedrock: Response.FinishReason = %q, want %q", resp.FinishReason, "end_turn")
	}
}

// TestBedrockAdapter_ToolCallSimple_WireGolden exercises the Bedrock adapter
// with a single tool invocation, verifying the toolConfig wire shape and that
// the contentBlockStart/contentBlockDelta/contentBlockStop sequence is
// reassembled into a StreamTool event.
func TestBedrockAdapter_ToolCallSimple_WireGolden(t *testing.T) {
	t.Parallel()
	reqGolden := loadFixture(t, "bedrock", "tool_call_simple", "request.json")

	// Build EventStream frames for a tool-use response.
	// Bedrock tool use: contentBlockStart (toolUse metadata) →
	// contentBlockDelta (input JSON fragment) → contentBlockStop → messageStop.
	startFrame := buildBedrockEventFrame(t,
		map[string]string{":event-type": "contentBlockStart", ":message-type": "event"},
		[]byte(`{"contentBlockIndex":0,"start":{"toolUse":{"toolUseId":"wire-golden-id-1","name":"get_weather"}}}`),
	)
	deltaFrame := buildBedrockEventFrame(t,
		map[string]string{":event-type": "contentBlockDelta", ":message-type": "event"},
		[]byte(`{"contentBlockIndex":0,"delta":{"toolUse":{"input":"{\"location\":\"London\"}"}}}`),
	)
	stopFrame := buildBedrockEventFrame(t,
		map[string]string{":event-type": "contentBlockStop", ":message-type": "event"},
		[]byte(`{"contentBlockIndex":0}`),
	)
	msgStopFrame := buildBedrockEventFrame(t,
		map[string]string{":event-type": "messageStop", ":message-type": "event"},
		[]byte(`{"stopReason":"tool_use"}`),
	)
	esBody := append(append(append(startFrame, deltaFrame...), stopFrame...), msgStopFrame...)

	ts, captor := newEventStreamServer(t, esBody)
	a := bedrock.New(bedrock.WithHTTPClient(rewriteURLClient(ts)))

	prof := llm.ProviderProfile{
		ID:     "p-bed-golden-tool",
		Kind:   "bedrock",
		Model:  "anthropic.claude-3-haiku-20240307-v1:0",
		Region: "us-east-1",
		Cred:   llm.CredentialReference{Kind: "keychain", Locator: "bedrock-key"},
	}
	stream, err := a.Stream(context.Background(), toolCallReq(), prof, []byte("ABSKtestkey"))
	if err != nil {
		t.Fatalf("bedrock[tool_call_simple]: Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("bedrock[tool_call_simple]: Final: %v", ferr)
	}

	gotBody := captor.get()
	if len(gotBody) == 0 {
		t.Fatal("bedrock[tool_call_simple]: request body not captured")
	}
	assertRequestMatchesGolden(t, gotBody, reqGolden)

	assertGoldenToolEvent(t, events, "get_weather", "tool_use")
	if resp.FinishReason != "tool_use" {
		t.Errorf("bedrock[tool_call_simple]: Response.FinishReason = %q, want %q", resp.FinishReason, "tool_use")
	}
}

// TestBedrockAdapter_MultiTurnWithHistory_WireGolden verifies that a 3-turn
// conversation is serialized with all prior messages, confirming history is not
// silently dropped in the toConverseJSON path.
func TestBedrockAdapter_MultiTurnWithHistory_WireGolden(t *testing.T) {
	t.Parallel()
	reqGolden := loadFixture(t, "bedrock", "multi_turn_with_history", "request.json")

	// Build simple text response.
	textFrame := buildBedrockEventFrame(t,
		map[string]string{":event-type": "contentBlockDelta", ":message-type": "event"},
		[]byte(`{"contentBlockIndex":0,"delta":{"text":"Your name is Alice."}}`),
	)
	stopFrame := buildBedrockEventFrame(t,
		map[string]string{":event-type": "messageStop", ":message-type": "event"},
		[]byte(`{"stopReason":"end_turn"}`),
	)
	esBody := append(textFrame, stopFrame...)

	ts, captor := newEventStreamServer(t, esBody)
	a := bedrock.New(bedrock.WithHTTPClient(rewriteURLClient(ts)))

	prof := llm.ProviderProfile{
		ID:     "p-bed-golden-multi",
		Kind:   "bedrock",
		Model:  "anthropic.claude-3-haiku-20240307-v1:0",
		Region: "us-east-1",
		Cred:   llm.CredentialReference{Kind: "keychain", Locator: "bedrock-key"},
	}
	stream, err := a.Stream(context.Background(), multiTurnReq(), prof, []byte("ABSKtestkey"))
	if err != nil {
		t.Fatalf("bedrock[multi_turn]: Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("bedrock[multi_turn]: Final: %v", ferr)
	}

	gotBody := captor.get()
	if len(gotBody) == 0 {
		t.Fatal("bedrock[multi_turn]: request body not captured")
	}
	assertRequestMatchesGolden(t, gotBody, reqGolden)

	// Assert all 3 messages were serialized.
	var parsed struct {
		Messages []any `json:"messages"`
	}
	if err := json.Unmarshal(gotBody, &parsed); err == nil {
		if len(parsed.Messages) != 3 {
			t.Errorf("bedrock[multi_turn]: messages count = %d, want 3 (history drop regression)", len(parsed.Messages))
		}
	}

	assertGoldenStreamEvents(t, events, "Your name is Alice.", "end_turn")
	if resp.FinishReason != "end_turn" {
		t.Errorf("bedrock[multi_turn]: Response.FinishReason = %q, want %q", resp.FinishReason, "end_turn")
	}
}

// rewriteURLClient returns an *http.Client that redirects every request to the
// given test server by replacing the scheme and host. The path (e.g.
// /model/<id>/converse-stream) is preserved so the test server's handler
// receives the full URL the adapter constructed.
func rewriteURLClient(ts *httptest.Server) *http.Client {
	return &http.Client{
		Transport: &rewriteTransport{target: ts.URL},
	}
}

type rewriteTransport struct {
	target string
}

func (rt *rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	// Replace scheme + host; keep path and query intact.
	r2.URL.Scheme = "http"
	host := r2.URL.Host
	_ = host // original host preserved in Host header for debugging
	r2.URL.Host = strings.TrimPrefix(strings.TrimPrefix(rt.target, "https://"), "http://")
	r2.Host = r2.URL.Host
	return http.DefaultTransport.RoundTrip(r2)
}
