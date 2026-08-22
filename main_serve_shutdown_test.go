package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/rpc"
	"github.com/kameas-ai/kenaz-harness/core/serve"
)

// main_serve_shutdown_test.go — served-mode-is-a-real-mode-01PMZ707 WP08,
// SD-11 / AC-718.
//
// runServeMode() itself is not directly testable: it depends on real env
// vars, paths.DataDir(), and the embedded frontend/dist-served FS. The
// signal wiring it needs was extracted into installServeShutdownSignal
// specifically so this defect class — "the docstring says SIGTERM works,
// nothing wires it" — has a real, running test instead of only a
// source-reading claim.

// TestInstallServeShutdownSignal_SIGTERM_CancelsContext pins the narrow
// unit: a real SIGTERM delivered to this process reaches cancel().
// *Falsify*: comment out the `signal.Notify` call inside
// installServeShutdownSignal → this test hangs until its own timeout
// fires, i.e. FAILS, never passes silently.
func TestInstallServeShutdownSignal_SIGTERM_CancelsContext(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	stop := installServeShutdownSignal(log, "test", cancel)
	defer stop()

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("self-signal SIGTERM: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("SIGTERM did not cancel the context within 3s — installServeShutdownSignal is not wired")
	}
}

// TestServeShutdown_SIGTERM_StopsRealHTTPServer is the AC-718 test proper:
// a REAL serve.Server (the same constructor runServeMode calls), wired
// through installServeShutdownSignal exactly as runServeMode wires it,
// actually stops serving HTTP when this process receives SIGTERM — i.e.
// the http.Server's Shutdown is OBSERVED to run, not merely assumed from
// the context becoming Done.
//
// *Falsify*: skip installServeShutdownSignal (call srv.Serve(ctx) with a
// context nothing ever cancels) → srv.Serve blocks past the timeout below
// and the test FAILS (hangs, not a false green).
func TestServeShutdown_SIGTERM_StopsRealHTTPServer(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	api := rpc.New(nil) // test chassis — no core needed to exercise shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := installServeShutdownSignal(log, "test-serve", cancel)
	defer stop()

	srv := serve.New(api, addr, "tok", nil, log)

	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ctx) }()

	// Wait for the listener to come up before signalling shutdown, or the
	// signal could race Serve's own startup.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, derr := net.Dial("tcp", addr)
		if derr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("self-signal SIGTERM: %v", err)
	}

	select {
	case err := <-serveDone:
		if err != nil && err != context.Canceled {
			t.Fatalf("srv.Serve returned an unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("srv.Serve did not return within 5s of SIGTERM — the served HTTP server did not shut down")
	}

	// The listener must actually be down now, not just the ctx cancelled.
	if c, derr := net.DialTimeout("tcp", addr, 500*time.Millisecond); derr == nil {
		_ = c.Close()
		t.Fatal("server is still accepting connections after SIGTERM-triggered shutdown")
	}
}
