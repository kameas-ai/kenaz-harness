// Command harness-vm is the in-VM kenaz-harness stub for Phase 1.
//
// It listens on a TCP port (standing in for a vsock listener — vsock
// framing is Phase 8's concern), accepts a single no-op task RPC, and
// responds with {"status":"ok","stub":true}.
//
// Build with:
//
//	make build-harness-vm        (from kenaz-harness/)
//	go build -o bin/kenaz-harness-vm ./cmd/harness-vm/
//
// Wire contract: see kenaz-harness/contracts/vm-rpc.md for the Phase 1
// draft framing. Phase 8 replaces the stub with a real harness-graph-
// kernel integration over the same wire protocol defined here.
//
// Port: 7881 TCP (or $HARNESS_VM_PORT). Phase 8 upgrades to vsock.
//
// The host orchestrator dispatches a no-op task by sending a single
// JSON line over TCP:
//
//	{"command":"noop"}
//
// The stub responds with:
//
//	{"status":"ok","stub":true}
//
// and then closes the connection. One task per connection for this stub.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// defaultHarnessPort is the TCP port the stub listens on.
const defaultHarnessPort = "7881"

// taskRequest is the wire type for incoming task RPCs.
// Phase 8 will expand this with task-graph metadata.
type taskRequest struct {
	Command string          `json:"command"`
	Args    json.RawMessage `json:"args,omitempty"`
}

// taskResponse is the wire type for outgoing task results.
// The Stub field is a Phase 1 marker; Phase 8 removes it.
type taskResponse struct {
	Status string `json:"status"`
	Stub   bool   `json:"stub,omitempty"`
	Error  string `json:"error,omitempty"`
}

func main() {
	addr := "0.0.0.0:" + defaultHarnessPort
	if p := os.Getenv("HARNESS_VM_PORT"); p != "" {
		addr = "0.0.0.0:" + p
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Error("kenaz-harness-vm: listen failed", "addr", addr, "err", err)
		os.Exit(1)
	}
	log.Info("kenaz-harness-vm stub listening", "addr", addr)

	// Honour SIGTERM / SIGINT for clean systemd stop.
	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigC
		log.Info("kenaz-harness-vm: received signal, shutting down")
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Info("kenaz-harness-vm: accept loop exiting", "reason", err)
			return
		}
		go handleTask(log, conn)
	}
}

func handleTask(log *slog.Logger, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	// Read one newline-delimited JSON task request.
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			log.Warn("kenaz-harness-vm: read task failed", "err", err)
		}
		return
	}

	var req taskRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		log.Warn("kenaz-harness-vm: unmarshal task failed", "err", err, "raw", scanner.Text())
		writeResponse(log, conn, taskResponse{Status: "error", Error: fmt.Sprintf("unmarshal: %v", err)})
		return
	}

	log.Info("kenaz-harness-vm: received task", "command", req.Command)

	// Phase 1 stub: accept any command, return ok.
	writeResponse(log, conn, taskResponse{Status: "ok", Stub: true})
}

func writeResponse(log *slog.Logger, conn net.Conn, resp taskResponse) {
	enc := json.NewEncoder(conn)
	if err := enc.Encode(resp); err != nil {
		log.Warn("kenaz-harness-vm: write response failed", "err", err)
	}
}
