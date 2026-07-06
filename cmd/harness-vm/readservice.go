// readservice.go adds the Phase G read-only RPC surface to the in-VM harness
// service: sessions / tools / memory / workflows / providers list+get queries
// the kenaz host renders in its IDE-merger views.
//
// Wire contract: contracts/vm-rpc.md §"Phase G — read-only view surfaces".
//
// Design notes:
//
//   - Reuses the in-process rpc.API surface (the same one the harness's own
//     Wails app binds) via rpc.New(core.New(...)). Only READ accessors are
//     called — List / Get / ListMessages. Writes are deferred to a later phase
//     and, in any case, would fail headless (the harness mutation paths emit
//     Wails runtime events that require a lifecycle context the in-VM service
//     does not have).
//
//   - PRIVACY IS LOAD-BEARING. The in-process view types carry fields that must
//     never cross the RPC boundary: memory.Chunk.Content (raw chunk text),
//     llm.Provider.Cred.Locator / .Redaction (credential-adjacent), and
//     unbounded session-message bodies. Each adapter below maps to a narrow
//     wire struct that omits those fields. See the per-adapter comments and
//     contracts/vm-rpc.md §Privacy. Two CI gates enforce this:
//     scripts/ci/check-no-cred-bytes-in-rpc.sh and the unit tests in
//     readservice_test.go.
package main

import (
	"context"
	"unicode/utf8"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/memory"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
	"log/slog"
)

// Per-surface caps. The in-process List methods return unbounded result sets;
// the read RPC layer bounds them so a single response can't blow the NDJSON
// frame or the host's render budget. Pagination cursors are a later-phase
// addition — for v1 the host shows the most-recent N.
const (
	maxListItems        = 200  // sessions / tools / memory / workflows / providers
	maxMessageItems     = 500  // messages in one session
	maxMessageBytes     = 4096 // per-message content cap (UTF-8 bytes)
	maxChunkSummaryByte = 256  // memory chunk summary cap (UTF-8 bytes)
)

// readAPI is the subset of the in-process rpc.API surface the read service
// needs. *rpc.API satisfies it; tests inject a fake. Only read accessors are
// listed — the service never calls a mutation method.
type readAPI interface {
	Sessions() sessions.SessionsAPI
	Tools() tools.ToolsAPI
	Memory() memory.MemoryAPI
	Workflows() workflows.WorkflowsAPI
	LLMConnector() llm.LLMConnectorAPI
}

// readService answers the Phase G read RPCs. A nil api means the in-VM
// bootstrap failed (or was skipped); every read RPC then returns
// code:"unavailable" rather than a transport error, so the host can render a
// clean error state instead of hanging.
type readService struct {
	api readAPI
	log *slog.Logger
}

// newReadService bootstraps the in-process rpc.API against the harness data
// dir and returns a ready read service. It mirrors the harness app's own
// main.go bootstrap (core.New → Start → rpc.New) minus the Wails layer. A
// bootstrap failure is returned to the caller, which logs it and continues
// serving the task surface with reads disabled.
func newReadService(ctx context.Context, dataDir string, log *slog.Logger) (*readService, error) {
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		return nil, err
	}
	if err := c.Start(ctx); err != nil {
		return nil, err
	}
	return &readService{api: rpc.New(c), log: log}, nil
}

// isReadKind reports whether kind names a Phase G read RPC. handleConn uses it
// to route reads to the read service before the task dispatcher's default
// (unknown-kind) branch.
func isReadKind(kind string) bool {
	switch kind {
	case "sessions.list", "sessions.get", "sessions.list_messages",
		"tools.list", "memory.list_chunks", "providers.list", "workflows.list":
		return true
	}
	return false
}

// handle dispatches a read RPC and returns the response message. It is only
// called for kinds where isReadKind is true. req_id is echoed so the host can
// correlate the response with its request on the shared connection.
func (s *readService) handle(ctx context.Context, kind string, in msg) msg {
	reqID, _ := in["req_id"].(string)

	if s == nil || s.api == nil {
		return errResp(kind, reqID, "unavailable", "harness read surface not initialised")
	}

	switch kind {
	case "sessions.list":
		return s.sessionsList(ctx, reqID)
	case "sessions.get":
		id, _ := in["sessionID"].(string)
		return s.sessionsGet(ctx, reqID, id)
	case "sessions.list_messages":
		id, _ := in["sessionID"].(string)
		return s.sessionsListMessages(ctx, reqID, id)
	case "tools.list":
		return s.toolsList(ctx, reqID)
	case "memory.list_chunks":
		return s.memoryListChunks(ctx, reqID)
	case "providers.list":
		return s.providersList(ctx, reqID)
	case "workflows.list":
		return s.workflowsList(ctx, reqID)
	default:
		return errResp(kind, reqID, "bad_request", "not a read kind")
	}
}

