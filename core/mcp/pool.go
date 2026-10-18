package mcp

import (
	"context"
	"encoding/json"
)

type ServerSpec struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Command   []string `json:"command,omitempty"`
	URL       string   `json:"url,omitempty"`
	// PostURL is the client→server endpoint for SSE recipes. Only
	// populated when Transport=="sse".
	PostURL string            `json:"post_url,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// IsolateEnv, when true, spawns stdio children with an EXPLICIT
	// environment: a minimal base (PATH, HOME, …) plus Env — nothing
	// inherited from the harness process. Served-mode connectors set it
	// (spec 091 D6): the served process env carries every whitelisted
	// connector's namespaced credentials, and a child must never see a
	// sibling's secret. Host mode leaves it false (full inherit,
	// unchanged behaviour).
	IsolateEnv bool `json:"isolate_env,omitempty"`
	// HeadersTemplate carries static HTTP headers for the http and sse
	// transports. Values may contain ${ENV_VAR} tokens that the factory
	// substitutes from Env at connection-open time. The Authorization
	// header is redacted from diagnostic logs.
	HeadersTemplate map[string]string `json:"headers_template,omitempty"`
	// RequestTimeoutMs is the per-POST timeout for the http transport,
	// in milliseconds. 0 → DefaultRequestTimeout (30 s).
	RequestTimeoutMs int `json:"request_timeout_ms,omitempty"`
	// InitTimeoutMs overrides the pool-wide post-spawn initialize
	// deadline for this one server, in milliseconds. 0 → the pool's
	// configured default (transport.DefaultInitTimeout when that is
	// also unset). Populated from recipes.Recipe.InitTimeoutMs
	// (connector-lifecycle-truth-01PMZ303 UNIT-12) — before this, every
	// recipe's declared init_timeout_ms was discarded and every stdio
	// server got the same process-wide 5s deadline regardless of what
	// its own catalog entry declared.
	InitTimeoutMs int `json:"init_timeout_ms,omitempty"`
	// PingPeriodMs overrides the pool-wide health-ping cadence for this
	// one server, in milliseconds. 0 → the pool's configured default.
	// Populated from recipes.Recipe.PingPeriodMs.
	PingPeriodMs int `json:"ping_period_ms,omitempty"`
	// On401, when set, is called synchronously the first time the http
	// transport observes a 401 response from this server. Used by
	// served-mode OAuth connectors to invalidate a cached broker token
	// so the next ConnectorToken call re-fetches rather than re-serving
	// a token the upstream just rejected (fleet-enforcement-truth-
	// 01PMZ505 WP14, AC-026). Not JSON-marshaled — a func value would
	// fail json.Marshal if this struct is ever serialized.
	On401 func() `json:"-"`
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
