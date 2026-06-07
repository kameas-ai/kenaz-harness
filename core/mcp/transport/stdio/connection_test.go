package stdio

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
)

// TestConnection_OpenSendRecvCycle drives a Connection through a
// minimal initialize → response round trip and asserts PID and
// StderrTail return real (non-zero) values, satisfying the WP02
// acceptance contract that the stdio Connection surfaces meaningful
// transport.Connection metadata rather than placeholder zeros.
func TestConnection_OpenSendRecvCycle(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	const seed = "[fake] hello-from-connection"
	conn := NewConnection(transport.SpawnSpec{
		ID:      "fake",
		Command: []string{bin, "--seed-stderr=" + seed},
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := conn.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	if pid := conn.PID(); pid <= 0 {
		t.Fatalf("PID() = %d, want > 0", pid)
	}

	// Drive an initialize round trip so we exercise Send + Recv.
	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      1,
		Method:  transport.MethodInitialize,
		Params: transport.InitializeParams{
			ProtocolVersion: transport.SupportedProtocolVersion,
			ClientInfo:      transport.Implementation{Name: "test", Version: "0"},
		},
	}
	if err := conn.Send(req); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The fake server should respond on stdout. Recv may surface
	// IsSkipped'able lines (banners) before the real response —
	// loop until we see one with id == 1 or the deadline fires.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msg, err := conn.Recv()
		if err != nil {
			if transport.IsSkipped(err) {
				continue
			}
			t.Fatalf("Recv: %v", err)
		}
		if msg.IsResponse() && msg.ID != nil {
			break
		}
	}

	// StderrTail must surface the seeded line. Allow a brief
	// drain window for the stderr pump goroutine.
	tailDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(tailDeadline) {
		if strings.Contains(conn.StderrTail(transport.StatusStderrTailBytes), seed) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("StderrTail never contained seed line; got %q", conn.StderrTail(transport.StatusStderrTailBytes))
}

// TestConnection_DoubleOpenRejected asserts the lifecycle contract
// in transport.Connection: Open is one-shot.
func TestConnection_DoubleOpenRejected(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	conn := NewConnection(transport.SpawnSpec{
		ID:      "fake",
		Command: []string{bin},
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Open(ctx); err != nil {
		t.Fatalf("Open#1: %v", err)
	}
	defer conn.Close()
	if err := conn.Open(ctx); err == nil {
		t.Fatalf("Open#2: want error, got nil")
	}
}

// TestConnection_CloseIdempotent asserts the WP02 contract that a
// second Close call returns the first call's outcome rather than
// crashing or hanging.
func TestConnection_CloseIdempotent(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	conn := NewConnection(transport.SpawnSpec{
		ID:      "fake",
		Command: []string{bin},
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Open(ctx); err != nil {
		t.Fatalf("Open: %v", err)
	}
	first := conn.Close()
	second := conn.Close()
	// Allow exit-status / signal-killed shapes through; the test
	// only asserts the second call doesn't deadlock and returns
	// the same error class.
	if (first == nil) != (second == nil) {
		t.Fatalf("Close idempotency violated: first=%v second=%v", first, second)
	}
}

// TestConnection_FactorySatisfiesContract asserts the
// stdio.ConnectionFactory satisfies transport.ConnectionFactory.
// WP02 leaves the factory single-recipe; WP07's Test-Connection
// will instantiate it per call.
func TestConnection_FactorySatisfiesContract(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	factory := &ConnectionFactory{
		Spec: transport.SpawnSpec{Command: []string{bin}},
	}
	c, err := factory.NewConnection("fake")
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}
	if c == nil {
		t.Fatalf("NewConnection returned nil connection")
	}
	// Spec without a command should fail fast with a typed error.
	emptyFactory := &ConnectionFactory{}
	if _, err := emptyFactory.NewConnection("ghost"); err == nil {
		t.Fatalf("empty factory: want error, got nil")
	} else if !strings.Contains(err.Error(), "empty command") {
		t.Fatalf("empty factory err = %v, want contains 'empty command'", err)
	}
}

// TestConnection_SendBeforeOpenRejected asserts the connection
// rejects Send/Recv before Open has been called, so misuse surfaces
// as an explicit error rather than a nil-pointer panic.
func TestConnection_SendBeforeOpenRejected(t *testing.T) {
	t.Parallel()
	conn := NewConnection(transport.SpawnSpec{Command: []string{"/bin/true"}}, nil)
	if err := conn.Send(struct{}{}); err == nil {
		t.Fatalf("Send before Open: want error, got nil")
	}
	if _, err := conn.Recv(); err == nil {
		t.Fatalf("Recv before Open: want error, got nil")
	} else if !errors.Is(err, transport.ErrFrameTooLarge) {
		// We accept io.EOF here too — the framer is nil so Recv
		// should short-circuit. The exact error is implementation-
		// detail; we only require non-nil.
		_ = err
	}
}
