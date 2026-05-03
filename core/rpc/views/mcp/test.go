// Package mcp — one-shot Test Connection helper (WP07).
//
// TestConnection opens a fresh transport.Connection for the given
// ServerSpec, drives the full MCP initialize handshake + capability
// discovery (tools/list, resources/list, prompts/list), captures
// the result, and tears everything down — all within the supplied
// context. It intentionally does NOT register the connection with
// any long-lived pool (per the WP07 hard constraint).
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
	mcptransport "github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport/stdio"
)

// DefaultTestTimeout is the per-call timeout used when no deadline
// is already set on the supplied context. 10s matches the WP07
// spec requirement; the 30s cap from FR-005 applies to the full
// lifecycle call at the MCP view layer.
const DefaultTestTimeout = 10 * time.Second

// stderrTailBytes is the maximum captured stderr tail on failure.
const stderrTailBytes = 4 * 1024

// TestConnection opens a one-shot connection to the server described
// by spec, drives the MCP handshake + discovery calls, and returns
// the result. The connection is always torn down before return,
// regardless of success or failure.
//
// If the context carries no deadline, DefaultTestTimeout is applied.
//
// Only stdio transport is currently implemented; HTTP and SSE will
// be added when WP03/WP04 land their Connection factories.
func TestConnection(ctx context.Context, spec coremcp.ServerSpec) coremcp.TestResult { // privacy-allow: WP07 RPC method, not a Go test
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTestTimeout)
		defer cancel()
	}

	start := time.Now()
	result, err := doTestConnection(ctx, spec)
	result.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		result.OK = false
		result.Error = err.Error()
	}
	return result
}

// doTestConnection is the inner implementation; returns TestResult +
// error so TestConnection can fold duration and error into the struct.
func doTestConnection(ctx context.Context, spec coremcp.ServerSpec) (coremcp.TestResult, error) {
	xport := spec.Transport
	if xport == "" {
		xport = "stdio"
	}

	switch xport {
	case "stdio":
		return testStdio(ctx, spec)
	case "http", "sse":
		// HTTP and SSE transports will be wired when WP03/WP04 land
		// their Connection factories. Return a clear "not yet" error.
		return coremcp.TestResult{}, fmt.Errorf("test connection: transport %q not yet implemented (WP03/WP04)", xport)
	default:
		return coremcp.TestResult{}, fmt.Errorf("test connection: unknown transport %q", xport)
	}
}

// testStdio runs a one-shot connection against a stdio-transport spec.
func testStdio(ctx context.Context, spec coremcp.ServerSpec) (coremcp.TestResult, error) {
	if len(spec.Command) == 0 {
		return coremcp.TestResult{}, errors.New("test connection: stdio spec has empty command")
	}

	spawnSpec := mcptransport.SpawnSpec{
		ID:      spec.Name,
		Command: spec.Command,
		Env:     spec.Env,
	}

	conn := stdio.NewConnection(spawnSpec, nil)
	if err := conn.Open(ctx); err != nil {
		return coremcp.TestResult{
			StderrTail: conn.StderrTail(stderrTailBytes),
		}, fmt.Errorf("test connection: open: %w", err)
	}
	defer func() { _ = conn.Close() }()

	result, err := driveHandshake(ctx, conn)
	if err != nil {
		// Capture stderr tail on failure so the caller can surface it.
		result.StderrTail = conn.StderrTail(stderrTailBytes)
	}
	return result, err
}