// ---- response helpers -------------------------------------------------------

func okResp(kind, reqID string) msg {
	m := msg{"kind": kind + ".ok"}
	if reqID != "" {
		m["req_id"] = reqID
	}
	return m
}

func errResp(kind, reqID, code, message string) msg {
	m := msg{
		"kind":              kind + ".error",
		"code":              code,
		"message_truncated": truncate(message, maxMessageLen),
	}
	if reqID != "" {
		m["req_id"] = reqID
	}
	return m
}

// truncBytes returns s truncated to at most max UTF-8 bytes (never splitting a
// rune) plus a bool reporting whether truncation occurred.
func truncBytes(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	// Walk back to a rune boundary at or below max.
	b := []byte(s)[:max]
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b), true
}

// ---- sessions ---------------------------------------------------------------

// wireSession is the privacy-narrow session shape. The in-process
// sessions.Session already excludes message bodies; we drop the system-prompt
// text and branch internals the host views don't render.
type wireSession struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Kind      string `json:"kind,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
}

func toWireSession(s sessions.Session) wireSession {
	return wireSession{
		ID: s.ID, Name: s.Name, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
		Kind: s.Kind, ProjectID: s.ProjectID,
	}
}

func (s *readService) sessionsList(ctx context.Context, reqID string) msg {
	list, err := s.api.Sessions().List(ctx)
	if err != nil {
		return errResp("sessions.list", reqID, "server_error", err.Error())
	}
	if len(list) > maxListItems {
		list = list[:maxListItems]
	}
	out := make([]wireSession, 0, len(list))
	for _, s := range list {
		out = append(out, toWireSession(s))
	}
	resp := okResp("sessions.list", reqID)
	resp["sessions"] = out
	return resp
}

func (s *readService) sessionsGet(ctx context.Context, reqID, id string) msg {
	if id == "" {
		return errResp("sessions.get", reqID, "bad_request", "sessionID required")
	}
	sess, err := s.api.Sessions().Get(ctx, id)
	if err != nil {
		return errResp("sessions.get", reqID, "server_error", err.Error())
	}
	resp := okResp("sessions.get", reqID)
	resp["session"] = toWireSession(sess)
	return resp
}

// wireMessage truncates message content at maxMessageBytes. Tool-call args are
// NOT forwarded (they can carry secret references); only the structural
// name/id of each tool call is surfaced.
type wireMessage struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
	CreatedAt string `json:"createdAt"`
}

func (s *readService) sessionsListMessages(ctx context.Context, reqID, id string) msg {
	if id == "" {
		return errResp("sessions.list_messages", reqID, "bad_request", "sessionID required")
	}
	msgs, err := s.api.Sessions().ListMessages(ctx, id)
	if err != nil {
		return errResp("sessions.list_messages", reqID, "server_error", err.Error())
	}
	if len(msgs) > maxMessageItems {
		msgs = msgs[len(msgs)-maxMessageItems:] // keep most-recent
	}
	out := make([]wireMessage, 0, len(msgs))
	for _, m := range msgs {
		content, trunc := truncBytes(m.Content, maxMessageBytes)
		out = append(out, wireMessage{
			ID: m.ID, Role: m.Role, Content: content, Truncated: trunc, CreatedAt: m.CreatedAt,
		})
	}
	resp := okResp("sessions.list_messages", reqID)
	resp["messages"] = out
	return resp
}

// ---- tools ------------------------------------------------------------------

// wireTool surfaces the MCP recipe registry: name + enabled-state only. No env
// keys, no config, no credential hints.
type wireTool struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
}

func (s *readService) toolsList(ctx context.Context, reqID string) msg {
	list, err := s.api.Tools().ListRecipes(ctx)
	if err != nil {
		return errResp("tools.list", reqID, "server_error", err.Error())
	}
	if len(list) > maxListItems {
		list = list[:maxListItems]
	}
	out := make([]wireTool, 0, len(list))
	for _, r := range list {
		name := r.Recipe.DisplayName
		if name == "" {
			name = r.Recipe.ID
		}
		out = append(out, wireTool{Name: name, Kind: "mcp", Enabled: r.Enabled})
	}
	resp := okResp("tools.list", reqID)
	resp["tools"] = out
	return resp
}

// ---- memory -----------------------------------------------------------------

// wireChunk is the privacy gate for the memory surface. It NEVER carries
// memory.Chunk.Content (raw chunk text), FilesRead, or FilesModified. The
// summary is the chunk Title bounded to 256 UTF-8 bytes — Title is a generated
// label, not raw user content; we never fall back to Content.
type wireChunk struct {
	ID        string `json:"id"`
	ScopeKind string `json:"scopeKind"`
	ScopeID   string `json:"scopeId,omitempty"`
	Summary   string `json:"summary"`
	CreatedAt string `json:"createdAt"`
	Pinned    bool   `json:"pinned,omitempty"`
}

func (s *readService) memoryListChunks(ctx context.Context, reqID string) msg {
	list, err := s.api.Memory().ListChunks(ctx, memory.ListFilter{})
	if err != nil {
		return errResp("memory.list_chunks", reqID, "server_error", err.Error())
	}
	if len(list) > maxListItems {
		list = list[:maxListItems]
	}
	out := make([]wireChunk, 0, len(list))
	for _, c := range list {
		summary, _ := truncBytes(c.Title, maxChunkSummaryByte)
		out = append(out, wireChunk{
			ID: c.ID, ScopeKind: c.ScopeKind, ScopeID: c.ScopeID,
			Summary: summary, CreatedAt: c.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			Pinned: c.Pinned,
		})
	}
	resp := okResp("memory.list_chunks", reqID)
	resp["chunks"] = out
	return resp
}

// ---- providers --------------------------------------------------------------

// wireProvider is the CREDENTIAL privacy gate. It carries only whether a
// credential is configured (present) and where it lives (sourceTag = the
// credential-reference *kind*, e.g. "keychain" / "aws-profile"). It NEVER
// carries the credential value, the redacted preview, the locator string, or
// the byte length of any of those. See contracts/vm-rpc.md §Privacy.
type wireProvider struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty"`
	Present   bool   `json:"present"`
	SourceTag string `json:"sourceTag,omitempty"`
}

