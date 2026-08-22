package scheduler_test

// Tests for the Cedar gating wired into the cron-triggered fire path
// (model-scheduled-jobs-01PMSJ01 WP09). Before this WP, fireSync went
// straight from the store read to the dispatcher with NO call to
// cedar.GateScheduledChatExecute at all — the RunNow path
// (core/rpc/views/scheduledchat.API.RunNow) was the ONLY caller of that
// gate, so a schedule that only ever fired on its cron (the common
// case — RunNow is the manual test button) never consulted policy.
// ActionScheduledRunExecute's own doc says it "gates background
// dispatch (both cron-triggered and RunNow paths)"
// (core/policy/cedar/types.go) — that sentence was false for the cron
// half until ChatCronEngineConfig.Cedar was wired.
//
// TestChatCronEngine_CedarDenyAllGate_BlocksCronFire is the general
// proof the gap existed at all: a deny-all gate on a perfectly ordinary
// USER-created row must now block the cron-triggered fire. Before this
// WP's change, this test would have passed the assertion on the wrong
// side — the fire would have gone through regardless of what the gate
// said, because nothing ever called it.
//
// The remaining tests are the B-3 fail-safe (F1) reaching the cron path
// specifically, mirroring core/policy/cedar's own
// GateScheduledChatExecute tests but exercised end-to-end through a
// real cron tick and real sqlite history persistence.

import (
	"context"
	"strings"
	"testing"
	"time"

	cedargo "github.com/cedar-policy/cedar-go"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
)

// fixedGate is a cedar.Gate stub returning a fixed Decision regardless
// of input. Used to prove the cron path now actually calls Evaluate.
type fixedGate struct {
	outcome cedar.Outcome
}

func (g fixedGate) Evaluate(_ context.Context, principal cedargo.EntityUID, action string, resource cedargo.EntityUID, _ map[cedargo.String]cedargo.Value) cedar.Decision {
	return cedar.Decision{
		Outcome:   g.outcome,
		Action:    action,
		Principal: principal.String(),
		Resource:  resource.String(),
		Reason:    "fixedGate: test stub",
	}
}

var _ cedar.Gate = fixedGate{}

func mustCreateRowWithProvenance(t *testing.T, store scheduler.ScheduledChatStore, id, cronExpr, createdBy string, toolAllowlist []string) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.Create(context.Background(), scheduler.ChatRunRecord{
		ID:             id,
		Name:           "test-" + id,
		PromptTemplate: "Hello {{date}}",
		Cron:           cronExpr,
		OutputSink:     "none",
		Enabled:        true,
		CreatedAt:      now,
		UpdatedAt:      now,
		CreatedBy:      createdBy,
		ToolAllowlist:  toolAllowlist,
	}); err != nil {
		t.Fatalf("seed row %s: %v", id, err)
	}
}

