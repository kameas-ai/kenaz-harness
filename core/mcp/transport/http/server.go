package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
)

// Server drives the MCP JSON-RPC exchange over an HTTP Connection.
// It handles initialize, tools/list, and tools/call. It is the
// HTTP-transport equivalent of the stdio ServerInstance — a thin
// wrapper that holds a Connection and exposes the same Tools /
// CallTool / Close surface the pool relies on.
//
// Unlike the stdio ServerInstance, Server does not supervise
// restarts (HTTP connections are stateless per-request; retries are
// left to a future health-probe layer added in WP04). The Closed-
// connection path surfaces as errors on CallTool.
type Server struct {
	id     string
	conn   transport.Connection
	logger *slog.Logger

	mu          sync.RWMutex
	tools       []coremcp.Tool
	negotiated  transport.InitializeResult
	initialized bool

	nextID atomic.Int64
}

// NewServer builds an unopened Server backed by conn. Call Open to
// perform the initialize handshake.
func NewServer(id string, conn transport.Connection, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		id:     id,
		conn:   conn,
		logger: logger.With("mcp.recipe", id),
	}
}

// Open calls conn.Open and drives the MCP initialize handshake.
// After Open returns nil, Tools and CallTool are ready.
func (s *Server) Open(ctx context.Context) error {
	if err := s.conn.Open(ctx); err != nil {
		return err
	}
	return s.initialize(ctx)
}

// initialize sends the MCP initialize request and waits for the response.
func (s *Server) initialize(ctx context.Context) error {
	id := s.nextID.Add(1)
	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      id,
		Method:  transport.MethodInitialize,
		Params: transport.InitializeParams{
			ProtocolVersion: transport.SupportedProtocolVersion,
			Capabilities: transport.ClientCapabilities{
				Roots: &transport.RootsCapability{ListChanged: false},
			},
			ClientInfo: transport.Implementation{
				Name:    transport.ClientName,
				Version: transport.ClientVersion,
			},
		},
	}
	if err := s.conn.Send(req); err != nil {
		return fmt.Errorf("http: send initialize: %w", err)
	}
	msg, err := s.conn.Recv()
	if err != nil {
		return fmt.Errorf("http: recv initialize: %w", err)
	}
	if msg.Error != nil {
		return fmt.Errorf("http: initialize error: %s", msg.Error.Message)
	}
	var result transport.InitializeResult
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return fmt.Errorf("http: decode initialize result: %w", err)
	}
	// Send notifications/initialized.
	notif := transport.NotificationEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		Method:  transport.NotificationInitialized,
	}
	if err := s.conn.Send(notif); err != nil {
		return fmt.Errorf("http: send initialized notification: %w", err)
	}

	s.mu.Lock()
	s.negotiated = result
	s.initialized = true
	s.mu.Unlock()

	// Eagerly fetch tools/list when the server advertises tools.
	if result.Capabilities.Tools != nil {
		if err := s.refreshTools(ctx); err != nil {
			s.logger.Warn("http.initial_tools_list", "err", err.Error())
		}
	}
	return nil
}

// refreshTools issues tools/list and caches the result.
func (s *Server) refreshTools(ctx context.Context) error {
	id := s.nextID.Add(1)
	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      id,
		Method:  transport.MethodToolsList,
	}
	if err := s.conn.Send(req); err != nil {
		return fmt.Errorf("http: send tools/list: %w", err)
	}
	msg, err := s.conn.Recv()
	if err != nil {
		return fmt.Errorf("http: recv tools/list: %w", err)
	}
	if msg.Error != nil {
		return fmt.Errorf("http: tools/list error: %s", msg.Error.Message)
	}
	var result transport.ToolsListResult
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return fmt.Errorf("http: decode tools/list: %w", err)
	}
	tools := make([]coremcp.Tool, 0, len(result.Tools))
	for _, t := range result.Tools {
		tools = append(tools, coremcp.Tool{
			Server:      s.id,
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	s.mu.Lock()
	s.tools = tools
	s.mu.Unlock()
	return nil
}

// Tools returns the cached tool list. Returns nil before Open completes.
func (s *Server) Tools() []coremcp.Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]coremcp.Tool, len(s.tools))
	copy(out, s.tools)
	return out
}

// CallTool issues tools/call and returns the raw result envelope.
func (s *Server) CallTool(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, error) {
	s.mu.RLock()
	initialized := s.initialized
	s.mu.RUnlock()
	if !initialized {
		return nil, errors.New("http: server not initialized")
	}
	id := s.nextID.Add(1)
	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      id,
		Method:  transport.MethodToolsCall,
		Params: transport.ToolsCallParams{
			Name:      tool,
			Arguments: args,
		},
	}
	if err := s.conn.Send(req); err != nil {
		return nil, fmt.Errorf("http: send tools/call: %w", err)
	}
	// Use a goroutine + select so ctx cancellation propagates within
	// the connection's request timeout.
	type result struct {
		msg transport.RawMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		msg, err := s.conn.Recv()
		ch <- result{msg: msg, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("http: recv tools/call: %w", r.err)
		}
		if r.msg.Error != nil {
			return nil, r.msg.Error
		}
		return r.msg.Result, nil
	}
}

// Close shuts the underlying connection down.
func (s *Server) Close() error {
	return s.conn.Close()
}

// Negotiated returns the server's initialize result.
func (s *Server) Negotiated() transport.InitializeResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.negotiated
}