func (s *readService) providersList(ctx context.Context, reqID string) msg {
	list, err := s.api.LLMConnector().ListProviders(ctx)
	if err != nil {
		return errResp("providers.list", reqID, "server_error", err.Error())
	}
	if len(list) > maxListItems {
		list = list[:maxListItems]
	}
	out := make([]wireProvider, 0, len(list))
	for _, p := range list {
		// present = a credential reference exists OR the provider last
		// validated. We read only Cred.Kind (the storage backend label) — never
		// Cred.Locator and never Redaction.
		present := p.Cred.Locator != "" || p.Validated
		out = append(out, wireProvider{
			ID: p.ID, Name: p.Name, Kind: p.Kind,
			Present: present, SourceTag: p.Cred.Kind,
		})
	}
	resp := okResp("providers.list", reqID)
	resp["providers"] = out
	return resp
}

// ---- workflows --------------------------------------------------------------

// wireWorkflow surfaces the workflow registry. The list response carries
// metadata only — no YAML body (which may embed secret refs). A later-phase
// workflows.get with secrets/lint redaction would surface the body.
type wireWorkflow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	StepCount   int    `json:"stepCount"`
	Source      string `json:"source"`
}

func (s *readService) workflowsList(ctx context.Context, reqID string) msg {
	list, err := s.api.Workflows().List(ctx)
	if err != nil {
		return errResp("workflows.list", reqID, "server_error", err.Error())
	}
	if len(list) > maxListItems {
		list = list[:maxListItems]
	}
	out := make([]wireWorkflow, 0, len(list))
	for _, w := range list {
		out = append(out, wireWorkflow{
			ID: w.ID, Name: w.Name, Description: w.Description,
			StepCount: w.StepCount, Source: w.Source,
		})
	}
	resp := okResp("workflows.list", reqID)
	resp["workflows"] = out
	return resp
}
