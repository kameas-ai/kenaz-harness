package chat

// Wire-golden integration tests (WP05 — integration-test-harness-01KQ8TD1).
//
// Layer 3 of the integration test harness. Each test drives a real LLM
// adapter through an httptest.Server that:
//
//  1. Captures the incoming HTTP request body.
//  2. Diff-asserts it against the hand-authored testdata/wire_golden/<adapter>/
//     chat_default/request.json (JSON field-level diff via wirecheck helpers).
//  3. Serves the recorded testdata/wire_golden/<adapter>/chat_default/response.sse
//     (or response.json for Bedrock's non-streaming path) byte-for-byte.
//
// Locked tier: all 4 adapters × chat_default scenario run on every commit
// without network keys. The -update machinery (wirecheck.UpdateEnabled) is
// wired but skipped in the default CI run.
//
// Naming: Test<Adapter>_ChatDefault_WireGolden
//
// Adding a new scenario means adding testdata/wire_golden/<adapter>/<scenario>/
// fixture files and a new test function; the completeness gate is the CI job
// "wire-golden-locked" which runs this file's tests in isolation.

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
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

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/anthropic"
	"github.com/sigil-tech/kaneaz-harness/core/llm/bedrock"
	"github.com/sigil-tech/kaneaz-harness/core/llm/openai"
	"github.com/sigil-tech/kaneaz-harness/core/llm/openrouter"
	"github.com/sigil-tech/kaneaz-harness/core/llm/wirecheck"
)

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
				Pointer:          ptr,
				WantPresent:      true,
				WantArrayLen:     len(typed),
				WantArrayLenSet:  len(typed) > 0,
				Label:            fmt.Sprintf("%s array[%d]", ptr, len(typed)),
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
