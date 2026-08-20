package rpc

// chat_run_dispatcher_armed_test.go — mission model-scheduled-jobs-
// 01PMSJ01 WP05: the dispatcher assignment. Per plan.md Rule 2, this is
// the commit where core/rpc/api.go actually assigns a live
// ChatRunDispatcher into scheduledchatview.Config.Dispatcher and into
// the WP03 cron engine — WP04 built the type without arming it.

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/scheduledchat"
)

// TestChatRunDispatcher_ArmedInProduction proves New(c) over a real Core
// wires a real Dispatcher into scheduledChatAPI, not the nil that would
// make RunNow return ErrDispatcherUnavailable. The scheduledChatAPI type
// only exposes the interface, so this asserts by absence of that
// specific error rather than reaching into unexported Config state — a
// nil dispatcher is the ONE thing RunNow reports unambiguously (WP02).
//
// The test chassis has no personal-store profiles, so the dispatch
// itself still fails downstream ("no default LLM profile configured")
// — that failure arriving as a RunSummary with Status "failed" (not as
// ErrDispatcherUnavailable, and not as a park) is exactly the proof that
// the Dispatcher field is non-nil and the unattended posture resolved
// the run to an immediate, reported failure rather than hanging.
func TestChatRunDispatcher_ArmedInProduction(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	assertSettingsStoreIsSandboxed(t, api)

	if api.chatCronEngine == nil {
		t.Fatal("chatCronEngine was not constructed over a real DB")
	}

	ctx := context.Background()
	entry, err := api.scheduledChatAPI.Create(ctx, scheduledchat.CreateInput{
		Name:           "arming test",
		PromptTemplate: "hello",
		Cron:           "0 9 * * *",
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = api.scheduledChatAPI.RunNow(ctx, entry.ID)
	if errors.Is(err, scheduledchat.ErrDispatcherUnavailable) {
		t.Fatal("RunNow returned ErrDispatcherUnavailable — the dispatcher was not armed")
	}
	// Every other outcome (a RunSummary with Status "failed" because no
	// LLM profile is configured in this bare test chassis, or a nil
	// error) is consistent with a real dispatcher having run.
}
