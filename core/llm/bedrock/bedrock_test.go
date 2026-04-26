package bedrock

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/sigil-tech/kaneaz-harness/core/llm"
)

// fakeServer captures the most recent HTTP request body / headers so
// tests can assert on what the adapter sent to the Bedrock REST
// endpoint. Mirrors the openai/anthropic adapter test fixtures.
type fakeServer struct {
	*httptest.Server
	mu          sync.Mutex
	lastBody    []byte
	lastHeaders http.Header
}

func newFakeServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *fakeServer {
	t.Helper()
	fs := &fakeServer{}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fs.mu.Lock()
		fs.lastBody = body
		fs.lastHeaders = r.Header.Clone()
		fs.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(fs.Close)
	return fs
}

// stdRequest is a minimal bearer-auth request with a single user
// message, the bedrock keychain auth path, and a small region.
func stdRequest(modelID string) (llm.GenerationRequest, llm.ProviderProfile) {
	req := llm.GenerationRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}},
		},
	}
	prof := llm.ProviderProfile{
		ID:     "p-bedrock",
		Kind:   Kind,
		Model:  modelID,
		Region: "us-east-1",
		Cred:   llm.CredentialReference{Kind: "keychain", Locator: "bedrock-key"},
	}
	return req, prof
}

// rewritingClient routes requests to the fakeServer regardless of host
// the adapter chose for the hard-coded https://bedrock-runtime.<region>
// URL. This lets us exercise the bearer-auth path end-to-end.
func rewritingClient(fs *fakeServer) *http.Client {
	return &http.Client{
		Transport: rewriteTransport{target: fs.URL},
	}
}

type rewriteTransport struct {
	target string
}

func (t rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	// Replace scheme + host with the httptest endpoint.
	r2.URL.Scheme = "http"
	// Strip the leading "http://" / "https://" from target.
	host := strings.TrimPrefix(strings.TrimPrefix(t.target, "https://"), "http://")
	r2.URL.Host = host
	r2.Host = host
	return http.DefaultTransport.RoundTrip(r2)
}

// drainEvents reads all stream events into slices.
func drainEvents(s llm.Stream) ([]string, []llm.ToolUse) {
	var texts []string
	var tools []llm.ToolUse
	for ev := range s.Events() {
		switch ev.Kind {
		case llm.StreamText:
			texts = append(texts, ev.Text)
		case llm.StreamTool:
			if ev.Tool != nil {
				tools = append(tools, *ev.Tool)
			}
		}
	}
	return texts, tools
}

