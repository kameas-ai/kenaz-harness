package stdio

import (
	"context"
	"encoding/json"
)

// SupportedProtocolVersion is the MCP protocolVersion this client
// advertises during the initialize handshake. Pinned per
// research.md §"protocolVersion and initialize handshake". Bumped
// in a one-line change when the spec advances.
const SupportedProtocolVersion = "2024-11-05"

// ClientName / ClientVersion identify this harness in the
// initialize handshake's clientInfo block. The version string is a
// placeholder until the build system threads through a real one;
// servers treat this as informational.
const (
	ClientName    = "kaneaz-harness"
	ClientVersion = "0.0.0"
)

// JSONRPCVersion is the only JSON-RPC version this transport
// speaks. All envelopes set jsonrpc:"2.0".
const JSONRPCVersion = "2.0"

// JSON-RPC error codes used by the transport. Recipe-author errors
// (validation, env-var resolution) surface through other channels;
// these are the wire-protocol codes.
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// Method names. Centralized so tests and the server reader can
// switch on a single source of truth.
const (
	MethodInitialize         = "initialize"
	MethodPing               = "ping"
	MethodToolsList          = "tools/list"
	MethodToolsCall          = "tools/call"
	MethodResourcesList      = "resources/list"
	MethodResourcesRead      = "resources/read"
	MethodResourcesSubscribe = "resources/subscribe"
	MethodPromptsList        = "prompts/list"
	MethodPromptsGet         = "prompts/get"

	// Server-initiated request methods. The reader dispatches these
	// via PoolOptions handlers (filled in by WP03).
	MethodRootsList             = "roots/list"
	MethodSamplingCreateMessage = "sampling/createMessage"

	// Notifications — id is absent.
	NotificationInitialized          = "notifications/initialized"
	NotificationCancelled            = "notifications/cancelled"
	NotificationProgress             = "notifications/progress"
	NotificationMessage              = "notifications/message"
	NotificationToolsListChanged     = "notifications/tools/list_changed"
	NotificationResourcesListChanged = "notifications/resources/list_changed"
	NotificationResourcesUpdated     = "notifications/resources/updated"
	NotificationPromptsListChanged   = "notifications/prompts/list_changed"
)

// RawMessage is the discriminator-friendly envelope read off the
// wire. ID==nil + Method!="" is a notification; ID!=nil + Method!=""
// is a server-initiated request; ID!=nil + Method=="" is a response
// to one of our outbound requests.
type RawMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

// IsNotification reports whether this envelope carries no id.
func (m RawMessage) IsNotification() bool { return m.ID == nil }

// IsResponse reports whether this envelope is a response (id set,
// method absent).
func (m RawMessage) IsResponse() bool { return m.ID != nil && m.Method == "" }

// IsRequest reports whether this envelope is a request (id and
// method both set). Server-initiated requests look like this.
func (m RawMessage) IsRequest() bool { return m.ID != nil && m.Method != "" }

// RPCError is the JSON-RPC 2.0 error object embedded in failure
// responses.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error makes RPCError satisfy the error interface so it can be
// returned directly from Call().
func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// requestEnvelope is the on-wire shape of an outbound request. The
// JSON-RPC spec treats id as either a string or a number; this
// transport always uses int64.
type requestEnvelope struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// notificationEnvelope is the outbound shape for notifications
// (no id by spec).
type notificationEnvelope struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// responseEnvelope is the outbound shape for replies to
// server-initiated requests.
type responseEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// --- initialize ---

// InitializeParams is the params payload for the client's
// initialize request.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
}

// ClientCapabilities advertises the feature surface the client
// supports. Per research §"Capability advertisement" we always
// declare roots; sampling is gated per recipe and adds itself when
// the server's samplingEnabled toggle is on (WP03 wires this).
type ClientCapabilities struct {
	Roots    *RootsCapability    `json:"roots,omitempty"`
	Sampling *SamplingCapability `json:"sampling,omitempty"`
}

// RootsCapability is the body advertising client-side roots
// support. listChanged is false today — the harness does not push
// root changes to servers in v1.
type RootsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// SamplingCapability is an empty marker per the MCP spec — its
// presence (rather than its content) declares that the client will
// service sampling/createMessage requests.
type SamplingCapability struct{}

// Implementation is the {name, version} block that appears in both
// clientInfo and the server's response.serverInfo.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is the shape of the server's initialize
// response. ServerCapabilities is intentionally a json.RawMessage:
// the spec leaves room for future fields and we don't want a typed
// struct to drop unknown ones silently.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
}

// ServerCapabilities is the server's declared surface. The fields
// we care about for routing are pointers: nil means the server
// didn't advertise the feature (so we skip tools/list etc.).
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
	Logging   *LoggingCapability   `json:"logging,omitempty"`
}

// ToolsCapability mirrors the server-side advertisement.
// listChanged signals whether the server emits
// notifications/tools/list_changed.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability mirrors the server-side advertisement.
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptsCapability mirrors the server-side advertisement.
type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// LoggingCapability is an empty marker per the MCP spec —
// presence means the server emits notifications/message frames.
type LoggingCapability struct{}

// --- tools ---

// ToolDefinition is one entry in a tools/list response. InputSchema
// is opaque JSON forwarded to upstream consumers (the toolloop
// projects this onto its own tool surface).
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ToolsListResult is the result body of tools/list.
type ToolsListResult struct {
	Tools []ToolDefinition `json:"tools"`
}

