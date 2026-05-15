package openaiwire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/sigil-tech/kaneaz-harness/core/llm"
)

// SSEFrame is the decoded top-level structure of one OpenAI SSE data
// payload.
type SSEFrame struct {
	Choices []SSEChoice `json:"choices"`
	Usage   *SSEUsage   `json:"usage"`
}

// SSEChoice is one entry in the choices array of an SSE frame.
type SSEChoice struct {
	Index        int       `json:"index"`
	Delta        SSEDelta  `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
}

// SSEDelta is the delta sub-object within a choice.
type SSEDelta struct {
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	ToolCalls []SSEToolCallDelta `json:"tool_calls"`
}

// SSEToolCallDelta is one streamed tool-call delta entry.
type SSEToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// SSEUsage carries the usage frame that OpenAI emits immediately before
// [DONE] when stream_options.include_usage=true.
type SSEUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ParseFrame decodes one raw SSE data payload into an SSEFrame.
// Returns (nil, nil) for the "[DONE]" sentinel.
func ParseFrame(raw []byte) (*SSEFrame, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	var f SSEFrame
	if err := json.Unmarshal(trimmed, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// ScanSSE reads SSE lines from r, calling onFrame for every non-empty
// data payload. It returns when the stream ends, when "[DONE]" is
// received, or when the context is done.
//
// onFrame receives the raw payload bytes (excluding the "data: " prefix
// and the "[DONE]" sentinel). The function should return false to stop
// scanning.
//
// Returns the first error from the scanner (context cancellation or
// I/O error), or nil on clean termination.
func ScanSSE(ctx context.Context, r io.Reader, onFrame func(raw []byte) bool) error {
	scanner := bufio.NewScanner(r)
	// 1 MiB max line — headroom for large JSON-mode responses.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		default:
		}
		line := scanner.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			// Comment / keepalive (": OPENAI PROCESSING").
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			return nil
		}
		if !onFrame([]byte(payload)) {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return &llm.ErrCancelled{Reason: "context"}
		}
		return &llm.ErrTransient{Message: "openaiwire: stream read: " + err.Error(), Cause: err}
	}
	return nil
}
