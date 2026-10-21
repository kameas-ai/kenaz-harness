package compaction

// Unit tests for RollingSummarizeWith's options (session_rolling_runs.go):
// the run-boundary tail, the token trigger, the tail budget and the
// prompt additions. Algorithm-level, over the package's in-memory fakes
// like session_rolling_test.go — the persisted round trip (real sqlite,
// soft-archive, the incremental second roll) is driven end to end by
// core/rpc/chat_run_dispatcher_budget_test.go.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// runsFixture builds a conversation of scheduled runs. Each run is a
// prompt row "p-N" plus an assistant reply "r-N"; interactive[N] adds a
// user turn "i-N" + reply "ir-N" after run N. Every row's content is
// `size` bytes so fakeTokenizer counts are predictable.
func runsFixture(runs int, size int, interactive map[int]bool) ([]SessionMessage, []string) {
	pad := func(tag string) string {
		if len(tag) >= size {
			return tag
		}
		return tag + strings.Repeat(".", size-len(tag))
	}
	var out []SessionMessage
	var ids []string
	seq := int64(0)
	add := func(id, role string) {
		out = append(out, SessionMessage{ID: id, Role: role, Content: pad(id), Sequence: seq})
		seq++
	}
	for i := 0; i < runs; i++ {
		add(fmt.Sprintf("p-%d", i), "user")
		ids = append(ids, fmt.Sprintf("p-%d", i))
		add(fmt.Sprintf("r-%d", i), "assistant")
		if interactive[i] {
			add(fmt.Sprintf("i-%d", i), "user")
			add(fmt.Sprintf("ir-%d", i), "assistant")
		}
	}
	return out, ids
}

func idSet(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func TestFindRunWindowStart(t *testing.T) {
	msgs, ids := runsFixture(4, 10, map[int]bool{2: true})
	// rows: p0 r0 p1 r1 p2 r2 i2 ir2 p3 r3
	set := idSet(ids...)
	cases := []struct {
		name      string
		keep      int
		set       map[string]bool
		wantStart int
		wantRuns  int
	}{
		{"keep 2: tail opens at p-2 and holds its interactive turn", 2, set, 4, 2},
		{"keep 1", 1, set, 8, 1},
		{"keep 0 folds everything", 0, set, len(msgs), 0},
		{"more runs than exist keeps everything", 9, set, 0, 4},
		// No named starts at all (a conversation from before the ids were
		// recorded): every user row is a run start, so the interactive
		// turn counts as one.
		{"legacy: unnamed user rows count", 2, idSet(), 6, 2},
		// Only the newest run is named: the rows before it are legacy
		// and each user row there opens a run.
		{"mixed: legacy rows before the oldest named start", 3, idSet("p-3"), 4, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, runs := findRunWindowStart(msgs, tc.set, tc.keep)
			if start != tc.wantStart || runs != tc.wantRuns {
				t.Fatalf("findRunWindowStart = (%d, %d), want (%d, %d)", start, runs, tc.wantStart, tc.wantRuns)
			}
		})
	}
}

// Under the trigger: no call, no write, no audit — a budgeted caller
// checks on every fire.
func TestRollingSummarizeWith_UnderBudgetIsANoOp(t *testing.T) {
	msgs, ids := runsFixture(3, 100, nil) // 600 tokens
	store := &fakeStore{messages: msgs}
	llm := &fakeLLM{text: "s"}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, fakeCapabilities{max: 1_000_000, ok: true}, em, 4)

	res, err := eng.RollingSummarizeWith(context.Background(), "s", ProviderProfileRef{}, RollingOptions{
		RunWindow:        &RunWindow{StartIDs: ids, Keep: 1},
		OnlyIfOverTokens: 650,
		PendingTokens:    50, // 600 + 50 is not over 650
	})
	if err != nil {
		t.Fatalf("RollingSummarizeWith: %v", err)
	}
	if !res.UnderBudget || res.SummaryID != "" || res.TokensBefore != 600 {
		t.Fatalf("result = %+v, want an under-budget no-op at 600 tokens", res)
	}
	if len(llm.calls) != 0 || len(store.applyCalls) != 0 || len(em.events) != 0 {
		t.Fatalf("calls=%d applies=%d audits=%d, want none", len(llm.calls), len(store.applyCalls), len(em.events))
	}
}

