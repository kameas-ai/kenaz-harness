package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ProbeRequest carries the parameters for a capability probe.
type ProbeRequest struct {
	// BaseURL is the endpoint's base URL (e.g. "https://api.groq.com/openai/v1").
	BaseURL string
	// Model is the model to use for probe requests.
	Model string
	// AuthScheme is the credential scheme.
	AuthScheme AuthScheme
	// AuthHeader is the header name for api-key-header/custom schemes.
	AuthHeader string
	// Cred is the resolved credential bytes. May be nil for auth_scheme:none.
	Cred []byte
}

// ProbeResult reports the outcome of a full probe sequence.
type ProbeResult struct {
	// Matrix is the probed capability matrix. Always non-nil, even on
	// partial failure (failed steps are set to "unknown").
	Matrix *CapabilityMatrix
	// Err is non-nil when the probe failed entirely (e.g. auth failure on step 1).
	Err error
}

// Prober executes the three-step capability probe protocol:
//
//  1. Auth check: POST /chat/completions with a minimal prompt. Auth failure → abort.
//  2. Tool-calling check: POST /chat/completions with a tool definition.
//  3. Streaming-usage check: inspect the last stream frame for a usage object.
//
// The probe returns within 15s total. Each step has a 5s timeout. On
// partial failure (step 1 ok, steps 2/3 fail) the matrix is saved with
// "unknown" for the failed fields.
type Prober struct {
	httpc *http.Client
}

// NewProber constructs a Prober with the given HTTP client.
func NewProber(httpc *http.Client) *Prober {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	return &Prober{httpc: httpc}
}

const probeStepTimeout = 5 * time.Second
const probeModel = "gpt-3.5-turbo" // fallback when ProbeRequest.Model is empty

// Probe executes the full three-step probe and returns a ProbeResult.
// The ctx should have a deadline of at most 15s.
func (p *Prober) Probe(ctx context.Context, req ProbeRequest) ProbeResult {
	matrix := NewCapabilityMatrix(req.BaseURL)

	model := req.Model
	if model == "" {
		model = probeModel
	}
	chatURL, err := ChatEndpoint(req.BaseURL)
	if err != nil {
		return ProbeResult{Matrix: matrix, Err: fmt.Errorf("custom-openai probe: %w", err)}
	}

	// Step 1: Auth + streaming check.
	step1Ctx, cancel1 := context.WithTimeout(ctx, probeStepTimeout)
	defer cancel1()

	streaming, streamingUsage, authErr := p.probeStreaming(step1Ctx, chatURL, model, req)
	if authErr != nil {
		// Auth failure → abort; do not save partial matrix.
		return ProbeResult{Matrix: matrix, Err: authErr}
	}
	matrix.Streaming = streaming
	matrix.StreamingUsage = streamingUsage

	// Step 2: Tool-calling check.
	step2Ctx, cancel2 := context.WithTimeout(ctx, probeStepTimeout)
	defer cancel2()

	toolCalling, _ := p.probeToolCalling(step2Ctx, chatURL, model, req)
	matrix.ToolCalling = toolCalling

	// Step 3: (streaming usage already captured in step 1)
	// Update ProbedAt timestamp.
	matrix.ProbedAt = time.Now().Unix()

	return ProbeResult{Matrix: matrix}
}

// probeStreaming sends a minimal streaming request and inspects the SSE
// frames for streaming support and usage frame presence.
func (p *Prober) probeStreaming(ctx context.Context, chatURL, model string, req ProbeRequest) (streaming, streamingUsage CapabilityValue, _ error) {
	body := map[string]any{
		"model":          model,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"max_tokens":     1,
		"messages":       []map[string]any{{"role": "user", "content": "Hi"}},
	}
	data, _ := json.Marshal(body)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, chatURL, bytes.NewReader(data))
	if err != nil {
		return CapabilityValueUnknown, CapabilityValueUnknown, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if len(req.Cred) > 0 {
		if aerr := ApplyAuth(httpReq, req.AuthScheme, req.Cred, req.AuthHeader); aerr != nil {
			return CapabilityValueUnknown, CapabilityValueUnknown, aerr
		}
	}

	resp, err := p.httpc.Do(httpReq)
	if err != nil {
		return CapabilityValueUnknown, CapabilityValueUnknown, fmt.Errorf("custom-openai probe step1: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		msg := extractErrorMessage(body)
		return CapabilityValueUnknown, CapabilityValueUnknown,
			fmt.Errorf("custom-openai probe: auth failed (status=%d): %s", resp.StatusCode, msg)
	}
	if resp.StatusCode != 200 {
		return CapabilityValueFalse, CapabilityValueUnknown, nil
	}

	// Parse SSE frames to detect streaming and usage.
	hasUsage := false
	hasFrames := false
	bodyData, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	for _, line := range strings.Split(string(bodyData), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" || payload == "" {
			continue
		}
		hasFrames = true
		var frame struct {
			Usage *struct{} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &frame); err == nil && frame.Usage != nil {
			hasUsage = true
		}
	}

	if hasFrames {
		streaming = CapabilityValueTrue
	} else {
		streaming = CapabilityValueFalse
	}
	if hasUsage {
		streamingUsage = CapabilityValueTrue
	} else {
		streamingUsage = CapabilityValueFalse
	}
	return streaming, streamingUsage, nil
}

// probeToolCalling sends a request with a minimal tool definition and
// reports whether the endpoint accepted it.
func (p *Prober) probeToolCalling(ctx context.Context, chatURL, model string, req ProbeRequest) (CapabilityValue, error) {
	body := map[string]any{
		"model":      model,
		"max_tokens": 1,
		"messages":   []map[string]any{{"role": "user", "content": "Hi"}},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name":        "probe_tool",
					"description": "Test tool for capability probe",
					"parameters": map[string]any{
						"type":       "object",
						"properties": map[string]any{},
					},
				},
			},
		},
	}
	data, _ := json.Marshal(body)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, chatURL, bytes.NewReader(data))
	if err != nil {
		return CapabilityValueUnknown, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if len(req.Cred) > 0 {
		if aerr := ApplyAuth(httpReq, req.AuthScheme, req.Cred, req.AuthHeader); aerr != nil {
			return CapabilityValueUnknown, aerr
		}
	}

	resp, err := p.httpc.Do(httpReq)
	if err != nil {
		return CapabilityValueUnknown, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == 200:
		return CapabilityValueTrue, nil
	case resp.StatusCode == 400 || resp.StatusCode == 422:
		// Some endpoints return 400 for unsupported features.
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		msg := strings.ToLower(string(respBody))
		if strings.Contains(msg, "tool") || strings.Contains(msg, "function") {
			return CapabilityValueFalse, nil
		}
		return CapabilityValueUnknown, nil
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return CapabilityValueUnknown, nil
	default:
		return CapabilityValueUnknown, nil
	}
}

