// Bearer-token path for AWS Bedrock.
//
// AWS shipped long-lived API keys for Bedrock in 2025 — short bearer
// tokens you generate from the console under Bedrock → API keys. They
// authenticate with `Authorization: Bearer <key>` instead of SigV4
// signed requests, so users don't have to configure ~/.aws/credentials.
//
// The AWS SDK v2 doesn't expose a public bearer-auth path for the
// streaming endpoints, so this file rolls a small HTTP client that
// hits the REST endpoint directly and parses the
// vnd.amazon.eventstream binary protocol the same way the SDK does
// internally. The bytes-on-the-wire format is documented at
// https://docs.aws.amazon.com/transcribe/latest/dg/event-stream.html.
package bedrock

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"strings"

	"github.com/sigil-tech/kaneaz-harness/core/llm"
)

// streamWithBearer opens a Converse-style stream against the Bedrock
// REST endpoint using a bearer API key. Mirrors the SDK path's
// behaviour so the caller (Stream) doesn't care which auth flavour
// is in play.
func (a *Adapter) streamWithBearer(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, apiKey string) (llm.Stream, error) {
	if strings.TrimSpace(prof.Region) == "" {
		return nil, &llm.ErrAuth{Message: "bedrock: profile region required"}
	}
	bedrockMessages, system, err := toConverseJSON(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("bedrock: convert messages: %w", err)
	}
	body := map[string]any{
		"messages": bedrockMessages,
	}
	if len(system) > 0 {
		body["system"] = system
	}
	// Tool serialization — the REST Converse endpoint accepts the same
	// toolConfig.tools[].toolSpec shape the SDK builds. inputSchema.json
	// carries the raw JSON Schema.
	// Reference: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html
	if len(req.Tools) > 0 {
		toolCfg, err := toConverseToolConfigJSON(req.Tools)
		if err != nil {
			return nil, &llm.ErrInvalidRequest{Message: err.Error()}
		}
		body["toolConfig"] = toolCfg
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("bedrock: marshal body: %w", err)
	}
	url := fmt.Sprintf(
		"https://bedrock-runtime.%s.amazonaws.com/model/%s/converse-stream",
		prof.Region,
		prof.Model,
	)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("bedrock: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/vnd.amazon.eventstream")

	resp, err := a.bearerClient().Do(httpReq)
	if err != nil {
		return nil, &llm.ErrTransient{Message: "bedrock: " + err.Error(), Cause: err}
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, classifyBedrockHTTPStatus(resp.StatusCode, string(body))
	}

	stream := &bearerStream{
		body:   resp.Body,
		events: make(chan llm.StreamEvent, 64),
		done:   make(chan struct{}),
	}
	go stream.pump()
	return stream, nil
}

// bearerClient returns the HTTP client used for bearer-auth Bedrock
// calls. Tests can swap via WithHTTPClient; production gets a stdlib
// default with no per-request timeout (context drives lifetime).
func (a *Adapter) bearerClient() *http.Client {
	if a.httpc != nil {
		return a.httpc
	}
	return http.DefaultClient
}

// toConverseToolConfigJSON converts the harness's ToolSpec slice into
// the JSON shape Bedrock's Converse REST API accepts under
// toolConfig.tools[].toolSpec. Mirrors buildToolConfig for the SDK
// path but produces plain map[string]any so we can json.Marshal.
func toConverseToolConfigJSON(specs []llm.ToolSpec) (map[string]any, error) {
	tools := make([]map[string]any, 0, len(specs))
	for _, t := range specs {
		var schema any
		if len(t.InputSchema) > 0 {
			if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
				return nil, fmt.Errorf("bedrock: tool %q input_schema: %w", t.Name, err)
			}
		} else {
			schema = map[string]any{"type": "object"}
		}
		spec := map[string]any{
			"name":        t.Name,
			"inputSchema": map[string]any{"json": schema},
		}
		if t.Description != "" {
			spec["description"] = t.Description
		}
		tools = append(tools, map[string]any{"toolSpec": spec})
	}
	return map[string]any{"tools": tools}, nil
}

// toConverseJSON converts the harness's GenerationRequest message
// slice into the JSON shape Bedrock's Converse REST API accepts.
// Mirrors toBedrockMessages for the SDK path but produces plain
// map[string]any so we can json.Marshal.
func toConverseJSON(msgs []llm.Message) ([]map[string]any, []map[string]any, error) {
	var converseMsgs []map[string]any
	var system []map[string]any
	for _, m := range msgs {
		text := flattenContent(m.Content)
		if text == "" {
			continue
		}
		switch m.Role {
		case llm.RoleSystem:
			system = append(system, map[string]any{"text": text})
		case llm.RoleUser:
			converseMsgs = append(converseMsgs, map[string]any{
				"role":    "user",
				"content": []map[string]any{{"text": text}},
			})
		case llm.RoleAssistant:
			converseMsgs = append(converseMsgs, map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"text": text}},
			})
		default:
			continue
		}
	}
	if len(converseMsgs) == 0 {
		return nil, nil, errors.New("bedrock: no user/assistant messages in request")
	}
	return converseMsgs, system, nil
}

