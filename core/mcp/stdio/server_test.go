package stdio

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestServer_SpawnInitializeClose(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	inst := newServerInstance("fake", nil, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:      "fake",
		Command: []string{bin},
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	got := inst.Negotiated()
	if got.ProtocolVersion != "2024-11-05" {
		t.Fatalf("protocolVersion = %q", got.ProtocolVersion)
	}
	if got.ServerInfo.Name != "fake-mcp-server" {
		t.Fatalf("serverInfo.name = %q", got.ServerInfo.Name)
	}
	tools := inst.Tools()
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(tools))
	}

	if err := inst.Close(context.Background()); err != nil && !isExpectedExit(err) {
		t.Fatalf("Close: %v", err)
	}
}

func TestServer_BannerLineSkipped(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	inst := newServerInstance("fake", nil, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:      "fake",
		Command: []string{bin, "--banner"},
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	if got := inst.Negotiated().ServerInfo.Name; got != "fake-mcp-server" {
		t.Fatalf("serverInfo.name = %q", got)
	}
}

func TestServer_InitTimeout(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	inst := newServerInstance("fake", nil, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := inst.Spawn(ctx, SpawnSpec{
		ID:          "fake",
		Command:     []string{bin, "--no-init"},
		InitTimeout: 250 * time.Millisecond,
	})
	if err == nil {
		t.Fatalf("Spawn: want timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "initialize timeout") {
		t.Fatalf("err = %v, want initialize timeout", err)
	}
}

func TestServer_FirstByteTimeout(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	inst := newServerInstance("fake", nil, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := inst.Spawn(ctx, SpawnSpec{
		ID:               "fake",
		Command:          []string{bin, "--no-init"},
		FirstByteTimeout: 200 * time.Millisecond,
		// Init timeout is generous so the failure surfaces from the
		// first-byte path, not from the post-write deadline.
		InitTimeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatalf("Spawn: want first-byte error, got nil")
	}
	// Either first-byte or initialize-read can win the race; both
	// are acceptable signals that the deadline triggered.
	if !strings.Contains(err.Error(), "first-byte") && !strings.Contains(err.Error(), "read initialize") && !strings.Contains(err.Error(), "initialize timeout") {
		t.Fatalf("err = %v, want first-byte / read initialize / initialize timeout", err)
	}
}

func TestServer_CallTool(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	inst := newServerInstance("fake", nil, nil, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{ID: "fake", Command: []string{bin}}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	res, err := inst.CallTool(ctx, "fake_echo", json.RawMessage(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(string(res), `"echo:{\"hello\":\"world\"}"`) {
		t.Fatalf("result = %s", string(res))
	}
}

func TestServer_GoroutinesReturnToBaseline(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	baseline := runtime.NumGoroutine()

	inst := newServerInstance("fake", nil, nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{ID: "fake", Command: []string{bin}}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := inst.Close(context.Background()); err != nil && !isExpectedExit(err) {
		t.Fatalf("Close: %v", err)
	}

	// Allow goroutines a brief window to schedule out. NFR-002
	// requires baseline within 100 ms; tolerate ±2 to account for
	// the Go runtime's own scheduler flux.
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		got := runtime.NumGoroutine()
		if got <= baseline+2 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("goroutine count %d exceeds baseline %d after Close", runtime.NumGoroutine(), baseline)
}
