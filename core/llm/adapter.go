package llm

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role    Role          `json:"role"`
	Content []ContentPart `json:"content"`
}

type ContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ToolUse  *ToolUse        `json:"tool_use,omitempty"`
	ToolData json.RawMessage `json:"tool_data,omitempty"`
}

type ToolUse struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type Request struct {
	Model    string         `json:"model"`
	System   string         `json:"system,omitempty"`
	Messages []Message      `json:"messages"`
	Tools    []ToolSpec     `json:"tools,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
}

type StreamEvent struct {
	Kind  string          `json:"kind"`
	Delta string          `json:"delta,omitempty"`
	Tool  *ToolUse        `json:"tool,omitempty"`
	Final json.RawMessage `json:"final,omitempty"`
	Err   string          `json:"err,omitempty"`
}

type Adapter interface {
	Name() string
	Stream(ctx context.Context, req Request) (<-chan StreamEvent, error)
}

type Registry interface {
	For(model string) (Adapter, error)
	Register(a Adapter, models ...string)
}