// listModelsWithBearer returns the merged inference-profile +
// foundation-model catalogue using the bearer-auth REST endpoint.
// Inference profiles come first because they are the only callable
// id for newer models (Llama 4, Claude 3.5+, Nova).
func (a *Adapter) listModelsWithBearer(ctx context.Context, apiKey string) ([]llm.ModelInfo, error) {
	out := []llm.ModelInfo{}

	// 1. Inference profiles.
	if profiles, err := a.bearerListInferenceProfiles(ctx, apiKey); err == nil {
		out = append(out, profiles...)
	}

	// 2. Foundation models with on-demand throughput.
	models, err := a.bearerListFoundationModels(ctx, apiKey)
	if err != nil && len(out) == 0 {
		return nil, err
	}
	out = append(out, models...)
	return out, nil
}

func (a *Adapter) bearerListInferenceProfiles(ctx context.Context, apiKey string) ([]llm.ModelInfo, error) {
	url := fmt.Sprintf("https://bedrock.%s.amazonaws.com/inference-profiles", a.listModelsRegion)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "application/json")
	resp, err := a.bearerClient().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("bedrock: list inference profiles: HTTP %d", resp.StatusCode)
	}
	var doc struct {
		InferenceProfileSummaries []struct {
			InferenceProfileID   string `json:"inferenceProfileId"`
			InferenceProfileName string `json:"inferenceProfileName"`
		} `json:"inferenceProfileSummaries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}
	out := make([]llm.ModelInfo, 0, len(doc.InferenceProfileSummaries))
	for _, p := range doc.InferenceProfileSummaries {
		if p.InferenceProfileID == "" {
			continue
		}
		name := p.InferenceProfileName
		if name == "" {
			name = p.InferenceProfileID
		}
		out = append(out, llm.ModelInfo{
			ID:          p.InferenceProfileID,
			DisplayName: name + " (inference profile)",
		})
	}
	return out, nil
}

func (a *Adapter) bearerListFoundationModels(ctx context.Context, apiKey string) ([]llm.ModelInfo, error) {
	url := fmt.Sprintf("https://bedrock.%s.amazonaws.com/foundation-models", a.listModelsRegion)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "application/json")
	resp, err := a.bearerClient().Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("bedrock: list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, classifyBedrockHTTPStatus(resp.StatusCode, string(body))
	}
	var doc struct {
		ModelSummaries []struct {
			ModelID                    string   `json:"modelId"`
			ModelName                  string   `json:"modelName"`
			ResponseStreamingSupported *bool    `json:"responseStreamingSupported"`
			OutputModalities           []string `json:"outputModalities"`
			InferenceTypesSupported    []string `json:"inferenceTypesSupported"`
		} `json:"modelSummaries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("bedrock: parse models: %w", err)
	}
	out := make([]llm.ModelInfo, 0, len(doc.ModelSummaries))
	for _, m := range doc.ModelSummaries {
		if m.ResponseStreamingSupported != nil && !*m.ResponseStreamingSupported {
			continue
		}
		if !containsText(m.OutputModalities) {
			continue
		}
		if !containsOnDemand(m.InferenceTypesSupported) {
			continue
		}
		display := m.ModelName
		if display == "" {
			display = m.ModelID
		}
		out = append(out, llm.ModelInfo{ID: m.ModelID, DisplayName: display})
	}
	return out, nil
}

func containsOnDemand(types []string) bool {
	if len(types) == 0 {
		return true
	}
	for _, t := range types {
		if strings.EqualFold(t, "ON_DEMAND") {
			return true
		}
	}
	return false
}

func containsText(modalities []string) bool {
	if len(modalities) == 0 {
		return true
	}
	for _, m := range modalities {
		if strings.EqualFold(m, "TEXT") {
			return true
		}
	}
	return false
}

// classifyBedrockHTTPStatus maps a non-2xx HTTP response to the
// harness's typed error taxonomy.
func classifyBedrockHTTPStatus(code int, body string) error {
	switch {
	case code == 401 || code == 403:
		return &llm.ErrAuth{Status: code, Message: body}
	case code == 400 || code == 404 || code == 422:
		return &llm.ErrInvalidRequest{Status: code, Message: body}
	case code == 429:
		return &llm.ErrTransient{Status: 429, Message: body}
	case code >= 500:
		return &llm.ErrTransient{Status: code, Message: body}
	default:
		return &llm.ErrTransient{Status: code, Message: body}
	}
}

