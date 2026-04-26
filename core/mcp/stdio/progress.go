package stdio

import (
	"encoding/json"
	"log/slog"
	"sync"
)

// ProgressTopic is the broker topic ProgressForwarder publishes
// onto. Single source of truth so subscribers don't drift from the
// publisher's spelling.
const ProgressTopic = "mcp:progress"

// ProgressEvent is the typed payload published on ProgressTopic for
// every notifications/progress frame the server emits. RequestID is
// the harness-side request id — useful for the chat UI to associate
// progress with the in-flight tools/call envelope. Tool is the
// originating tool name when known (correlated via the request map
// the ServerInstance maintains).
type ProgressEvent struct {
	Server    string   `json:"server"`
	Tool      string   `json:"tool,omitempty"`
	RequestID int64    `json:"requestId"`
	Token     string   `json:"token,omitempty"`
	Progress  float64  `json:"progress"`
	Total     *float64 `json:"total,omitempty"`
	Message   string   `json:"message,omitempty"`
}

// ProgressForwarder transforms server-sent notifications/progress
// frames into ProgressEvent payloads on an EventPublisher. It owns
// the requestID ↔ progressToken correlation table per server.
//
// Best-effort: malformed frames or missing publishers are dropped
// with a debug log; the reader loop must never fail because the
// progress path failed.
type ProgressForwarder struct {
	server  string
	publish EventPublisher
	logger  *slog.Logger

	mu     sync.RWMutex
	tokens map[int64]progressEntry // request id → token + tool
	byTok  map[string]int64        // token → request id (reverse index)
}

// progressEntry carries the per-request bookkeeping needed to
// reconstruct a ProgressEvent on inbound notifications.
type progressEntry struct {
	token string
	tool  string
}

// NewProgressForwarder constructs a forwarder for one server. A nil
// publisher is allowed — the forwarder's Handle path becomes a
// no-op, keeping wiring simple in tests that don't care about
// progress.
func NewProgressForwarder(server string, publisher EventPublisher, logger *slog.Logger) *ProgressForwarder {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProgressForwarder{
		server:  server,
		publish: publisher,
		logger:  logger,
		tokens:  make(map[int64]progressEntry),
		byTok:   make(map[string]int64),
	}
}

// Register associates an in-flight request id with a progress
// token so a later progress notification can be tagged with the
// correct id. Empty token clears any prior association for the id.
// Safe to call concurrently.
func (p *ProgressForwarder) Register(id int64, token, tool string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.tokens[id]; ok {
		delete(p.byTok, existing.token)
	}
	if token == "" {
		delete(p.tokens, id)
		return
	}
	p.tokens[id] = progressEntry{token: token, tool: tool}
	p.byTok[token] = id
}

// Forget drops the request-id ↔ token association. Called when the
// originating request completes (response received or cancelled).
func (p *ProgressForwarder) Forget(id int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.tokens[id]; ok {
		delete(p.byTok, entry.token)
		delete(p.tokens, id)
	}
}

// Handle processes one notifications/progress payload. The progress
// token in the payload is matched against the registry; on a hit
// the matched id and tool name are folded into the published event.
// On a miss the event is still published with RequestID=0 so
// subscribers see best-effort progress for tokens we never saw on
// the request side (server-spontaneous progress).
func (p *ProgressForwarder) Handle(raw json.RawMessage) {
	if p == nil || p.publish == nil {
		return
	}
	if len(raw) == 0 {
		return
	}
	var params ProgressParams
	if err := json.Unmarshal(raw, &params); err != nil {
		p.logger.Debug("stdio.progress.parse_error", "err", err.Error())
		return
	}
	// The MCP spec lets progressToken be a string or a number. Try
	// both shapes; on either failure, fall back to "" so the publish
	// path still emits a usable event.
	token := decodeProgressToken(params.ProgressToken)

	// Pull the optional `message` field — it landed in a later spec
	// revision and ProgressParams doesn't model it natively. Decode
	// from the raw payload defensively so an older/newer server
	// shape doesn't break the path.
	var withMessage struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &withMessage)

	var (
		id   int64
		tool string
	)
	if token != "" {
		p.mu.RLock()
		if rid, ok := p.byTok[token]; ok {
			id = rid
			if entry, ok := p.tokens[rid]; ok {
				tool = entry.tool
			}
		}
		p.mu.RUnlock()
	}

	p.publish.Publish(ProgressTopic, ProgressEvent{
		Server:    p.server,
		Tool:      tool,
		RequestID: id,
		Token:     token,
		Progress:  params.Progress,
		Total:     params.Total,
		Message:   withMessage.Message,
	})
}

// decodeProgressToken extracts a string or numeric token from the
// raw JSON value. Numeric tokens are stringified so the registry
// uses one key shape regardless of the server's choice.
func decodeProgressToken(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Numeric fallback.
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}
