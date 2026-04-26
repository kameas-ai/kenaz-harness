package hooks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistry_AddListUpdateRemove_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r, err := NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	h := Hook{
		ID:      "hk-1",
		Name:    "test pre-send",
		Event:   EventPreSend,
		Kind:    KindBuiltin,
		Enabled: true,
		Builtin: BuiltinMemoryRetrieve,
	}
	if err := r.Add(h); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := r.Add(h); !errors.Is(err, ErrHookExists) {
		t.Fatalf("Add dup: want ErrHookExists, got %v", err)
	}
	got, err := r.Get("hk-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "test pre-send" {
		t.Errorf("Get.Name = %q", got.Name)
	}

	// Reopen the registry — persistence test.
	r2, err := NewRegistry(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if list := r2.List(); len(list) != 1 || list[0].ID != "hk-1" {
		t.Fatalf("after reopen len=%d, list=%+v", len(list), list)
	}

	// Update.
	h.Name = "renamed"
	if err := r2.Update(h); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got2, _ := r2.Get("hk-1")
	if got2.Name != "renamed" {
		t.Errorf("after Update Name=%q", got2.Name)
	}

	// Remove.
	if err := r2.Remove("hk-1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := r2.Get("hk-1"); !errors.Is(err, ErrHookNotFound) {
		t.Fatalf("Get after Remove: want ErrHookNotFound, got %v", err)
	}

	// File mode check.
	path := filepath.Join(dir, "hooks.json")
	// Force a save by adding-then-removing so the file definitely exists.
	if err := r2.Add(Hook{ID: "x", Name: "x", Event: EventPreSend, Kind: KindBuiltin, Builtin: BuiltinMemoryRetrieve, Enabled: true}); err != nil {
		t.Fatalf("Add x: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat hooks.json: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("hooks.json mode = %o, want 600", mode)
	}
}

func TestRegistry_Validate_RejectsBadHooks(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry("")
	cases := []struct {
		name string
		h    Hook
	}{
		{"empty id", Hook{Name: "n", Event: EventPreSend, Kind: KindBuiltin, Builtin: "x"}},
		{"empty name", Hook{ID: "i", Event: EventPreSend, Kind: KindBuiltin, Builtin: "x"}},
		{"unknown event", Hook{ID: "i", Name: "n", Event: "??", Kind: KindBuiltin, Builtin: "x"}},
		{"unknown kind", Hook{ID: "i", Name: "n", Event: EventPreSend, Kind: "??"}},
		{"shell missing command", Hook{ID: "i", Name: "n", Event: EventPreSend, Kind: KindShell}},
		{"mcp missing tool", Hook{ID: "i", Name: "n", Event: EventPreSend, Kind: KindMCP}},
		{"builtin missing id", Hook{ID: "i", Name: "n", Event: EventPreSend, Kind: KindBuiltin}},
	}
	for _, c := range cases {
		if err := r.Add(c.h); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}

func TestRunner_BuiltinPreSendMutatesMessages(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	builtins := NewBuiltinRegistry()
	builtins.RegisterPreSend("test.inject", func(_ context.Context, ev PreSendEvent, _ map[string]any) (PreSendEvent, error) {
		ev.Messages = append([]HookMessage{{Role: "system", Content: "injected"}}, ev.Messages...)
		return ev, nil
	}, BuiltinDescriptor{ID: "test.inject", Name: "test", Events: []string{EventPreSend}})

	if err := reg.Add(Hook{
		ID: "h", Name: "n", Event: EventPreSend, Kind: KindBuiltin,
		Enabled: true, Builtin: "test.inject",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	runner := NewRunner(Config{Registry: reg, Builtins: builtins})
	ev := PreSendEvent{
		SessionID: "s", Model: "m", Kind: "k",
		Messages: []HookMessage{{Role: "user", Content: "hi"}},
	}
	out, err := runner.RunPreSend(context.Background(), ev)
	if err != nil {
		t.Fatalf("RunPreSend: %v", err)
	}
	if len(out.Messages) != 2 || out.Messages[0].Role != "system" {
		t.Fatalf("expected injected system message at index 0, got %+v", out.Messages)
	}
}

func TestRunner_BuiltinPostSendFiresOnce(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	var calls atomic.Int32
	builtins := NewBuiltinRegistry()
	builtins.RegisterPostSend("test.count", func(_ context.Context, ev PostSendEvent, _ map[string]any) error {
		calls.Add(1)
		if ev.AssistantTurn == "" {
			return errors.New("missing assistant turn")
		}
		return nil
	}, BuiltinDescriptor{ID: "test.count", Name: "test", Events: []string{EventPostSend}})

	if err := reg.Add(Hook{
		ID: "h", Name: "n", Event: EventPostSend, Kind: KindBuiltin,
		Enabled: true, Builtin: "test.count",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	runner := NewRunner(Config{Registry: reg, Builtins: builtins})
	runner.RunPostSend(context.Background(), PostSendEvent{
		SessionID: "s", UserTurn: "u", AssistantTurn: "a", FinishReason: "completed",
	})
	if got := calls.Load(); got != 1 {
		t.Fatalf("post-send fired %d times, want 1", got)
	}
}

func TestRunner_DisabledHookSkipped(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	var calls atomic.Int32
	builtins := NewBuiltinRegistry()
	builtins.RegisterPreSend("noisy", func(_ context.Context, ev PreSendEvent, _ map[string]any) (PreSendEvent, error) {
		calls.Add(1)
		return ev, nil
	}, BuiltinDescriptor{ID: "noisy", Name: "noisy", Events: []string{EventPreSend}})

	if err := reg.Add(Hook{
		ID: "h", Name: "n", Event: EventPreSend, Kind: KindBuiltin,
		Enabled: false, Builtin: "noisy",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	runner := NewRunner(Config{Registry: reg, Builtins: builtins})
	if _, err := runner.RunPreSend(context.Background(), PreSendEvent{}); err != nil {
		t.Fatalf("RunPreSend: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("disabled hook fired %d times, want 0", calls.Load())
	}
}

func TestRunner_MatchFiltersByModel(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	var calls atomic.Int32
	builtins := NewBuiltinRegistry()
	builtins.RegisterPreSend("model-only", func(_ context.Context, ev PreSendEvent, _ map[string]any) (PreSendEvent, error) {
		calls.Add(1)
		return ev, nil
	}, BuiltinDescriptor{ID: "model-only", Name: "x", Events: []string{EventPreSend}})

	if err := reg.Add(Hook{
		ID: "h", Name: "n", Event: EventPreSend, Kind: KindBuiltin,
		Enabled: true, Builtin: "model-only",
		Match: HookMatch{Models: []string{"claude-sonnet-4-5"}},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	runner := NewRunner(Config{Registry: reg, Builtins: builtins})

	// Different model — should NOT fire.
	_, _ = runner.RunPreSend(context.Background(), PreSendEvent{Model: "gpt-4o"})
	if calls.Load() != 0 {
		t.Fatalf("hook fired despite mismatched model")
	}
	// Matching model — should fire.
	_, _ = runner.RunPreSend(context.Background(), PreSendEvent{Model: "claude-sonnet-4-5"})
	if calls.Load() != 1 {
		t.Fatalf("hook did not fire on matching model: calls=%d", calls.Load())
	}
}

func TestRunner_ShellHookMutatesMessages(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	// A tiny shell pipeline that reads JSON, swaps in a system message,
	// and re-emits the response shape. Tests assume /bin/sh with jq...
	// but we don't want a hard jq dependency, so use python3 when
	// available; fall back to a hand-rolled echo.
	cmd := `python3 -c '
import json, sys
data = json.load(sys.stdin)
messages = [{"role": "system", "content": "shell-injected"}] + data["input"]["messages"]
print(json.dumps({"continue": True, "messages": messages}))
'`
	if !commandAvailable("python3") {
		t.Skip("python3 not available; skipping shell hook test")
	}
	if err := reg.Add(Hook{
		ID: "h", Name: "shellhook", Event: EventPreSend, Kind: KindShell,
		Enabled: true, Command: cmd,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	runner := NewRunner(Config{Registry: reg})
	ev := PreSendEvent{
		Messages: []HookMessage{{Role: "user", Content: "hello"}},
	}
	out, err := runner.RunPreSend(context.Background(), ev)
	if err != nil {
		t.Fatalf("RunPreSend: %v", err)
	}
	if len(out.Messages) != 2 || out.Messages[0].Content != "shell-injected" {
		t.Fatalf("shell hook did not inject: %+v", out.Messages)
	}
}

func TestRunner_ShellHookTimeout(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	if err := reg.Add(Hook{
		ID: "h", Name: "slow", Event: EventPreSend, Kind: KindShell,
		Enabled: true, Command: "sleep 5",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	runner := NewRunner(Config{Registry: reg})
	ev := PreSendEvent{Messages: []HookMessage{{Role: "user", Content: "x"}}}
	ctx := WithShellTimeout(context.Background(), 200*time.Millisecond)
	start := time.Now()
	out, err := runner.RunPreSend(ctx, ev)
	if err != nil {
		t.Fatalf("RunPreSend: %v", err) // we swallow per-hook errors
	}
	dur := time.Since(start)
	if dur > 2*time.Second {
		t.Fatalf("shell hook ran %v — timeout did not kick in", dur)
	}
	// The slow hook is logged + skipped; messages must equal input.
	if len(out.Messages) != 1 || out.Messages[0].Content != "x" {
		t.Fatalf("messages mutated unexpectedly: %+v", out.Messages)
	}
}

func TestRunner_BuiltinMissingLogged(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry("")
	if err := reg.Add(Hook{
		ID: "h", Name: "n", Event: EventPreSend, Kind: KindBuiltin,
		Enabled: true, Builtin: "nope.does.not.exist",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	runner := NewRunner(Config{Registry: reg})
	out, err := runner.RunPreSend(context.Background(), PreSendEvent{
		Messages: []HookMessage{{Role: "user", Content: "x"}},
	})
	if err != nil {
		t.Fatalf("RunPreSend: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("messages mutated despite missing builtin: %+v", out.Messages)
	}
}

func TestRunner_Builtins_DescribeReturnsRegistered(t *testing.T) {
	t.Parallel()
	builtins := NewBuiltinRegistry()
	builtins.RegisterPreSend("a", func(_ context.Context, ev PreSendEvent, _ map[string]any) (PreSendEvent, error) {
		return ev, nil
	}, BuiltinDescriptor{ID: "a", Name: "a"})
	builtins.RegisterPostSend("b", func(_ context.Context, _ PostSendEvent, _ map[string]any) error {
		return nil
	}, BuiltinDescriptor{ID: "b", Name: "b"})
	r := NewRunner(Config{Builtins: builtins})
	got := r.Builtins()
	if len(got) != 2 {
		t.Fatalf("Describe len=%d, want 2", len(got))
	}
	ids := []string{got[0].ID, got[1].ID}
	if strings.Join(ids, ",") != "a,b" {
		t.Fatalf("ids = %v, want [a b]", ids)
	}
}

// ── small helpers ────────────────────────────────────────────────────

func commandAvailable(name string) bool {
	cmd := commandContext(context.Background(), "command -v "+name)
	return cmd.Run() == nil
}