// bearerStream wraps a vnd.amazon.eventstream HTTP body so it
// satisfies llm.Stream. The pump runs the event-stream parser on a
// goroutine and translates Converse frames into llm.StreamEvent.
type bearerStream struct {
	body      io.ReadCloser
	events    chan llm.StreamEvent
	done      chan struct{}
	finalResp llm.Response
	finalErr  error
	cancelled bool

	// toolPartial reassembles tool_use blocks across contentBlockStart
	// / contentBlockDelta / contentBlockStop frames (Converse keys
	// these by an integer contentBlockIndex).
	toolPartial map[int]*toolUseAccum
}

func (s *bearerStream) Events() <-chan llm.StreamEvent { return s.events }

func (s *bearerStream) Cancel() error {
	if s.cancelled {
		return nil
	}
	s.cancelled = true
	if s.body != nil {
		_ = s.body.Close()
	}
	return nil
}

func (s *bearerStream) Final() (llm.Response, error) {
	<-s.done
	if s.cancelled {
		return s.finalResp, &llm.ErrCancelled{Reason: "cancelled"}
	}
	return s.finalResp, s.finalErr
}

func (s *bearerStream) pump() {
	defer close(s.events)
	defer close(s.done)
	defer s.body.Close()

	for {
		msg, err := readEventStreamMessage(s.body)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			if !s.cancelled {
				s.finalErr = err
				s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: err.Error()}
			}
			return
		}
		s.dispatch(msg)
	}
}

// dispatch maps one parsed event-stream message into a StreamEvent.
// Header `:event-type` selects the JSON payload schema.
func (s *bearerStream) dispatch(msg eventStreamMessage) {
	eventType := msg.headers[":event-type"]
	messageType := msg.headers[":message-type"]
	if messageType == "exception" {
		s.finalErr = fmt.Errorf("bedrock stream error %s: %s",
			msg.headers[":exception-type"], string(msg.payload))
		s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: s.finalErr.Error()}
		return
	}
	switch eventType {
	case "contentBlockStart":
		var p struct {
			ContentBlockIndex int `json:"contentBlockIndex"`
			Start             struct {
				ToolUse *struct {
					ToolUseID string `json:"toolUseId"`
					Name      string `json:"name"`
				} `json:"toolUse"`
			} `json:"start"`
		}
		if err := json.Unmarshal(msg.payload, &p); err != nil || p.Start.ToolUse == nil {
			return
		}
		if s.toolPartial == nil {
			s.toolPartial = map[int]*toolUseAccum{}
		}
		s.toolPartial[p.ContentBlockIndex] = &toolUseAccum{
			id:   p.Start.ToolUse.ToolUseID,
			name: p.Start.ToolUse.Name,
		}
	case "contentBlockDelta":
		var p struct {
			ContentBlockIndex int `json:"contentBlockIndex"`
			Delta             struct {
				Text    string `json:"text"`
				ToolUse *struct {
					Input string `json:"input"`
				} `json:"toolUse"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(msg.payload, &p); err != nil {
			return
		}
		if p.Delta.Text != "" {
			s.events <- llm.StreamEvent{Kind: llm.StreamText, Text: p.Delta.Text}
		}
		if p.Delta.ToolUse != nil {
			if accum, ok := s.toolPartial[p.ContentBlockIndex]; ok {
				accum.input.WriteString(p.Delta.ToolUse.Input)
			}
		}
	case "contentBlockStop":
		var p struct {
			ContentBlockIndex int `json:"contentBlockIndex"`
		}
		if err := json.Unmarshal(msg.payload, &p); err != nil {
			return
		}
		if accum, ok := s.toolPartial[p.ContentBlockIndex]; ok {
			delete(s.toolPartial, p.ContentBlockIndex)
			input := accum.input.String()
			if input == "" {
				input = "{}"
			}
			tool := llm.ToolUse{
				ID:    accum.id,
				Name:  accum.name,
				Input: json.RawMessage(input),
			}
			s.finalResp.ToolCalls = append(s.finalResp.ToolCalls, tool)
			s.events <- llm.StreamEvent{Kind: llm.StreamTool, Tool: &tool}
		}
	case "messageStop":
		var p struct {
			StopReason string `json:"stopReason"`
		}
		_ = json.Unmarshal(msg.payload, &p)
		s.finalResp.FinishReason = p.StopReason
		s.events <- llm.StreamEvent{Kind: llm.StreamFinish, Finish: p.StopReason}
	case "metadata":
		var p struct {
			Usage struct {
				InputTokens  int `json:"inputTokens"`
				OutputTokens int `json:"outputTokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(msg.payload, &p); err == nil {
			usage := llm.Usage{
				InputTokens:  p.Usage.InputTokens,
				OutputTokens: p.Usage.OutputTokens,
			}
			s.finalResp.Usage = usage
			usageCopy := usage
			s.events <- llm.StreamEvent{Kind: llm.StreamUsage, Usage: &usageCopy}
		}
	default:
		// messageStart and other frames — not surfaced to the UI today.
	}
}

