package mcp

import (
	"context"
	"encoding/json"
)

type ServerSpec struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Command   []string          `json:"command,omitempty"`
	URL       string            `json:"url,omitempty"`
	// PostURL is the client→server endpoint for SSE recipes. Only
	// populated when Transport=="sse".
	PostURL string            `json:"post_url,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// HeadersTemplate carries static HTTP headers for the http and sse
	// transports. Values may contain ${ENV_VAR} tokens that the factory
	// substitutes from Env at connection-open time. The Authorization
	// header is redacted from diagnostic logs.
	HeadersTemplate map[string]string `json:"headers_template,omitempty"`
	// RequestTimeoutMs is the per-POST timeout for the http transport,
	// in milliseconds. 0 → DefaultRequestTimeout (30 s).
	RequestTimeoutMs int `json:"request_timeout_ms,omitempty"`
}

type Tool struct {
	Server      string          `json:"server"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type Pool interface {
	Open(ctx context.Context, specs []ServerSpec) error
	Close(ctx context.Context) error
	Tools(ctx context.Context) ([]Tool, error)
	Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error)
}