// ToolsCallParams is the params payload of tools/call.
type ToolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolsCallResult is the result body of tools/call. Content is
// intentionally typed as json.RawMessage because the spec allows
// heterogeneous content blocks (text, image, embedded resources);
// the toolloop forwards the raw envelope to the model.
type ToolsCallResult struct {
	Content []json.RawMessage `json:"content,omitempty"`
	IsError bool              `json:"isError,omitempty"`
}

// --- resources ---

// ResourceDefinition is one entry in a resources/list response.
type ResourceDefinition struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

// ResourcesListResult is the result body of resources/list.
type ResourcesListResult struct {
	Resources []ResourceDefinition `json:"resources"`
}

// ResourcesReadParams is the params payload of resources/read.
type ResourcesReadParams struct {
	URI string `json:"uri"`
}

// ResourceContent is one block of a resources/read response —
// either text or base64-encoded blob content.
type ResourceContent struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// ResourcesReadResult is the result body of resources/read.
type ResourcesReadResult struct {
	Contents []ResourceContent `json:"contents"`
}

// ResourcesSubscribeParams is the params payload of
// resources/subscribe.
type ResourcesSubscribeParams struct {
	URI string `json:"uri"`
}

// --- prompts ---

// PromptDefinition is one entry in a prompts/list response.
type PromptDefinition struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument is one input slot for a prompt.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptsListResult is the result body of prompts/list.
type PromptsListResult struct {
	Prompts []PromptDefinition `json:"prompts"`
}

// PromptsGetParams is the params payload of prompts/get.
type PromptsGetParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// PromptsGetResult is the result body of prompts/get. Messages are
// raw JSON because the spec allows several content shapes.
type PromptsGetResult struct {
	Description string            `json:"description,omitempty"`
	Messages    []json.RawMessage `json:"messages"`
}

// --- ping ---

// PingResult is the (empty) result body of ping.
type PingResult struct{}

// --- server-initiated: roots/list ---

// Root is one filesystem root the client exposes to the server. v1
// always returns at least <DataDir>; project root lands when WP03
// wires the RootsHandler to a real source.
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// RootsListResult is the body returned to the server's roots/list
// request.
type RootsListResult struct {
	Roots []Root `json:"roots"`
}

// --- server-initiated: sampling/createMessage ---

// SamplingMessage is one entry in the sampling messages array.
// Content is left as raw JSON to handle the spec's text-or-image
// content union.
type SamplingMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// SamplingRequest is the params payload of sampling/createMessage,
// translated from the on-wire shape into a stable Go struct that
// WP03's handler bridges onto core/llm.
type SamplingRequest struct {
	Messages         []SamplingMessage `json:"messages"`
	SystemPrompt     string            `json:"systemPrompt,omitempty"`
	ModelPreferences json.RawMessage   `json:"modelPreferences,omitempty"`
	IncludeContext   string            `json:"includeContext,omitempty"`
	Temperature      *float64          `json:"temperature,omitempty"`
	MaxTokens        int               `json:"maxTokens,omitempty"`
	StopSequences    []string          `json:"stopSequences,omitempty"`
	Metadata         json.RawMessage   `json:"metadata,omitempty"`
}

// SamplingResponse is the result body returned to the server.
type SamplingResponse struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Model      string          `json:"model,omitempty"`
	StopReason string          `json:"stopReason,omitempty"`
}

// --- notifications ---

// CancelledParams is the body of notifications/cancelled. RequestID
// matches the id of the in-flight request to cancel.
type CancelledParams struct {
	RequestID json.RawMessage `json:"requestId"`
	Reason    string          `json:"reason,omitempty"`
}

// ProgressParams is the body of notifications/progress. Total is
// optional — when absent, progress is indeterminate.
type ProgressParams struct {
	ProgressToken json.RawMessage `json:"progressToken"`
	Progress      float64         `json:"progress"`
	Total         *float64        `json:"total,omitempty"`
}

// MessageParams is the body of notifications/message — server-side
// log records.
type MessageParams struct {
	Level  string          `json:"level"`
	Logger string          `json:"logger,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// ResourcesUpdatedParams is the body of
// notifications/resources/updated.
type ResourcesUpdatedParams struct {
	URI string `json:"uri"`
}

// --- WP03 handler placeholders ---

// SamplingHandler bridges server-initiated sampling/createMessage
// requests onto the host LLM. Defined here so PoolOptions can
// accept a non-nil handler in WP03; today the pool tolerates a nil
// handler by responding with -32601 (Method not found).
type SamplingHandler interface {
	CreateMessage(ctx context.Context, server string, req SamplingRequest) (SamplingResponse, error)
}

// RootsHandler returns the filesystem roots advertised to the
// server. Populated by WP03; the pool calls it lazily for each
// roots/list request. A nil handler responds with an empty roots
// list.
type RootsHandler interface {
	Roots(ctx context.Context, server string) ([]Root, error)
}

// EventPublisher is the WP03 hook for notifications/progress fan-
// out onto the chassis stream broker. A nil publisher drops
// progress events on the floor; reader notifications still flow
// through the log path.
type EventPublisher interface {
	Publish(topic string, payload any)
}