// eventStreamMessage is one decoded vnd.amazon.eventstream frame.
type eventStreamMessage struct {
	headers map[string]string
	payload []byte
}

// readEventStreamMessage parses one event-stream frame from r.
//
// Frame layout:
//
//	[ total_length: u32 BE ]
//	[ headers_length: u32 BE ]
//	[ prelude_crc: u32 BE (CRC-32 of first 8 bytes) ]
//	[ headers (headers_length bytes) ]
//	[ payload (total_length - headers_length - 16 bytes) ]
//	[ message_crc: u32 BE (CRC-32 of every byte except this one) ]
func readEventStreamMessage(r io.Reader) (eventStreamMessage, error) {
	prelude := make([]byte, 12)
	if _, err := io.ReadFull(r, prelude); err != nil {
		return eventStreamMessage{}, err
	}
	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])
	preludeCRC := binary.BigEndian.Uint32(prelude[8:12])
	if got := crc32.ChecksumIEEE(prelude[0:8]); got != preludeCRC {
		return eventStreamMessage{}, fmt.Errorf("bedrock: prelude crc mismatch (got %x want %x)", got, preludeCRC)
	}
	if totalLen < 16 || headersLen > totalLen-16 {
		return eventStreamMessage{}, fmt.Errorf("bedrock: invalid frame lengths total=%d headers=%d", totalLen, headersLen)
	}
	rest := make([]byte, totalLen-12)
	if _, err := io.ReadFull(r, rest); err != nil {
		return eventStreamMessage{}, err
	}
	headers, err := parseEventStreamHeaders(rest[:headersLen])
	if err != nil {
		return eventStreamMessage{}, err
	}
	payload := rest[headersLen : len(rest)-4]
	// (We don't validate the message-CRC; if the connection is busted
	// we'll see a JSON parse error downstream and surface it.)
	return eventStreamMessage{headers: headers, payload: payload}, nil
}

// parseEventStreamHeaders decodes the variable-length header block.
// We only care about string-typed headers (event-type / message-type
// / content-type / exception-type); other types are skipped.
func parseEventStreamHeaders(buf []byte) (map[string]string, error) {
	headers := map[string]string{}
	for len(buf) > 0 {
		if len(buf) < 2 {
			return nil, fmt.Errorf("bedrock: header buffer truncated")
		}
		nameLen := int(buf[0])
		buf = buf[1:]
		if len(buf) < nameLen+1 {
			return nil, fmt.Errorf("bedrock: header buffer truncated at name")
		}
		name := string(buf[:nameLen])
		buf = buf[nameLen:]
		valType := buf[0]
		buf = buf[1:]
		switch valType {
		case 0, 1: // bool true / bool false — no body
			if valType == 0 {
				headers[name] = "true"
			} else {
				headers[name] = "false"
			}
		case 2: // byte
			if len(buf) < 1 {
				return nil, fmt.Errorf("bedrock: byte header truncated")
			}
			buf = buf[1:]
		case 3: // short
			if len(buf) < 2 {
				return nil, fmt.Errorf("bedrock: short header truncated")
			}
			buf = buf[2:]
		case 4: // integer
			if len(buf) < 4 {
				return nil, fmt.Errorf("bedrock: integer header truncated")
			}
			buf = buf[4:]
		case 5: // long
			if len(buf) < 8 {
				return nil, fmt.Errorf("bedrock: long header truncated")
			}
			buf = buf[8:]
		case 6, 7: // byte_array (6) or string (7) — both prefixed by uint16 length
			if len(buf) < 2 {
				return nil, fmt.Errorf("bedrock: string header length truncated")
			}
			vl := int(binary.BigEndian.Uint16(buf[:2]))
			buf = buf[2:]
			if len(buf) < vl {
				return nil, fmt.Errorf("bedrock: string header value truncated")
			}
			if valType == 7 {
				headers[name] = string(buf[:vl])
			}
			buf = buf[vl:]
		case 8: // timestamp (i64)
			if len(buf) < 8 {
				return nil, fmt.Errorf("bedrock: timestamp header truncated")
			}
			buf = buf[8:]
		case 9: // uuid (16 bytes)
			if len(buf) < 16 {
				return nil, fmt.Errorf("bedrock: uuid header truncated")
			}
			buf = buf[16:]
		default:
			return nil, fmt.Errorf("bedrock: unknown header value type %d", valType)
		}
	}
	return headers, nil
}
