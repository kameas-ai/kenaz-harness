package scheduledchat_test

// Context policy (sessions/0349, scheduled-chat-context-budget) through
// the create/edit API. Real sqlite (core.New -> scheduler.SQLiteChatStore)
// because the assertions are about what a schedule reads back after a
// round trip through storage (CLAUDE.md blind spot #2), not the API's
// control flow alone.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/scheduledchat"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
)

func policyAPI(t *testing.T, window int) *scheduledchat.API {
	t.Helper()
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	cfg := scheduledchat.Config{Store: scheduler.NewSQLiteChatStore(c.Storage())}
	if window > 0 {
		cfg.ContextWindow = func(string) (int, bool) { return window, true }
		cfg.DefaultModel = func() scheduledchat.DefaultModel { return scheduledchat.DefaultModel{ProfileID: "p", Model: "m"} }
	}
	return scheduledchat.New(cfg)
}

func intp(v int) *int       { return &v }
func strp(v string) *string { return &v }

func TestContextPolicy_DefaultsWhenAbsent(t *testing.T) {
	api := policyAPI(t, 0)
	ctx := context.Background()
	e, err := api.Create(ctx, scheduledchat.CreateInput{Name: "n", PromptTemplate: "p", Cron: "*/30 * * * *"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := api.Get(ctx, e.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ContextBudgetTokens != 20000 || got.KeepRecentRuns != 2 || got.MemoryFocus != "" {
		t.Fatalf("defaults = %d/%d/%q, want 20000/2/\"\"", got.ContextBudgetTokens, got.KeepRecentRuns, got.MemoryFocus)
	}
}

func TestContextPolicy_RoundTripsAndUpdateKeepsAbsentFields(t *testing.T) {
	api := policyAPI(t, 0)
	ctx := context.Background()
	e, err := api.Create(ctx, scheduledchat.CreateInput{
		Name: "n", PromptTemplate: "p", Cron: "*/30 * * * *",
		ContextBudgetTokens: intp(8000), KeepRecentRuns: intp(0), MemoryFocus: strp("  open action items  "),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := api.Get(ctx, e.ID)
	if got.ContextBudgetTokens != 8000 || got.KeepRecentRuns != 0 || got.MemoryFocus != "open action items" {
		t.Fatalf("after create = %d/%d/%q, want 8000/0/\"open action items\"", got.ContextBudgetTokens, got.KeepRecentRuns, got.MemoryFocus)
	}
	// An edit that does not send the policy (an older client) keeps it.
	if _, err := api.Update(ctx, scheduledchat.UpdateInput{ID: e.ID, Name: "renamed", PromptTemplate: "p", Cron: "*/30 * * * *"}); err != nil {
		t.Fatalf("Update without policy: %v", err)
	}
	got, _ = api.Get(ctx, e.ID)
	if got.Name != "renamed" || got.ContextBudgetTokens != 8000 || got.KeepRecentRuns != 0 || got.MemoryFocus != "open action items" {
		t.Fatalf("after policy-less update = %+v, want the policy kept", got)
	}
	if _, err := api.Update(ctx, scheduledchat.UpdateInput{ID: e.ID, Name: "renamed", PromptTemplate: "p", Cron: "*/30 * * * *",
		ContextBudgetTokens: intp(30000), KeepRecentRuns: intp(5), MemoryFocus: strp("")}); err != nil {
		t.Fatalf("Update with policy: %v", err)
	}
	got, _ = api.Get(ctx, e.ID)
	if got.ContextBudgetTokens != 30000 || got.KeepRecentRuns != 5 || got.MemoryFocus != "" {
		t.Fatalf("after update = %d/%d/%q, want 30000/5/\"\"", got.ContextBudgetTokens, got.KeepRecentRuns, got.MemoryFocus)
	}
}

func TestContextPolicy_Validation(t *testing.T) {
	api := policyAPI(t, 128_000)
	ctx := context.Background()
	cases := []struct {
		name string
		in   scheduledchat.CreateInput
		want string
	}{
		{"budget below minimum", scheduledchat.CreateInput{ContextBudgetTokens: intp(3999)}, "at least 4000"},
		{"budget over the model window", scheduledchat.CreateInput{ContextBudgetTokens: intp(128_001)}, "context window (128000 tokens)"},
		{"keep negative", scheduledchat.CreateInput{KeepRecentRuns: intp(-1)}, "between 0 and 10"},
		{"keep over 10", scheduledchat.CreateInput{KeepRecentRuns: intp(11)}, "between 0 and 10"},
		{"focus too long", scheduledchat.CreateInput{MemoryFocus: strp(strings.Repeat("x", 2001))}, "at most 2000 characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.Name, in.PromptTemplate, in.Cron = "n", "p", "*/30 * * * *"
			_, err := api.Create(ctx, in)
			if !errors.Is(err, scheduledchat.ErrInvalidInput) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Create = %v, want ErrInvalidInput mentioning %q", err, tc.want)
			}
		})
	}
	// The window itself is allowed, and so is keep 0 / 10.
	if _, err := api.Create(ctx, scheduledchat.CreateInput{Name: "n", PromptTemplate: "p", Cron: "*/30 * * * *",
		ContextBudgetTokens: intp(128_000), KeepRecentRuns: intp(10)}); err != nil {
		t.Fatalf("Create at the bounds: %v", err)
	}
	dm, _ := api.DefaultModel(ctx)
	if dm.MaxContextTokens != 128_000 {
		t.Fatalf("DefaultModel.MaxContextTokens = %d, want 128000", dm.MaxContextTokens)
	}
}

// Without a known window the ceiling is scheduler.MaxContextBudgetTokens.
func TestContextPolicy_UnknownWindowUsesAbsoluteCeiling(t *testing.T) {
	api := policyAPI(t, 0)
	_, err := api.Create(context.Background(), scheduledchat.CreateInput{Name: "n", PromptTemplate: "p", Cron: "*/30 * * * *",
		ContextBudgetTokens: intp(scheduler.MaxContextBudgetTokens + 1)})
	if !errors.Is(err, scheduledchat.ErrInvalidInput) {
		t.Fatalf("Create over the absolute ceiling = %v, want ErrInvalidInput", err)
	}
}

// The run list reports a fire's summarisation cost on its own line.
func TestContextPolicy_HistoryCarriesCompactionCost(t *testing.T) {
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	store := scheduler.NewSQLiteChatStore(c.Storage())
	api := scheduledchat.New(scheduledchat.Config{Store: store})
	ctx := context.Background()
	e, err := api.Create(ctx, scheduledchat.CreateInput{Name: "n", PromptTemplate: "p", Cron: "*/30 * * * *"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	cost := 0.02
	if err := store.AppendHistory(ctx, scheduler.ChatRunHistoryRecord{ID: "h1", ChatRunID: e.ID, Status: "completed", CostUSD: 0.12, CompactionCostUSD: &cost}); err != nil {
		t.Fatalf("AppendHistory: %v", err)
	}
	hist, err := api.History(ctx, e.ID, 5)
	if err != nil || len(hist) != 1 {
		t.Fatalf("History = %+v (err %v)", hist, err)
	}
	if hist[0].CompactionCostUSD == nil || *hist[0].CompactionCostUSD != 0.02 || hist[0].CostUSD != 0.12 {
		t.Fatalf("summary = %+v, want compaction cost 0.02 within cost 0.12", hist[0])
	}
}
