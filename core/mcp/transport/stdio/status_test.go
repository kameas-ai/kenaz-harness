package stdio

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
)

// TestStatus_LifecycleStates walks one server through every state
// transition the WP02 supervisor can produce and asserts
// RecipeStatus reflects each.
//
// stopped → starting → running covered by Spawn.
// running → restarting → running covered by an external kill.
// running → failed covered by repeated --crash-on-call sessions.
// running → stopped covered by Close.
func TestStatus_LifecycleStates(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)

	inst := newServerInstance("fake", nil, nil, nil, nil, instanceOptions{
		Sleep: func(time.Duration) {},
	})

	// Pre-spawn snapshot — state=stopped, no PID.
	pre := inst.RecipeStatus()
	if pre.State != string(StateStopped) {
		t.Fatalf("pre-spawn state = %q, want stopped", pre.State)
	}
	if pre.PID != 0 {
		t.Fatalf("pre-spawn PID = %d, want 0", pre.PID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	const seedLine = "[fake] hello from stderr"
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:         "fake",
		Command:    []string{bin, "--seed-stderr=" + seedLine},
		PingPeriod: -1,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Post-spawn — state=running, PID > 0, server info populated.
	got := inst.RecipeStatus()
	if got.State != string(StateRunning) {
		t.Fatalf("post-spawn state = %q, want running", got.State)
	}
	if got.PID <= 0 {
		t.Fatalf("post-spawn PID = %d, want > 0", got.PID)
	}
	if got.ProtocolVersion != SupportedProtocolVersion {
		t.Fatalf("ProtocolVersion = %q, want %q", got.ProtocolVersion, SupportedProtocolVersion)
	}
	if got.ServerName != "fake-mcp-server" {
		t.Fatalf("ServerName = %q", got.ServerName)
	}
	if got.ToolCount != 2 {
		t.Fatalf("ToolCount = %d, want 2", got.ToolCount)
	}

	// StderrTail captures the seeded line. The pump runs as a
	// goroutine so allow a tick for it to drain.
	if !waitForStderrTail(inst, seedLine, 2*time.Second) {
		t.Fatalf("StderrTail never contained seed line; got %q", inst.StderrTail(StatusStderrTailBytes))
	}

	// Force a restart cycle by killing the process. We expect to
	// observe restarting at some point, then running again.
	origPID := got.PID
	if err := inst.killProcessForTest(); err != nil {
		t.Fatalf("kill: %v", err)
	}

	waitForRestartCount(t, inst, 1, 5*time.Second)
	post := inst.RecipeStatus()
	if post.State != string(StateRunning) {
		t.Fatalf("post-restart state = %q, want running", post.State)
	}
	if post.PID == origPID || post.PID == 0 {
		t.Fatalf("post-restart PID = %d (orig %d); want fresh", post.PID, origPID)
	}
	if post.RestartAttempts != 1 {
		t.Fatalf("RestartAttempts = %d, want 1", post.RestartAttempts)
	}
	if post.LastRestartAt.IsZero() {
		t.Fatalf("LastRestartAt is zero after restart")
	}

	// Close → stopped.
	if err := inst.Close(context.Background()); err != nil && !isExpectedExit(err) {
		t.Fatalf("Close: %v", err)
	}
	final := inst.RecipeStatus()
	if final.State != string(StateStopped) {
		t.Fatalf("post-close state = %q, want stopped", final.State)
	}
	if final.PID != 0 {
		t.Fatalf("post-close PID = %d, want 0", final.PID)
	}
}

// TestStatus_ExposedThroughPool asserts Pool.RecipeStatus surfaces
// the per-instance snapshot and returns ok=false for unknown ids.
func TestStatus_ExposedThroughPool(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	p := NewPool(PoolOptions{PingPeriod: -1})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Open(ctx, []coremcp.ServerSpec{
		{Name: "alpha", Transport: "stdio", Command: []string{bin}},
	}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer p.Close(context.Background())

	st, ok := p.RecipeStatus("alpha")
	if !ok {
		t.Fatalf("RecipeStatus(alpha): ok=false; want true")
	}
	if st.ID != "alpha" {
		t.Fatalf("ID = %q, want alpha", st.ID)
	}
	if st.State != string(StateRunning) {
		t.Fatalf("State = %q, want running", st.State)
	}
	if st.ToolCount != 2 {
		t.Fatalf("ToolCount = %d, want 2", st.ToolCount)
	}

	if _, ok := p.RecipeStatus("ghost"); ok {
		t.Fatalf("RecipeStatus(ghost): ok=true; want false")
	}
}

// TestStatus_FailedAfterExhaustedRestarts asserts the snapshot
// captures the failed state with three restart attempts recorded
// after the budget has been exhausted.
func TestStatus_FailedAfterExhaustedRestarts(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	inst := newServerInstance("fake", nil, nil, nil, nil, instanceOptions{
		Sleep: func(time.Duration) {},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:         "fake",
		Command:    []string{bin, "--crash-on-call"},
		PingPeriod: -1,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	for i := 0; i < 3; i++ {
		preLen := len(inst.restartHistorySnapshot())
		_, _ = inst.CallTool(ctx, "fake_echo", json.RawMessage(`{}`))
		waitForRestartCount(t, inst, preLen+1, 5*time.Second)
	}
	// Fourth crash exhausts the budget.
	_, _ = inst.CallTool(ctx, "fake_echo", json.RawMessage(`{}`))
	waitForState(t, inst, StateFailed, 5*time.Second)

	st := inst.RecipeStatus()
	if st.State != string(StateFailed) {
		t.Fatalf("State = %q, want failed", st.State)
	}
	if st.RestartAttempts != 3 {
		t.Fatalf("RestartAttempts = %d, want 3", st.RestartAttempts)
	}
	if st.PID != 0 {
		t.Fatalf("failed-state PID = %d, want 0", st.PID)
	}
	// Server info from the last successful initialize is
	// preserved (handy for the UI to keep showing the recipe
	// identity even after failure).
	if st.ServerName != "fake-mcp-server" {
		t.Fatalf("ServerName = %q", st.ServerName)
	}
}

// waitForStderrTail polls until the ring buffer contains want or
// the deadline passes. Drains race with the pump, which writes
// asynchronously.
func waitForStderrTail(inst *ServerInstance, want string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if strings.Contains(inst.StderrTail(StatusStderrTailBytes), want) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
