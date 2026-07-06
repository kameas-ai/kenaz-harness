// Package sites — stdio.go
//
// Run is the stdio JSON-RPC loop for the fleet-sites MCP server.
// It implements newline-delimited JSON-RPC 2.0 over stdin/stdout
// with no Wails, no SQLite, and no core.New().
//
// The loop handles: initialize, tools/list, tools/call, prompts/list,
// prompts/get, ping. Notifications (initialized) are sent after
// a successful initialize. Unknown methods return -32601 Method not found.
//
// Mission: sites-mcp-server-01NSITE05 WP02.
package sites

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/mcp/builtin/toolserver"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
)

// Run starts the stdio JSON-RPC server. It blocks until stdin is closed
// or ctx is cancelled. It writes all RPC responses to stdout.
//
// dataDir is used for capability cache reads and recents recording.
// fc is the fleet client (may be a nop client when fleet is disabled).
func Run(ctx context.Context, dataDir string, fc *fleet.Client) error {
	srv := toolserver.NewServer(ServerName, ServerVersion)
	RegisterAll(srv, fc, dataDir)
	RegisterPrompts(srv)

	enc := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	// Increase scanner buffer for large payloads.
	const maxScannerBuf = 16 * 1024 * 1024 // 16 MiB
	scanner.Buffer(make([]byte, 64*1024), maxScannerBuf)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("sites mcp: stdin scan: %w", err)
			}
			return nil // EOF
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var raw transport.RawMessage
		if err := json.Unmarshal(line, &raw); err != nil {
			writeError(enc, nil, transport.ErrCodeParseError, "parse error: "+err.Error())
			continue
		}

		// Notifications have no id — acknowledge and continue.
		if raw.IsNotification() {
			// notifications/initialized et al — no response needed.
			continue
		}

		// Dispatch to server.
		env := transport.RequestEnvelope{
			JSONRPC: raw.JSONRPC,
			Method:  raw.Method,
		}
		if raw.ID != nil {
			// Extract numeric id from the raw JSON id.
			var id int64
			if err := json.Unmarshal(*raw.ID, &id); err != nil {
				// Non-numeric id — treat as 0.
				id = 0
			}
			env.ID = id
		}
		if len(raw.Params) > 0 {
			env.Params = json.RawMessage(raw.Params)
		} else {
			env.Params = json.RawMessage(nil)
		}

		result, rpcErr := srv.HandleEnvelope(ctx, env)

		idBytes := mustMarshalID(raw.ID)
		if rpcErr != nil {
			resp := map[string]any{
				"jsonrpc": transport.JSONRPCVersion,
				"id":      json.RawMessage(idBytes),
				"error": map[string]any{
					"code":    rpcErr.Code,
					"message": rpcErr.Message,
				},
			}
			if err := enc.Encode(resp); err != nil {
				log.Printf("sites mcp: encode error response: %v", err)
			}
			continue
		}

		// Success response.
		resp := map[string]any{
			"jsonrpc": transport.JSONRPCVersion,
			"id":      json.RawMessage(idBytes),
			"result":  result,
		}
		if err := enc.Encode(resp); err != nil {
			log.Printf("sites mcp: encode response: %v", err)
		}

		// After a successful initialize, send the initialized notification.
		if raw.Method == transport.MethodInitialize {
			notif := map[string]any{
				"jsonrpc": transport.JSONRPCVersion,
				"method":  transport.NotificationInitialized,
			}
			if err := enc.Encode(notif); err != nil {
				log.Printf("sites mcp: encode initialized notification: %v", err)
			}
		}
	}
}

// writeError writes a JSON-RPC error response to w.
func writeError(enc *json.Encoder, id *json.RawMessage, code int, msg string) {
	idBytes := mustMarshalID(id)
	resp := map[string]any{
		"jsonrpc": transport.JSONRPCVersion,
		"id":      json.RawMessage(idBytes),
		"error": map[string]any{
			"code":    code,
			"message": msg,
		},
	}
	_ = enc.Encode(resp)
}

// mustMarshalID marshals the raw JSON id or returns null.
func mustMarshalID(id *json.RawMessage) []byte {
	if id == nil {
		return []byte("null")
	}
	return []byte(*id)
}

// Ensure io is used (scanner reads from io.Reader pattern).
var _ io.Reader = os.Stdin
