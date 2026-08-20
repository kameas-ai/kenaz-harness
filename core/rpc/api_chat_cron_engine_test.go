package rpc

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
)

// TestChatCronEngine_ConstructedButNotStarted_UntilSetContext is AC-003's
// "first half" (mission model-scheduled-jobs-01PMSJ01 WP03): rpc.New
// constructs the chat-run cron engine over a real DB, but does not arm
// ticks by itself. Asserting this separately matters because an engine
// that is never Start()ed passes every one of ChatCronEngine's own unit
// tests (core/scheduler/chat_cron_engine_test.go) — those construct and
// Start() it explicitly. Only this wiring test can catch "New() forgot to
// call Start() from SetContext".
func TestChatCronEngine_ConstructedButNotStarted_UntilSetContext(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	assertSettingsStoreIsSandboxed(t, api)

	if api.chatCronEngine == nil {
		t.Fatal("New(c) with a real DB did not construct chatCronEngine")
	}
	if api.chatCronEngine.Started() {
		t.Fatal("chatCronEngine reports Started() before SetContext was ever called")
	}

	api.SetContext(context.Background())

	if !api.chatCronEngine.Started() {
		t.Fatal("chatCronEngine.Started() == false after SetContext — the engine was constructed but never armed, which is FR-008's exact regression shape for a build WITH schedules")
	}

	// Shutdown must stop it (mirrors wfScheduler's Start/Stop pairing).
	api.Shutdown()
	if api.chatCronEngine.Started() {
		t.Fatal("chatCronEngine still reports Started() after Shutdown")
	}
}

// TestChatCronEngine_NilCoreDoesNotConstructEngine: the test-chassis path
// (New(nil)) must not construct a chat cron engine — there is no DB to
// read scheduled_chat_runs from, so a constructed-but-storeless engine
// would be a second lie surface.
func TestChatCronEngine_NilCoreDoesNotConstructEngine(t *testing.T) {
	api := New(nil, WithSettingsStore(newTestStore(t)))
	if api.chatCronEngine != nil {
		t.Fatal("New(nil) constructed a chatCronEngine with no DB behind it")
	}
	// scheduledChatAPI must still be usable (graceful-empty surface), and
	// its Engine field must be a true nil interface, not a typed-nil
	// *scheduler.ChatCronEngine wrapped in a non-nil Registrar — that trap
	// is exactly what this construction path guards against in api.go.
	if api.scheduledChatAPI == nil {
		t.Fatal("scheduledChatAPI must still be wired (graceful-empty) when Core is nil")
	}
}
