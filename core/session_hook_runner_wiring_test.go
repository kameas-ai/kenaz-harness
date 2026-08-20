package core_test

// trust-surfaces-that-fire-01PMZ202 WP10 / UNIT-9, AC-07 (session leg).
//
// Proves SetSessionHookRunner, called before the first SessionManager()
// call (as core/rpc/api.go does), actually reaches session.Manager.Create
// and fires session_start through a REAL hooks.Runner + hooks.Registry +
// saved builtin hook — against a real sqlite-backed session store, not
// session.NewMemoryStore(). CLAUDE.md's persistence-integrity doctrine
// requires this: "Anything asserting persistence... must drive real
// sqlite," and the mission's own WP10 body says the same thing verbatim
// ("session_start... fire on a real session open... against real
// sqlite").
//
// Mutation: comment out the `c.SetSessionHookRunner(...)` call in
// core/rpc/api.go's New(). This test does not exercise that exact call
// site (constructing a full HarnessAPI is out of proportion for this
// test), but it does exercise the SAME mechanism SetSessionHookRunner
// depends on: confirmed by hand — commenting out the equivalent call in
// this test body (skipping SetSessionHookRunner entirely) makes the
// firedCh assertion below time out instead of receiving.

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/hooks"
)

func TestCore_SetSessionHookRunner_FiresSessionStartOnRealSQLite(t *testing.T) {
	t.Parallel()

	// firedCh is the spy: the builtin sends the session id it observed,
	// so the assertion below proves Manager.Create() itself invoked the
	// hook (not that the runner/registry combo works in isolation —
	// Manager.Create discards FireSessionStart's return value, so a
	// side-effecting builtin is the only way to observe it from outside).
	firedCh := make(chan string, 1)
	builtins := hooks.NewBuiltinRegistry()
	builtins.RegisterGenericFire("wp10-record-session-start",
		func(_ context.Context, event string, payload any, _ map[string]any) (hooks.HookOutput, error) {
			if ev, ok := payload.(hooks.SessionStartEvent); ok {
				firedCh <- ev.SessionID
			} else {
				firedCh <- ""
			}
			return hooks.HookOutput{}, nil
		},
		hooks.BuiltinDescriptor{ID: "wp10-record-session-start", Name: "wp10-record-session-start", Events: []string{hooks.EventSessionStart}},
	)
	registry, err := hooks.NewRegistry("")
	if err != nil {
		t.Fatalf("hooks.NewRegistry: %v", err)
	}
	if err := registry.Add(hooks.Hook{
		ID: "h-session-start", Name: "record", Event: hooks.EventSessionStart,
		Kind: hooks.KindBuiltin, Enabled: true, Builtin: "wp10-record-session-start",
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	runner := hooks.NewRunner(hooks.Config{Registry: registry, Builtins: builtins})

	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}

	// The load-bearing ordering: SetSessionHookRunner MUST run before the
	// first SessionManager() call, matching core/rpc/api.go's sequencing
	// (SetSessionHookRunner is called immediately after a.hookRunner is
	// set, well before the first c.SessionManager() call later in New()).
	c.SetSessionHookRunner(&hooks.SessionRunnerAdapter{Runner: runner})

	mgr := c.SessionManager()
	if mgr == nil {
		t.Fatal("SessionManager() returned nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rec, err := mgr.Create(ctx, "wp10 test session")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.ID == "" {
		t.Fatal("Create returned a session with no ID — real sqlite path did not run")
	}

	select {
	case got := <-firedCh:
		if got != rec.ID {
			t.Errorf("session_start fired with session_id = %q, want %q", got, rec.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session_start never fired within 2s — Manager.Create did not reach the wired SessionRunnerAdapter")
	}
}

// TestCore_SetSessionHookRunner_NoEffectAfterManagerConstructed pins the
// "too late" guard: calling SetSessionHookRunner after SessionManager()
// has already been constructed must not panic, replace the manager, or
// silently rebuild a second instance — it is a documented no-op.
func TestCore_SetSessionHookRunner_NoEffectAfterManagerConstructed(t *testing.T) {
	t.Parallel()

	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	first := c.SessionManager()
	if first == nil {
		t.Fatal("SessionManager() returned nil")
	}

	runner := hooks.NewRunner(hooks.Config{Registry: mustEmptyRegistry(t)})
	c.SetSessionHookRunner(&hooks.SessionRunnerAdapter{Runner: runner})

	second := c.SessionManager()
	if first != second {
		t.Fatal("SessionManager() returned a different instance after a too-late SetSessionHookRunner call — SessionManager must stay a singleton")
	}
}

func mustEmptyRegistry(t *testing.T) *hooks.Registry {
	t.Helper()
	r, err := hooks.NewRegistry("")
	if err != nil {
		t.Fatalf("hooks.NewRegistry: %v", err)
	}
	return r
}