// Over the trigger: the last Keep runs stay verbatim (with the
// interactive turn inside them), everything older is folded into one
// rolling summary, and the prompt carries the length target and the
// caller's instructions before the pile.
func TestRollingSummarizeWith_KeepsLastRunsAndRollsOlder(t *testing.T) {
	msgs, ids := runsFixture(5, 100, map[int]bool{1: true, 3: true})
	store := &fakeStore{messages: msgs}
	llm := &fakeLLM{text: "condensed", outputTokens: 7}
	em := &fakeAudit{}
	eng := newTestEngine(t, store, llm, fakeCapabilities{max: 1_000_000, ok: true}, em, 4)

	res, err := eng.RollingSummarizeWith(context.Background(), "s", ProviderProfileRef{ProviderID: "p"}, RollingOptions{
		RunWindow:         &RunWindow{StartIDs: ids, Keep: 2},
		OnlyIfOverTokens:  500,
		MaxSummaryTokens:  400,
		ExtraInstructions: "Remember: FOCUS-MARKER",
		Tier:              "scheduled_budget",
	})
	if err != nil {
		t.Fatalf("RollingSummarizeWith: %v", err)
	}
	if len(store.applyCalls) != 1 {
		t.Fatalf("ApplyCompaction calls = %d, want 1", len(store.applyCalls))
	}
	got := store.applyCalls[0].originalIDs
	want := []string{"p-0", "r-0", "p-1", "r-1", "i-1", "ir-1", "p-2", "r-2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("archived = %v, want %v (runs 0-2 and run 1's interactive turn)", got, want)
	}
	if res.KeptRuns != 2 || res.RolledMessages != len(want) || res.HadPreviousSummary {
		t.Fatalf("result = %+v, want 2 kept runs, %d rolled, first roll", res, len(want))
	}
	prompt := llm.calls[0].userPrompt
	for _, s := range []string{"about 300 words", "Remember: FOCUS-MARKER", "p-2", "i-1"} {
		if !strings.Contains(prompt, s) {
			t.Errorf("summariser prompt missing %q", s)
		}
	}
	for _, s := range []string{"p-3", "i-3", "p-4"} {
		if strings.Contains(prompt, s) {
			t.Errorf("summariser prompt holds kept row %q", s)
		}
	}
	if strings.Index(prompt, "FOCUS-MARKER") > strings.Index(prompt, "<rolling_pile>") {
		t.Error("instructions must precede the pile")
	}
	if len(em.events) != 1 || em.events[0].kind != audit.KindSessionCompacted {
		t.Fatalf("audit = %+v, want one session-compacted event", em.events)
	}
	if p := em.events[0].payload.(audit.SessionCompactedPayload); p.AggressivenessTier != "scheduled_budget" {
		t.Fatalf("audit tier = %q, want scheduled_budget", p.AggressivenessTier)
	}
}

// The second overflow is incremental: the pile is the previous summary
// plus only the rows that aged out since, never the archived originals.
func TestRollingSummarizeWith_SecondRollIsIncremental(t *testing.T) {
	msgs, ids := runsFixture(6, 100, nil)
	prev := SessionMessage{ID: "sum-1", Role: "system", Content: rollingSummaryContentPrefix + "PREVIOUS-SUMMARY" + rollingSummaryContentSuffix, Sequence: -1}
	// Runs 0-2 were folded into sum-1 last time: only runs 3-5 are active.
	active := append([]SessionMessage{prev}, msgs[6:]...)
	store := &fakeStore{messages: active}
	llm := &fakeLLM{text: "updated"}
	eng := newTestEngine(t, store, llm, fakeCapabilities{max: 1_000_000, ok: true}, &fakeAudit{}, 4)

	res, err := eng.RollingSummarizeWith(context.Background(), "s", ProviderProfileRef{}, RollingOptions{
		RunWindow: &RunWindow{StartIDs: ids, Keep: 2},
	})
	if err != nil {
		t.Fatalf("RollingSummarizeWith: %v", err)
	}
	pile := llm.calls[0].userPrompt
	pile = pile[strings.Index(pile, "<rolling_pile>"):]
	wantPile := "<rolling_pile>\n" + prev.Content + "\nuser: " + msgs[6].Content + "\nassistant: " + msgs[7].Content + "\n</rolling_pile>"
	if pile != wantPile {
		t.Fatalf("pile =\n%s\nwant\n%s", pile, wantPile)
	}
	if got := strings.Join(store.applyCalls[0].originalIDs, ","); got != "sum-1,p-3,r-3" {
		t.Fatalf("archived = %s, want the previous summary and run 3 only", got)
	}
	if !res.HadPreviousSummary || res.RolledMessages != 2 {
		t.Fatalf("result = %+v, want an incremental roll of 2 rows", res)
	}
}

// Nothing aged out since the last roll: the summary alone is never
// re-summarised.
func TestRollingSummarizeWith_SummaryAloneIsNotResummarised(t *testing.T) {
	msgs, ids := runsFixture(2, 100, nil)
	prev := SessionMessage{ID: "sum-1", Role: "system", Content: rollingSummaryContentPrefix + "x" + rollingSummaryContentSuffix, Sequence: -1}
	store := &fakeStore{messages: append([]SessionMessage{prev}, msgs...)}
	llm := &fakeLLM{text: "s"}
	eng := newTestEngine(t, store, llm, fakeCapabilities{max: 1_000_000, ok: true}, &fakeAudit{}, 4)
	res, err := eng.RollingSummarizeWith(context.Background(), "s", ProviderProfileRef{}, RollingOptions{
		RunWindow: &RunWindow{StartIDs: ids, Keep: 2},
	})
	if err != nil || res.SummaryID != "" || len(llm.calls) != 0 {
		t.Fatalf("result = %+v err=%v calls=%d, want a no-op", res, err, len(llm.calls))
	}
}

// The tail budget wins over the run count: when the last Keep runs alone
// do not fit, fewer are kept, never below MinKeep.
func TestRollingSummarizeWith_TailBudgetShrinksKeptRuns(t *testing.T) {
	msgs, ids := runsFixture(4, 100, nil) // each run = 200 tokens
	cases := []struct {
		name     string
		budget   int
		minKeep  int
		wantKept int
	}{
		{"fits 2", 400, 0, 2},
		{"fits 1", 399, 0, 1},
		{"fits none", 150, 0, 0},
		{"floor", 150, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{messages: msgs}
			llm := &fakeLLM{text: "s"}
			eng := newTestEngine(t, store, llm, fakeCapabilities{max: 1_000_000, ok: true}, &fakeAudit{}, 4)
			res, err := eng.RollingSummarizeWith(context.Background(), "s", ProviderProfileRef{}, RollingOptions{
				RunWindow:        &RunWindow{StartIDs: ids, Keep: 2, MinKeep: tc.minKeep},
				TailBudgetTokens: tc.budget,
			})
			if err != nil {
				t.Fatalf("RollingSummarizeWith: %v", err)
			}
			if res.KeptRuns != tc.wantKept {
				t.Fatalf("kept %d runs, want %d", res.KeptRuns, tc.wantKept)
			}
			if wantRolled := len(msgs) - 2*tc.wantKept; res.RolledMessages != wantRolled {
				t.Fatalf("rolled %d rows, want %d", res.RolledMessages, wantRolled)
			}
		})
	}
}

// No options: the maximal tier's prompt bytes are unchanged.
func TestBuildRollingPrompt_NoOptionsIsTheLockedPrompt(t *testing.T) {
	if got, want := buildRollingPrompt("PILE", RollingOptions{RecentWindow: 4}), fmt.Sprintf(rollingSummaryUserPromptTemplate, "PILE"); got != want {
		t.Fatalf("prompt drifted:\n%s\nwant\n%s", got, want)
	}
}

func TestCallCostTally(t *testing.T) {
	ctx, tally := WithCallCostTally(context.Background())
	CallCostTallyFrom(ctx).Add(0.25, false, 10, 2)
	CallCostTallyFrom(ctx).Add(9, true, 5, 1)
	CallCostTallyFrom(context.Background()).Add(1, false, 1, 1) // no tally: no-op
	got := tally.Snapshot()
	if got.Calls != 2 || got.TotalUSD != 0.25 || got.Indeterminate != 1 || got.InputTokens != 15 || got.OutputTokens != 3 {
		t.Fatalf("snapshot = %+v", got)
	}
}