// waitForHistoryStatus polls store.History until a row exists, then
// returns its status/error. Mirrors TestChatCronEngine_RegisterAndFire's
// polling loop.
func waitForHistoryStatus(t *testing.T, store scheduler.ScheduledChatStore, id string) (status, errStr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		hist, err := store.History(context.Background(), id, 10)
		if err != nil {
			t.Fatalf("History: %v", err)
		}
		if len(hist) > 0 {
			return hist[0].Status, hist[0].Error
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for history row to persist")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestChatCronEngine_CedarDenyAllGate_BlocksCronFire is the general
// gap-closure proof described in the file header.
func TestChatCronEngine_CedarDenyAllGate_BlocksCronFire(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateRow(t, store, "cr-denyall", "* * * * * *", true) // ordinary user row

	disp := newStubDispatcher()
	engine, err := scheduler.NewChatCronEngine(context.Background(), scheduler.ChatCronEngineConfig{
		Store:      store,
		Dispatcher: disp,
		Cedar:      fixedGate{outcome: cedar.Deny},
	})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	engine.Start()
	t.Cleanup(engine.Stop)

	status, errStr := waitForHistoryStatus(t, store, "cr-denyall")
	if status != "failed" {
		t.Fatalf("status=%q, want failed (Cedar denied) — the cron path is not consulting Cedar", status)
	}
	if !strings.Contains(errStr, "denied by cedar policy") {
		t.Errorf("error=%q, want it to name the cedar denial", errStr)
	}
	if calls := disp.snapshot(); len(calls) != 0 {
		t.Errorf("dispatcher called %d times, want 0 — a Cedar deny must block dispatch entirely", len(calls))
	}
}

// TestChatCronEngine_CedarAllowGate_PermitsCronFire is the paired
// positive: the same wiring, an allow-all gate, and the fire proceeds
// normally. Without this, TestChatCronEngine_CedarDenyAllGate_BlocksCronFire
// alone could not distinguish "Cedar is wired and denying" from "Cedar
// is wired and ALWAYS denies regardless of outcome" (a misconfiguration
// that would look identical from that test alone).
func TestChatCronEngine_CedarAllowGate_PermitsCronFire(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateRow(t, store, "cr-allowall", "* * * * * *", true)

	disp := newStubDispatcher()
	engine, err := scheduler.NewChatCronEngine(context.Background(), scheduler.ChatCronEngineConfig{
		Store:      store,
		Dispatcher: disp,
		Cedar:      fixedGate{outcome: cedar.Allow},
	})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	engine.Start()
	t.Cleanup(engine.Stop)

	status, _ := waitForHistoryStatus(t, store, "cr-allowall")
	if status != "completed" {
		t.Fatalf("status=%q, want completed", status)
	}
	if calls := disp.snapshot(); len(calls) != 1 || calls[0] != "cr-allowall" {
		t.Errorf("dispatcher calls=%v, want exactly [cr-allowall]", calls)
	}
}

// TestChatCronEngine_ModelCreatedNoAllowlist_RealEngineDenies is F1
// (spec.md §6.1) exercised through a real cron tick against the real
// embedded default policy bundle, not a stub — the shipped
// default_scheduled_run_policy.cedar's own `when` clause requires
// has_tool_allowlist == true, so a model-created row with none must
// refuse even though the shipped policy IS installed.
func TestChatCronEngine_ModelCreatedNoAllowlist_RealEngineDenies(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateRowWithProvenance(t, store, "cr-model-noallow", "* * * * * *",
		scheduler.ScheduledRunCreatedByModel, nil)

	realEngine, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}

	disp := newStubDispatcher()
	engine, err := scheduler.NewChatCronEngine(context.Background(), scheduler.ChatCronEngineConfig{
		Store:      store,
		Dispatcher: disp,
		Cedar:      realEngine,
	})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	engine.Start()
	t.Cleanup(engine.Stop)

	status, errStr := waitForHistoryStatus(t, store, "cr-model-noallow")
	if status != "failed" {
		t.Fatalf("status=%q, want failed (F1: model-created row with no tool allowlist must not run)", status)
	}
	if !strings.Contains(errStr, "denied by cedar policy") {
		t.Errorf("error=%q, want it to name the cedar denial", errStr)
	}
	if calls := disp.snapshot(); len(calls) != 0 {
		t.Errorf("dispatcher called %d times, want 0", len(calls))
	}
}

// TestChatCronEngine_ModelCreatedWithAllowlist_RealEngineFires is the
// paired positive for the real-engine F1 test above: a model-created
// row WITH a declared allowlist, against the same shipped policy
// bundle, must actually fire.
func TestChatCronEngine_ModelCreatedWithAllowlist_RealEngineFires(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateRowWithProvenance(t, store, "cr-model-allow", "* * * * * *",
		scheduler.ScheduledRunCreatedByModel, []string{"kenaz__web_fetch"})

	realEngine, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}

	disp := newStubDispatcher()
	engine, err := scheduler.NewChatCronEngine(context.Background(), scheduler.ChatCronEngineConfig{
		Store:      store,
		Dispatcher: disp,
		Cedar:      realEngine,
	})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	engine.Start()
	t.Cleanup(engine.Stop)

	status, errStr := waitForHistoryStatus(t, store, "cr-model-allow")
	if status != "completed" {
		t.Fatalf("status=%q (error=%q), want completed — a model-created row WITH an allowlist should fire", status, errStr)
	}
	if calls := disp.snapshot(); len(calls) != 1 || calls[0] != "cr-model-allow" {
		t.Errorf("dispatcher calls=%v, want exactly [cr-model-allow]", calls)
	}
}
