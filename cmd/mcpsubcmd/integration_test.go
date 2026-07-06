// Package mcpsubcmd — integration_test.go
//
// External-agent smoke test: builds a minimal test binary containing
// the mcpsubcmd.Dispatch entrypoint, spawns it as `<bin> mcp sites`,
// drives the initialize / tools/list protocol, and asserts:
//   - 6 tool names returned
//   - no credential bytes appear in any stdio frame
//
// The test builds a dedicated cmd/mcpsubcmd/testmain/main.go that
// re-exports the Dispatch entrypoint so we don't need a full Wails
// frontend build.
//
// Mission: sites-mcp-server-01NSITE05 WP05.
package mcpsubcmd_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// buildTestBinary compiles a minimal test binary that dispatches to
// mcpsubcmd.Dispatch for `mcp sites`. Returns the path to the binary.
func buildTestBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test: -short flag set")
	}

	// Write a minimal main.go for the test binary.
	src := `package main

import (
	"context"
	"os"
	"github.com/kameas-ai/kenaz-harness/cmd/mcpsubcmd"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "mcp" {
		mcpsubcmd.Dispatch(context.Background(), os.Args[2:])
	}
}
`
	tmpDir := t.TempDir()
	mainFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(mainFile, []byte(src), 0o644); err != nil {
		t.Fatalf("write test main: %v", err)
	}

	binName := "test-harness-mcp"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(tmpDir, binName)

	// Get module root (two levels up from this test file's package).
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	// testFile is .../cmd/mcpsubcmd/integration_test.go
	moduleRoot := filepath.Join(filepath.Dir(testFile), "..", "..")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "build", "-o", binPath, mainFile)
	cmd.Dir = moduleRoot
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build test binary: %v\n%s", err, out)
	}
	return binPath
}

// rpcFrame is the minimal shape of a JSON-RPC frame we send.
type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// TestMCPSitesSubcommand spawns the binary as an MCP server and verifies
// the initialize + tools/list protocol.
func TestMCPSitesSubcommand(t *testing.T) {
	binPath := buildTestBinary(t)
	t.Setenv("HARNESS_FLEET_DISABLED", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "mcp", "sites")
	cmd.Env = append(os.Environ(), "HARNESS_FLEET_DISABLED=1")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start mcp sites: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	enc := json.NewEncoder(stdin)
	scanner := bufio.NewScanner(stdout)

	// Helper: send a request and return the raw response line.
	sendRecv := func(frame rpcFrame) map[string]any {
		t.Helper()
		if err := enc.Encode(frame); err != nil {
			t.Fatalf("encode frame: %v", err)
		}
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				t.Fatalf("scan: %v", err)
			}
			t.Fatal("server closed stdout early")
		}
		line := scanner.Text()
		// Assert no credential bytes in any frame.
		assertNoCredBytes(t, line)
		var result map[string]any
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			t.Fatalf("unmarshal response %q: %v", line, err)
		}
		if e, ok := result["error"]; ok {
			t.Logf("RPC error (may be expected): %v", e)
		}
		return result
	}

	// 1. initialize
	initFrame := rpcFrame{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2024-11-05","clientInfo":{"name":"test","version":"0.0.0"}}`),
	}
	initResp := sendRecv(initFrame)
	if initResp["error"] != nil {
		t.Fatalf("initialize returned error: %v", initResp["error"])
	}
	result, ok := initResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize result not an object: %T", initResp["result"])
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != "fleet-sites" {
		t.Errorf("serverInfo.name = %v, want fleet-sites", info["name"])
	}

	// Consume the notifications/initialized notification.
	if scanner.Scan() {
		line := scanner.Text()
		assertNoCredBytes(t, line)
		// Expect a notification — ignore if it's the initialized notif.
	}

	// 2. tools/list
	toolsFrame := rpcFrame{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}
	toolsResp := sendRecv(toolsFrame)
	if toolsResp["error"] != nil {
		t.Fatalf("tools/list returned error: %v", toolsResp["error"])
	}
	toolsResult, _ := toolsResp["result"].(map[string]any)
	tools, _ := toolsResult["tools"].([]any)
	if len(tools) != 6 {
		t.Errorf("tools count = %d, want 6", len(tools))
	}
	names := make([]string, 0, len(tools))
	for _, td := range tools {
		if m, ok := td.(map[string]any); ok {
			names = append(names, fmt.Sprintf("%v", m["name"]))
		}
	}
	t.Logf("tools: %v", names)

	// 3. Call a gated tool — should return isError:true (not signed in).
	callFrame := rpcFrame{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"sites_deploy","arguments":{"path":"/tmp/nonexistent"}}`),
	}
	callResp := sendRecv(callFrame)
	// tools/call errors surface as isError:true in the result, not as
	// a top-level RPC error.
	callResult, _ := callResp["result"].(map[string]any)
	isError, _ := callResult["isError"].(bool)
	if !isError {
		t.Errorf("expected isError:true for sites_deploy when fleet disabled, got result: %v", callResult)
	}
}

// assertNoCredBytes checks that no raw credential bytes appear in the frame.
// Credential bytes would be 64-char hex strings (JWTs start with eyJ).
func assertNoCredBytes(t *testing.T, line string) {
	t.Helper()
	// JWT tokens always start with "eyJ" (base64 of {"alg"...}).
	if strings.Contains(line, "eyJ") && len(line) > 100 {
		t.Errorf("SECURITY: possible credential bytes (JWT pattern) in stdio frame: %.100s...", line)
	}
}