// driveHandshake sends initialize → notifications/initialized →
// (tools/list, resources/list, prompts/list based on capabilities)
// over the supplied connection and returns the aggregated TestResult.
//
// It is transport-agnostic: the caller opens and closes the
// connection; driveHandshake only sends/recvs through the
// transport.Connection interface.
func driveHandshake(ctx context.Context, conn mcptransport.Connection) (coremcp.TestResult, error) {
	var nextID atomic.Int64

	// callAndRecv sends a request envelope and waits for the matching
	// response, discarding any notifications/server-requests in between.
	// It respects ctx cancellation by running Recv in a goroutine and
	// racing it against ctx.Done().
	callAndRecv := func(id int64, method string, params any) (json.RawMessage, error) {
		req := mcptransport.RequestEnvelope{
			JSONRPC: mcptransport.JSONRPCVersion,
			ID:      id,
			Method:  method,
			Params:  params,
		}
		if err := conn.Send(req); err != nil {
			return nil, fmt.Errorf("driveHandshake: write %s: %w", method, err)
		}

		type recvResult struct {
			msg mcptransport.RawMessage
			err error
		}

		for {
			ch := make(chan recvResult, 1)
			go func() {
				msg, err := conn.Recv()
				ch <- recvResult{msg, err}
			}()

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case r := <-ch:
				if r.err != nil {
					if errors.Is(r.err, io.EOF) {
						return nil, fmt.Errorf("driveHandshake: EOF waiting for %s response", method)
					}
					return nil, fmt.Errorf("driveHandshake: recv %s: %w", method, r.err)
				}
				msg := r.msg
				if !msg.IsResponse() {
					// Notification or server-initiated request — skip.
					// For server-initiated requests we respond with -32601
					// so the server doesn't stall waiting for us.
					if msg.IsRequest() && msg.ID != nil {
						_ = conn.Send(mcptransport.ResponseEnvelope{
							JSONRPC: mcptransport.JSONRPCVersion,
							ID:      *msg.ID,
							Error: &mcptransport.RPCError{
								Code:    mcptransport.ErrCodeMethodNotFound,
								Message: "not handled in test mode",
							},
						})
					}
					continue
				}
				if !rawIDMatchesInt64(msg.ID, id) {
					continue
				}
				if msg.Error != nil {
					return nil, msg.Error
				}
				return msg.Result, nil
			}
		}
	}

	// ── initialize ──────────────────────────────────────────────────
	initID := nextID.Add(1)
	initResult, err := callAndRecv(initID, mcptransport.MethodInitialize, mcptransport.InitializeParams{
		ProtocolVersion: mcptransport.SupportedProtocolVersion,
		Capabilities: mcptransport.ClientCapabilities{
			Roots: &mcptransport.RootsCapability{ListChanged: false},
		},
		ClientInfo: mcptransport.Implementation{
			Name:    mcptransport.ClientName,
			Version: mcptransport.ClientVersion,
		},
	})
	if err != nil {
		return coremcp.TestResult{}, fmt.Errorf("test connection: initialize: %w", err)
	}

	var initRes mcptransport.InitializeResult
	if err := json.Unmarshal(initResult, &initRes); err != nil {
		return coremcp.TestResult{}, fmt.Errorf("test connection: decode initialize result: %w", err)
	}

	// Send notifications/initialized to complete the handshake.
	if err := conn.Send(mcptransport.NotificationEnvelope{
		JSONRPC: mcptransport.JSONRPCVersion,
		Method:  mcptransport.NotificationInitialized,
	}); err != nil {
		return coremcp.TestResult{}, fmt.Errorf("test connection: write initialized notification: %w", err)
	}

	result := coremcp.TestResult{
		OK:              true,
		ProtocolVersion: initRes.ProtocolVersion,
		ServerInfo:      initRes.ServerInfo,
		Capabilities:    initRes.Capabilities,
		ToolCount:       -1,
		ResourceCount:   -1,
		PromptCount:     -1,
	}

	// ── tools/list ──────────────────────────────────────────────────
	if initRes.Capabilities.Tools != nil {
		id := nextID.Add(1)
		raw, err := callAndRecv(id, mcptransport.MethodToolsList, nil)
		if err != nil {
			return result, fmt.Errorf("test connection: tools/list: %w", err)
		}
		var res mcptransport.ToolsListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return result, fmt.Errorf("test connection: decode tools/list: %w", err)
		}
		result.ToolCount = len(res.Tools)
	}

	// ── resources/list ──────────────────────────────────────────────
	if initRes.Capabilities.Resources != nil {
		id := nextID.Add(1)
		raw, err := callAndRecv(id, mcptransport.MethodResourcesList, nil)
		if err != nil {
			return result, fmt.Errorf("test connection: resources/list: %w", err)
		}
		var res mcptransport.ResourcesListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return result, fmt.Errorf("test connection: decode resources/list: %w", err)
		}
		result.ResourceCount = len(res.Resources)
	}

	// ── prompts/list ────────────────────────────────────────────────
	if initRes.Capabilities.Prompts != nil {
		id := nextID.Add(1)
		raw, err := callAndRecv(id, mcptransport.MethodPromptsList, nil)
		if err != nil {
			return result, fmt.Errorf("test connection: prompts/list: %w", err)
		}
		var res mcptransport.PromptsListResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return result, fmt.Errorf("test connection: decode prompts/list: %w", err)
		}
		result.PromptCount = len(res.Prompts)
	}

	return result, nil
}

// rawIDMatchesInt64 compares a *json.RawMessage id field against an int64.
// Returns false when raw is nil or not a valid int64.
func rawIDMatchesInt64(raw *json.RawMessage, want int64) bool {
	if raw == nil {
		return false
	}
	var got int64
	if err := json.Unmarshal(*raw, &got); err != nil {
		return false
	}
	return got == want
}
