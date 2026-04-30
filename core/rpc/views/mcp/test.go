package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport/stdio"
)

// testTimeout is the hard ceiling for a TestRecipe run. The ctx
// deadline, if shorter, takes precedence.
const testTimeout = 30 * time.Second

// stderrCapBytes is the maximum number of bytes captured from stdio
// stderr and returned in TestResult.StderrTail.
const stderrCapBytes = 4 * 1024

// TestRecipe opens a one-shot Connection for recipe, runs the
// initialize + capability listing handshake, and returns a
// TestResult. The connection is always closed before this method
// returns; the recipe is never registered with the production pool.
//
// Transport routing:
//   - "stdio" (or empty string, which defaults to stdio): spawns a
//     child process via stdio.Connection.
//   - "http" / "sse" / anything else: returns a TestResult{OK: false}
//     with a descriptive ErrorMessage (transports are not yet
//     implemented in this binary).
func (a *API) TestRecipe(ctx context.Context, recipe recipes.Recipe) (TestResult, error) {
	// Impose the hard 30 s ceiling while still respecting a tighter
	// caller-supplied deadline.
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > testTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, testTimeout)
		defer cancel()
	}

	// All shipped recipes use stdio; we dispatch on the spec transport
	// so future http/sse support is a single case addition.
	spec := recipe.ToServerSpec(nil, nil)

	switch spec.Transport {
	case "stdio", "":
		return testStdio(ctx, spec)
	case "http":
		return TestResult{
			OK:           false,
			ErrorMessage: "http transport: not yet implemented",
		}, nil
	case "sse":
		return TestResult{
			OK:           false,
			ErrorMessage: "sse transport: not yet implemented",
		}, nil
	default:
		return TestResult{
			OK:           false,
			ErrorMessage: fmt.Sprintf("unknown transport %q", spec.Transport),
		}, nil
	}
}

// testStdio runs the one-shot test against a stdio recipe.
// spec is the mcp.ServerSpec produced by recipe.ToServerSpec.
func testStdio(ctx context.Context, spec coremcp.ServerSpec) (result TestResult, _ error) {
	start := time.Now()

	spawnSpec := transport.SpawnSpec{
		ID:      spec.Name,
		Command: spec.Command,
		Env:     spec.Env,
	}

	conn := stdio.NewConnection(spawnSpec, nil)
	if err := conn.Open(ctx); err != nil {
		return TestResult{
			OK:           false,
			StderrTail:   conn.StderrTail(stderrCapBytes),
			ErrorMessage: fmt.Sprintf("open: %s", err),
			DurationMs:   time.Since(start).Milliseconds(),
		}, nil
	}
	defer func() {
		_ = conn.Close()
		// Capture stderr after close so the pump has drained.
		result.StderrTail = conn.StderrTail(stderrCapBytes)
		result.DurationMs = time.Since(start).Milliseconds()
	}()

	initResult, err := doInitialize(ctx, conn)
	if err != nil {
		return TestResult{
			OK:           false,
			ErrorMessage: fmt.Sprintf("initialize: %s", err),
		}, nil
	}

	result.OK = true
	result.ServerName = initResult.ServerInfo.Name
	result.ServerVersion = initResult.ServerInfo.Version
	result.ProtocolVersion = initResult.ProtocolVersion

	// Fetch capability lists if the server advertised them.
	if initResult.Capabilities.Tools != nil {
		count, err := listCount(ctx, conn, transport.MethodToolsList, "tools")
		if err != nil {
			result.OK = false
			result.ErrorMessage = fmt.Sprintf("tools/list: %s", err)
			return result, nil
		}
		result.ToolCount = count
	}
	if initResult.Capabilities.Resources != nil {
		count, err := listCount(ctx, conn, transport.MethodResourcesList, "resources")
		if err != nil {
			result.OK = false
			result.ErrorMessage = fmt.Sprintf("resources/list: %s", err)
			return result, nil
		}
		result.ResourceCount = count
	}
	if initResult.Capabilities.Prompts != nil {
		count, err := listCount(ctx, conn, transport.MethodPromptsList, "prompts")
		if err != nil {
			result.OK = false
			result.ErrorMessage = fmt.Sprintf("prompts/list: %s", err)
			return result, nil
		}
		result.PromptCount = count
	}

	return result, nil
}

