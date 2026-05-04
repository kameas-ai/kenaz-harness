package harness

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
)

// TestServer_InitializeRoundTrip verifies the in-process transport
// returns a well-formed initialize result.
func TestServer_InitializeRoundTrip(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	tr := NewTransport(srv)
	defer tr.Close()
	ctx := context.Background()

	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      1,
		Method:  transport.MethodInitialize,
		Params: transport.InitializeParams{
			ProtocolVersion: transport.SupportedProtocolVersion,
			ClientInfo: transport.Implementation{
				Name:    "test-client",
				Version: "0.0.0",
			},
		},
	}
	if err := tr.Send(ctx, req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %v", resp.Error)
	}
	body, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got transport.InitializeResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ServerInfo.Name != ServerName {
		t.Errorf("server name = %q, want %q", got.ServerInfo.Name, ServerName)
	}
	if got.Capabilities.Tools == nil {
		t.Errorf("expected tools capability advertised")
	}
}

// TestServer_ToolsListRoundTrip is the WP02 acceptance criterion:
// register a tool, dispatch tools/list through the in-proc pipe, observe
// the tool in the result.
func TestServer_ToolsListRoundTrip(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	srv.Register(ToolSpec{
		Name:        "harness_read_ping",
		Description: "test tool",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	})
	tr := NewTransport(srv)
	defer tr.Close()
	ctx := context.Background()

	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      2,
		Method:  transport.MethodToolsList,
	}
	if err := tr.Send(ctx, req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %v", resp.Error)
	}
	body, _ := json.Marshal(resp.Result)
	var got transport.ToolsListResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "harness_read_ping" {
		t.Fatalf("unexpected tools: %+v", got.Tools)
	}
}

// TestServer_ToolsCall verifies a tool handler gets dispatched and its
// result is wrapped into a content block.
func TestServer_ToolsCall(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	srv.Register(ToolSpec{
		Name: "harness_read_ping",
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]any{"ok": true, "message": "pong"}, nil
		},
	})
	tr := NewTransport(srv)
	defer tr.Close()
	ctx := context.Background()

	args, _ := json.Marshal(transport.ToolsCallParams{Name: "harness_read_ping"})
	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      3,
		Method:  transport.MethodToolsCall,
		Params:  json.RawMessage(args),
	}
	if err := tr.Send(ctx, req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %v", resp.Error)
	}
	body, _ := json.Marshal(resp.Result)
	var got transport.ToolsCallResult
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.IsError {
		t.Fatalf("isError = true: %s", string(got.Content[0]))
	}
	if len(got.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(got.Content))
	}
}

// TestServer_ToolsCall_UnknownTool verifies that unregistered tool
// names return an MCP method-not-found error.
func TestServer_ToolsCall_UnknownTool(t *testing.T) {
	t.Parallel()
	srv := NewServer()
	tr := NewTransport(srv)
	defer tr.Close()
	ctx := context.Background()

	args, _ := json.Marshal(transport.ToolsCallParams{Name: "harness_read_missing"})
	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      4,
		Method:  transport.MethodToolsCall,
		Params:  json.RawMessage(args),
	}
	if err := tr.Send(ctx, req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("expected RPC error for unknown tool")
	}
	if resp.Error.Code != transport.ErrCodeMethodNotFound {
		t.Errorf("error code = %d, want %d", resp.Error.Code, transport.ErrCodeMethodNotFound)
	}
}