// TestBearer_ToolConfigSerialized verifies that GenerationRequest.Tools
// is serialized into the Converse REST body's toolConfig.tools[].toolSpec
// shape with the JSON Schema preserved verbatim under inputSchema.json.
func TestBearer_ToolConfigSerialized(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		// Respond with an empty event stream so the pump exits cleanly.
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
	})
	a := New(WithHTTPClient(rewritingClient(fs)))
	req, prof := stdRequest("anthropic.claude-3-haiku-20240307-v1:0")
	req.Tools = []llm.ToolSpec{
		{
			Name:        "calc.add",
			Description: "Add two numbers",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"number"}},"required":["a"]}`),
		},
		{Name: "no_schema"},
	}
	stream, err := a.Stream(context.Background(), req, prof, []byte("ABSKtestkey"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	_, _ = stream.Final()

	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.lastBody) == 0 {
		t.Fatal("captured body is empty")
	}
	var body struct {
		ToolConfig struct {
			Tools []struct {
				ToolSpec struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					InputSchema struct {
						JSON map[string]any `json:"json"`
					} `json:"inputSchema"`
				} `json:"toolSpec"`
			} `json:"tools"`
		} `json:"toolConfig"`
	}
	if err := json.Unmarshal(fs.lastBody, &body); err != nil {
		t.Fatalf("unmarshal body: %v\nbody=%s", err, fs.lastBody)
	}
	if len(body.ToolConfig.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d (body=%s)", len(body.ToolConfig.Tools), fs.lastBody)
	}
	if body.ToolConfig.Tools[0].ToolSpec.Name != "calc.add" {
		t.Fatalf("tools[0].toolSpec.name = %q", body.ToolConfig.Tools[0].ToolSpec.Name)
	}
	if body.ToolConfig.Tools[0].ToolSpec.Description != "Add two numbers" {
		t.Fatalf("tools[0].toolSpec.description = %q", body.ToolConfig.Tools[0].ToolSpec.Description)
	}
	if body.ToolConfig.Tools[0].ToolSpec.InputSchema.JSON["type"] != "object" {
		t.Fatalf("tools[0].inputSchema.json.type missing: %+v", body.ToolConfig.Tools[0].ToolSpec.InputSchema.JSON)
	}
	props, _ := body.ToolConfig.Tools[0].ToolSpec.InputSchema.JSON["properties"].(map[string]any)
	if _, ok := props["a"]; !ok {
		t.Fatalf("tools[0].inputSchema.json.properties.a missing: %+v", body.ToolConfig.Tools[0].ToolSpec.InputSchema.JSON)
	}
	// Empty schema falls back to {"type":"object"}.
	if body.ToolConfig.Tools[1].ToolSpec.InputSchema.JSON["type"] != "object" {
		t.Fatalf("tools[1] schema fallback failed: %+v", body.ToolConfig.Tools[1].ToolSpec.InputSchema.JSON)
	}
}

// TestBearer_ToolConfigAbsentWhenEmpty verifies that requests without
// tools do NOT include a toolConfig field in the body.
func TestBearer_ToolConfigAbsentWhenEmpty(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
	})
	a := New(WithHTTPClient(rewritingClient(fs)))
	req, prof := stdRequest("anthropic.claude-3-haiku-20240307-v1:0")
	stream, err := a.Stream(context.Background(), req, prof, []byte("ABSKtestkey"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	_, _ = stream.Final()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if bytes.Contains(fs.lastBody, []byte(`"toolConfig"`)) {
		t.Fatalf("body should not contain toolConfig when no tools provided: %s", fs.lastBody)
	}
}

// encodeEventStreamMessage builds one vnd.amazon.eventstream frame
// matching the format the adapter's parser expects. Only string-typed
// headers are needed for our purposes.
func encodeEventStreamMessage(t *testing.T, headers map[string]string, payload []byte) []byte {
	t.Helper()
	// Headers block.
	var hbuf bytes.Buffer
	for name, value := range headers {
		if len(name) > 255 {
			t.Fatalf("header name too long")
		}
		hbuf.WriteByte(byte(len(name)))
		hbuf.WriteString(name)
		hbuf.WriteByte(7) // string type
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
	// Message CRC (we don't validate it on the read side, so any
	// 4-byte suffix works; use 0 for simplicity).
	_ = binary.Write(&msg, binary.BigEndian, uint32(0))
	return msg.Bytes()
}

// TestBearer_ToolUseRoundTrip verifies that a Converse event stream
// containing toolUse frames parses back into a ToolUse value with the
// reassembled JSON arguments, surfaced both via the StreamTool event
// and Response.ToolCalls.
func TestBearer_ToolUseRoundTrip(t *testing.T) {
	frames := [][]byte{
		encodeEventStreamMessage(t,
			map[string]string{":event-type": "contentBlockStart", ":message-type": "event"},
			[]byte(`{"contentBlockIndex":0,"start":{"toolUse":{"toolUseId":"tool_1","name":"calc.add"}}}`),
		),
		encodeEventStreamMessage(t,
			map[string]string{":event-type": "contentBlockDelta", ":message-type": "event"},
			[]byte(`{"contentBlockIndex":0,"delta":{"toolUse":{"input":"{\"a\":"}}}`),
		),
		encodeEventStreamMessage(t,
			map[string]string{":event-type": "contentBlockDelta", ":message-type": "event"},
			[]byte(`{"contentBlockIndex":0,"delta":{"toolUse":{"input":"42}"}}}`),
		),
		encodeEventStreamMessage(t,
			map[string]string{":event-type": "contentBlockStop", ":message-type": "event"},
			[]byte(`{"contentBlockIndex":0}`),
		),
		encodeEventStreamMessage(t,
			map[string]string{":event-type": "messageStop", ":message-type": "event"},
			[]byte(`{"stopReason":"tool_use"}`),
		),
	}

	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, frame := range frames {
			_, _ = w.Write(frame)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	a := New(WithHTTPClient(rewritingClient(fs)))
	req, prof := stdRequest("anthropic.claude-3-haiku-20240307-v1:0")
	req.Tools = []llm.ToolSpec{{Name: "calc.add", Description: "add"}}
	stream, err := a.Stream(context.Background(), req, prof, []byte("ABSKtestkey"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_, tools := drainEvents(stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool event, got %d", len(tools))
	}
	if tools[0].Name != "calc.add" {
		t.Fatalf("tool name = %q", tools[0].Name)
	}
	if tools[0].ID != "tool_1" {
		t.Fatalf("tool id = %q", tools[0].ID)
	}
	if string(tools[0].Input) != `{"a":42}` {
		t.Fatalf("tool input = %s, want %q", tools[0].Input, `{"a":42}`)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "calc.add" {
		t.Fatalf("Response.ToolCalls = %+v", resp.ToolCalls)
	}
	if resp.FinishReason != "tool_use" {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
}

// TestBearer_ImageBlock_Serialized verifies the bearer-auth REST path
// emits an image content block with the right format + base64 source
// shape per the Converse contract:
//
//	{"image":{"format":"png","source":{"bytes":"<b64>"}}}
//
// The bearer path stays in JSON the whole way, so source.bytes is the
// base64 string verbatim rather than the SDK path's []byte.
func TestBearer_ImageBlock_Serialized(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
	})
	a := New(WithHTTPClient(rewritingClient(fs)))
	req, prof := stdRequest("anthropic.claude-3-haiku-20240307-v1:0")
	req.Messages = []llm.Message{{
		Role: llm.RoleUser,
		Content: []llm.ContentBlock{
			{Type: "text", Text: "describe this"},
			{Type: "image", Source: &llm.MediaSource{
				Kind: "base64", MediaType: "image/png", Data: "iVBORw0KGgo=",
			}},
		},
	}}
	stream, err := a.Stream(context.Background(), req, prof, []byte("ABSKtestkey"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	_, _ = stream.Final()

	fs.mu.Lock()
	defer fs.mu.Unlock()
	var body struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(fs.lastBody, &body); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, fs.lastBody)
	}
	if len(body.Messages) != 1 || len(body.Messages[0].Content) != 2 {
		t.Fatalf("body shape unexpected: %s", fs.lastBody)
	}
	imgBlock, ok := body.Messages[0].Content[1]["image"].(map[string]any)
	if !ok {
		t.Fatalf("image block missing: %+v", body.Messages[0].Content[1])
	}
	if imgBlock["format"] != "png" {
		t.Fatalf("image format = %v", imgBlock["format"])
	}
	src, ok := imgBlock["source"].(map[string]any)
	if !ok {
		t.Fatalf("image source missing: %+v", imgBlock)
	}
	if src["bytes"] != "iVBORw0KGgo=" {
		t.Fatalf("image source bytes = %v", src["bytes"])
	}
}

// TestBearer_DocumentBlock_DefaultsName verifies that a document block
// without an OriginalName defaults to "document" — Converse rejects
// requests with an empty name field per the API contract.
func TestBearer_DocumentBlock_DefaultsName(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
	})
	a := New(WithHTTPClient(rewritingClient(fs)))
	req, prof := stdRequest("anthropic.claude-3-haiku-20240307-v1:0")
	req.Messages = []llm.Message{{
		Role: llm.RoleUser,
		Content: []llm.ContentBlock{
			{Type: "text", Text: "summarize"},
			{Type: "document", Source: &llm.MediaSource{
				Kind: "base64", MediaType: "application/pdf", Data: "JVBERi0=",
			}},
		},
	}}
	stream, err := a.Stream(context.Background(), req, prof, []byte("ABSKtestkey"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	_, _ = stream.Final()

	fs.mu.Lock()
	defer fs.mu.Unlock()
	var body struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(fs.lastBody, &body); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, fs.lastBody)
	}
	if len(body.Messages) != 1 || len(body.Messages[0].Content) != 2 {
		t.Fatalf("body shape unexpected: %s", fs.lastBody)
	}
	docBlock, ok := body.Messages[0].Content[1]["document"].(map[string]any)
	if !ok {
		t.Fatalf("document block missing: %+v", body.Messages[0].Content[1])
	}
	if docBlock["format"] != "pdf" {
		t.Fatalf("doc format = %v", docBlock["format"])
	}
	if docBlock["name"] != "document" {
		t.Fatalf("doc name = %v, want default 'document'", docBlock["name"])
	}
}

// TestBearer_DocumentBlock_PreservesOriginalName confirms the explicit
// name path: when MediaSource.OriginalName is set, the adapter forwards
// it verbatim (subject to provider-side validation, which is documented
// to reject names with disallowed characters but is the user's problem).
func TestBearer_DocumentBlock_PreservesOriginalName(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
	})
	a := New(WithHTTPClient(rewritingClient(fs)))
	req, prof := stdRequest("anthropic.claude-3-haiku-20240307-v1:0")
	req.Messages = []llm.Message{{
		Role: llm.RoleUser,
		Content: []llm.ContentBlock{
			{Type: "document", Source: &llm.MediaSource{
				Kind: "base64", MediaType: "application/pdf", Data: "JVBERi0=",
				OriginalName: "report-q3",
			}},
		},
	}}
	stream, err := a.Stream(context.Background(), req, prof, []byte("ABSKtestkey"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	_, _ = stream.Final()

	fs.mu.Lock()
	defer fs.mu.Unlock()
	if !bytes.Contains(fs.lastBody, []byte(`"name":"report-q3"`)) {
		t.Fatalf("document name not preserved: %s", fs.lastBody)
	}
}

// TestToBedrockContentBlocks_ImageDocument exercises the SDK-path
// content-block builder. The SDK path can't be hit via httptest (it
// signs requests), so we validate the resulting struct shape.
func TestToBedrockContentBlocks_ImageDocument(t *testing.T) {
	parts := []llm.ContentBlock{
		{Type: "text", Text: "alpha"},
		{Type: "image", Source: &llm.MediaSource{
			Kind: "base64", MediaType: "image/jpeg", Data: "/9j/4AAQ",
		}},
		{Type: "document", Source: &llm.MediaSource{
			Kind: "base64", MediaType: "application/pdf", Data: "JVBERi0=",
			OriginalName: "manual",
		}},
	}
	blocks := toBedrockContentBlocks(parts)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want 3", len(blocks))
	}
	if _, ok := blocks[0].(*types.ContentBlockMemberText); !ok {
		t.Fatalf("blocks[0] = %T", blocks[0])
	}
	imgBlock, ok := blocks[1].(*types.ContentBlockMemberImage)
	if !ok {
		t.Fatalf("blocks[1] = %T", blocks[1])
	}
	if string(imgBlock.Value.Format) != "jpeg" {
		t.Fatalf("image format = %q", imgBlock.Value.Format)
	}
	imgSrc, ok := imgBlock.Value.Source.(*types.ImageSourceMemberBytes)
	if !ok {
		t.Fatalf("image source = %T", imgBlock.Value.Source)
	}
	// The SDK path decodes base64 — bytes are the raw decoded payload.
	if len(imgSrc.Value) == 0 {
		t.Fatalf("image bytes empty")
	}
	docBlock, ok := blocks[2].(*types.ContentBlockMemberDocument)
	if !ok {
		t.Fatalf("blocks[2] = %T", blocks[2])
	}
	if docBlock.Value.Name == nil || *docBlock.Value.Name != "manual" {
		t.Fatalf("doc name = %v", docBlock.Value.Name)
	}
	if string(docBlock.Value.Format) != "pdf" {
		t.Fatalf("doc format = %q", docBlock.Value.Format)
	}
	docSrc, ok := docBlock.Value.Source.(*types.DocumentSourceMemberBytes)
	if !ok || len(docSrc.Value) == 0 {
		t.Fatalf("doc source = %T %v", docBlock.Value.Source, docSrc)
	}
}

// TestToBedrockContentBlocks_DefaultsDocumentName verifies the
// "document" fallback when MediaSource.OriginalName is empty (Converse
// rejects empty / missing document names).
func TestToBedrockContentBlocks_DefaultsDocumentName(t *testing.T) {
	parts := []llm.ContentBlock{{
		Type: "document", Source: &llm.MediaSource{
			Kind: "base64", MediaType: "application/pdf", Data: "JVBERi0=",
		},
	}}
	blocks := toBedrockContentBlocks(parts)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d", len(blocks))
	}
	doc := blocks[0].(*types.ContentBlockMemberDocument)
	if doc.Value.Name == nil || *doc.Value.Name != "document" {
		t.Fatalf("doc name = %v", doc.Value.Name)
	}
}

// TestToBedrockContentBlocks_DropsUnsupportedFormats confirms that
// unknown image / document MIME types are silently dropped (capability
// gating in WP03 will surface these as a friendly error before the
// builder runs).
func TestToBedrockContentBlocks_DropsUnsupportedFormats(t *testing.T) {
	parts := []llm.ContentBlock{
		{Type: "image", Source: &llm.MediaSource{Kind: "base64", MediaType: "image/heic", Data: "AAAA"}},
		{Type: "document", Source: &llm.MediaSource{Kind: "base64", MediaType: "application/zip", Data: "AAAA"}},
		{Type: "text", Text: "still here"},
	}
	blocks := toBedrockContentBlocks(parts)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1 (text only)", len(blocks))
	}
	if _, ok := blocks[0].(*types.ContentBlockMemberText); !ok {
		t.Fatalf("blocks[0] = %T", blocks[0])
	}
}

// TestBuildToolConfig exercises the SDK-path tool builder. The Bedrock
// SDK can't easily be hit against a fake server (it signs requests and
// expects specific endpoints), so we validate the resulting struct
// shape directly.
func TestBuildToolConfig(t *testing.T) {
	specs := []llm.ToolSpec{
		{
			Name:        "search",
			Description: "Search the web",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`),
		},
		{Name: "ping"},
	}
	cfg, err := buildToolConfig(specs)
	if err != nil {
		t.Fatalf("buildToolConfig: %v", err)
	}
	if cfg == nil || len(cfg.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %+v", cfg)
	}
	first, ok := cfg.Tools[0].(*types.ToolMemberToolSpec)
	if !ok {
		t.Fatalf("expected *types.ToolMemberToolSpec, got %T", cfg.Tools[0])
	}
	if first.Value.Name == nil || *first.Value.Name != "search" {
		t.Fatalf("tools[0].name = %v", first.Value.Name)
	}
	if first.Value.Description == nil || *first.Value.Description != "Search the web" {
		t.Fatalf("tools[0].description = %v", first.Value.Description)
	}
	schemaMember, ok := first.Value.InputSchema.(*types.ToolInputSchemaMemberJson)
	if !ok || schemaMember.Value == nil {
		t.Fatalf("tools[0].inputSchema = %T %+v", first.Value.InputSchema, first.Value.InputSchema)
	}
	// Round-trip the document back to JSON to confirm the schema bytes
	// survive intact.
	raw, err := schemaMember.Value.MarshalSmithyDocument()
	if err != nil {
		t.Fatalf("MarshalSmithyDocument: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["type"] != "object" {
		t.Fatalf("schema.type = %v", got["type"])
	}

	// Empty schema falls back to {"type":"object"}.
	second := cfg.Tools[1].(*types.ToolMemberToolSpec)
	secondSchema, _ := second.Value.InputSchema.(*types.ToolInputSchemaMemberJson)
	rawSecond, _ := secondSchema.Value.MarshalSmithyDocument()
	var gotSecond map[string]any
	_ = json.Unmarshal(rawSecond, &gotSecond)
	if gotSecond["type"] != "object" {
		t.Fatalf("tools[1] schema fallback failed: %+v", gotSecond)
	}
}
