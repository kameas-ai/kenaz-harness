// Package main is the in-tree fake MCP server used by stdio
// integration tests. The tests in core/mcp/transport/stdio/ compile
// this program at test setup (via `go build`) and spawn it as a
// child process to drive the real lifecycle code paths.
//
// `go test` excludes testdata/ from the package compilation, so
// this file does not land in the production binary or in the
// stdio package's test binary either — it is built explicitly by
// the test helper `buildFakeServer` and stored under the test
// temp dir.
//
// CLI flags (WP01 + WP02 + WP03):
//
//	--banner             emit a non-JSON banner line on stdout first
//	--slow-init=DUR      delay the initialize response by DUR
//	--crash-on-call      exit(1) after the first tools/call
//	--no-init            never reply to initialize (timeout test)
//	--ignore-pings       accept ping requests but never reply (WP02
//	                     health-pinger test)
//	--seed-stderr=S      write S to stderr immediately on startup so
//	                     RecipeStatus.StderrTail tests have a
//	                     deterministic value to assert
//	--emit-roots-list    after initialize, send a roots/list request
//	                     to the host and exit on response
//	--emit-sampling      after initialize, send sampling/createMessage
//	                     to the host and exit on response
//	--emit-log=LEVEL     emit one notifications/message at LEVEL after
//	                     initialize (debug|info|notice|warning|error|...)
//	--progress-on-call   on tools/call, emit one notifications/progress
//	                     mid-flight before the result
//
// Tools exposed (per WP01 acceptance):
//
//	fake_echo  — returns its arguments verbatim as text content
//	fake_count — returns the integers 0..n-1 as text content
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	banner := flag.Bool("banner", false, "emit a non-JSON banner line on stdout first")
	slowInit := flag.Duration("slow-init", 0, "delay the initialize response by this duration")
	crashOnCall := flag.Bool("crash-on-call", false, "exit(1) after the first tools/call")
	noInit := flag.Bool("no-init", false, "never reply to initialize")
	ignorePings := flag.Bool("ignore-pings", false, "accept ping requests but never reply")
	seedStderr := flag.String("seed-stderr", "", "write to stderr on startup")
	emitRootsList := flag.Bool("emit-roots-list", false, "send a roots/list request to the host after initialize")
	emitSampling := flag.Bool("emit-sampling", false, "send a sampling/createMessage request to the host after initialize")
	emitLog := flag.String("emit-log", "", "emit a notifications/message at the given level after initialize")
	progressOnCall := flag.Bool("progress-on-call", false, "emit a notifications/progress mid-tools/call")
	flag.Parse()
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr, runConfig{
		Banner:         *banner,
		SlowInit:       *slowInit,
		CrashOnCall:    *crashOnCall,
		NoInit:         *noInit,
		IgnorePings:    *ignorePings,
		SeedStderr:     *seedStderr,
		EmitRootsList:  *emitRootsList,
		EmitSampling:   *emitSampling,
		EmitLogLevel:   *emitLog,
		ProgressOnCall: *progressOnCall,
	}))
}

type runConfig struct {
	Banner         bool
	SlowInit       time.Duration
	CrashOnCall    bool
	NoInit         bool
	IgnorePings    bool
	SeedStderr     string
	EmitRootsList  bool
	EmitSampling   bool
	EmitLogLevel   string
	ProgressOnCall bool
}