// doInitialize performs the MCP initialize handshake over conn.
// It sends initialize, reads the response (skipping notifications
// and banners), then sends notifications/initialized.
func doInitialize(ctx context.Context, conn transport.Connection) (transport.InitializeResult, error) {
	const initID int64 = 1

	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      initID,
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
	if err := conn.Send(req); err != nil {
		return transport.InitializeResult{}, fmt.Errorf("send: %w", err)
	}

	// Read responses until we find the one matching our id or the
	// context fires. Skip any notifications the server emits before
	// the handshake completes.
	type readResult struct {
		msg transport.RawMessage
		err error
	}
	resCh := make(chan readResult, 1)
	go func() {
		for {
			msg, err := conn.Recv()
			if err != nil {
				if err == io.EOF {
					select {
					case resCh <- readResult{err: io.EOF}:
					default:
					}
					return
				}
				select {
				case resCh <- readResult{err: err}:
				default:
				}
				return
			}
			// Skip notifications (no id).
			if msg.IsNotification() {
				continue
			}
			// Skip server-initiated requests (not our response).
			if msg.IsRequest() {
				continue
			}
			select {
			case resCh <- readResult{msg: msg}:
			default:
			}
			return
		}
	}()

	var raw transport.RawMessage
	select {
	case <-ctx.Done():
		return transport.InitializeResult{}, ctx.Err()
	case r := <-resCh:
		if r.err != nil {
			return transport.InitializeResult{}, r.err
		}
		raw = r.msg
	}

	if raw.Error != nil {
		return transport.InitializeResult{}, raw.Error
	}
	var result transport.InitializeResult
	if err := json.Unmarshal(raw.Result, &result); err != nil {
		return transport.InitializeResult{}, fmt.Errorf("decode: %w", err)
	}

	// Send notifications/initialized to complete the handshake.
	notif := transport.NotificationEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		Method:  transport.NotificationInitialized,
	}
	if err := conn.Send(notif); err != nil {
		return transport.InitializeResult{}, fmt.Errorf("send initialized: %w", err)
	}

	return result, nil
}

// listCount issues a list request (tools/list, resources/list, or
// prompts/list) and returns the count of items in the named array
// within the result object. Returns 0 on an empty result.
func listCount(ctx context.Context, conn transport.Connection, method, arrayKey string) (int, error) {
	const listID int64 = 2

	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      listID,
		Method:  method,
	}
	if err := conn.Send(req); err != nil {
		return 0, fmt.Errorf("send: %w", err)
	}

	type readResult struct {
		msg transport.RawMessage
		err error
	}
	resCh := make(chan readResult, 1)
	go func() {
		for {
			msg, err := conn.Recv()
			if err != nil {
				select {
				case resCh <- readResult{err: err}:
				default:
				}
				return
			}
			// Skip notifications and server-initiated requests.
			if msg.IsNotification() || msg.IsRequest() {
				continue
			}
			select {
			case resCh <- readResult{msg: msg}:
			default:
			}
			return
		}
	}()

	var raw transport.RawMessage
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case r := <-resCh:
		if r.err != nil {
			return 0, r.err
		}
		raw = r.msg
	}

	if raw.Error != nil {
		return 0, raw.Error
	}

	// Parse the result object as a map and count items in arrayKey.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw.Result, &obj); err != nil {
		return 0, fmt.Errorf("decode result: %w", err)
	}
	arrRaw, ok := obj[arrayKey]
	if !ok {
		return 0, nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(arrRaw, &arr); err != nil {
		return 0, fmt.Errorf("decode %s array: %w", arrayKey, err)
	}
	return len(arr), nil
}