func run(stdin io.Reader, stdout io.Writer, stderr io.Writer, cfg runConfig) int {
	if cfg.Banner {
		fmt.Fprintln(stdout, "[fake] non-JSON banner line — should be skipped")
	}
	if cfg.SeedStderr != "" {
		fmt.Fprintln(stderr, cfg.SeedStderr)
	}
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	var writeMu sync.Mutex
	write := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return enc.Encode(v)
	}

	// Server-initiated request id sequence — distinct from response
	// ids so the host can route by direction without ambiguity.
	var nextSrvID atomic.Int64
	srvID := func() int64 { return nextSrvID.Add(1) }

	// emitPostInit fires the WP03 server-initiated traffic once
	// initialize has been answered. It runs in its own goroutine so
	// the main reader loop keeps draining inbound responses.
	emitPostInit := func() {
		if cfg.EmitLogLevel != "" {
			_ = write(map[string]any{
				"jsonrpc": "2.0",
				"method":  "notifications/message",
				"params": map[string]any{
					"level":  cfg.EmitLogLevel,
					"logger": "fake.server",
					"data":   map[string]any{"message": "hello from fake server"},
				},
			})
		}
		if cfg.EmitRootsList {
			_ = write(map[string]any{
				"jsonrpc": "2.0",
				"id":      srvID(),
				"method":  "roots/list",
			})
		}
		if cfg.EmitSampling {
			_ = write(map[string]any{
				"jsonrpc": "2.0",
				"id":      srvID(),
				"method":  "sampling/createMessage",
				"params": map[string]any{
					"messages": []map[string]any{
						{
							"role": "user",
							"content": map[string]any{
								"type": "text",
								"text": "ping?",
							},
						},
					},
					"systemPrompt": "you are a fake test fixture",
					"maxTokens":    64,
				},
			})
		}
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			fmt.Fprintln(stderr, "[fake] parse error:", err)
			continue
		}
		var method string
		_ = json.Unmarshal(msg["method"], &method)
		idRaw, hasID := msg["id"]
		switch method {
		case "initialize":
			if cfg.NoInit {
				continue
			}
			if cfg.SlowInit > 0 {
				time.Sleep(cfg.SlowInit)
			}
			_ = write(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(idRaw),
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo": map[string]any{
						"name": "fake-mcp-server", "version": "0.0.1",
					},
				},
			})
		case "notifications/initialized":
			// Trigger any post-init server-initiated traffic now that
			// the handshake is complete. Done in a goroutine so the
			// reader keeps draining the host's inbound stream (which
			// includes responses to the requests we're about to send).
			go emitPostInit()
		case "tools/list":
			_ = write(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(idRaw),
				"result": map[string]any{
					"tools": []map[string]any{
						{
							"name":        "fake_echo",
							"description": "Echo args back",
							"inputSchema": map[string]any{
								"type":       "object",
								"properties": map[string]any{},
							},
						},
						{
							"name":        "fake_count",
							"description": "Return 0..n-1",
							"inputSchema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"n": map[string]any{"type": "integer"},
								},
							},
						},
					},
				},
			})
		case "tools/call":
			if cfg.CrashOnCall {
				fmt.Fprintln(stderr, "[fake] crashing on call as configured")
				return 1
			}
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
				Meta      struct {
					ProgressToken json.RawMessage `json:"progressToken"`
				} `json:"_meta"`
			}
			_ = json.Unmarshal(msg["params"], &params)
			if cfg.ProgressOnCall && len(params.Meta.ProgressToken) > 0 {
				// Mid-flight progress before we fall through to the
				// per-tool result builder below.
				total := 1.0
				_ = write(map[string]any{
					"jsonrpc": "2.0",
					"method":  "notifications/progress",
					"params": map[string]any{
						"progressToken": json.RawMessage(params.Meta.ProgressToken),
						"progress":      0.5,
						"total":         total,
						"message":       "halfway",
					},
				})
			}
			var result map[string]any
			switch params.Name {
			case "fake_echo":
				result = map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": "echo:" + string(params.Arguments)},
					},
				}
			case "fake_count":
				var args struct {
					N int `json:"n"`
				}
				_ = json.Unmarshal(params.Arguments, &args)
				items := make([]int, 0, args.N)
				for i := 0; i < args.N; i++ {
					items = append(items, i)
				}
				itemsB, _ := json.Marshal(items)
				result = map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": string(itemsB)},
					},
				}
			default:
				_ = write(map[string]any{
					"jsonrpc": "2.0",
					"id":      json.RawMessage(idRaw),
					"error": map[string]any{
						"code":    -32601,
						"message": "unknown tool: " + params.Name,
					},
				})
				continue
			}
			_ = write(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(idRaw),
				"result":  result,
			})
		case "ping":
			if cfg.IgnorePings {
				continue
			}
			_ = write(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(idRaw),
				"result":  map[string]any{},
			})
		default:
			if hasID {
				_ = write(map[string]any{
					"jsonrpc": "2.0",
					"id":      json.RawMessage(idRaw),
					"error": map[string]any{
						"code":    -32601,
						"message": "method not found: " + method,
					},
				})
			}
		}
	}
	return 0
}
