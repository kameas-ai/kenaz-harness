package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/compactionpolicy"
)

// compaction_dial_golden_test.go is the WP07 behaviour pin for
// agentgraph-total-convergence-01PMGX01 Phase 4.
//
// WHY A GOLDEN AND NOT ASSERTIONS. Phase 4 moves compaction out of the
// pre-send hook and into the graph as a `compact` node between
// history_read and the agent loop (spec §4.4). That is a relocation,
// not a redesign: every tier must keep making the same decision, with
// the same arguments, in the same order, and return the same error.
// Scattered per-tier assertions cannot prove "the same" across a move —
// each one only pins the branch its author remembered. One byte-exact
// transcript of the whole 5-tier × 9-scenario matrix can, and it fails
// loudly on any decision that shifts.
//
// WHAT "OBSERVABLE OUTPUT" MEANS HERE. The pre-send layer does not
// itself transform messages. Its entire observable contribution is:
//
//  1. which compaction.SessionEngine calls it makes, in order, with which
//     arguments (the Engine is the seam that actually rewrites the
//     persisted session history — see core/agentgraph/compaction/session_engine.go); and
//  2. the error it returns to the caller (compaction.ErrSessionFull is
//     load-bearing: it is what makes the chat surface render the
//     honest "session full" copy instead of a provider error).
//
// The Engine's own transform is pinned by core/agentgraph/compaction's own tests
// and is NOT changed by Phase 4 — it stays exactly where it is, invoked
// from a new place. So pinning (1) + (2) pins the whole of what moves.
//
// HOW WP08 CONSUMES THIS. Every case runs through observeCompactionDial
// below, which is the ONE function that names the production entry
// point. WP08 re-points that single line at the graph node path and
// this file's golden must not move a byte. If it moves, the relocation
// changed behaviour and the WP is not behaviour-preserving.

// dialCallRecorder is a compaction.SessionEngine that records every call with
// its full argument list instead of performing one.
//
// Race-safety: the pre-send path is single-goroutine per session, but
// CI runs -race and a future node-side caller may fan out, so the
// recorder locks and reads go through snapshot() (CLAUDE.md "Race-safe
// test fakes").
type dialCallRecorder struct {
	mu    sync.Mutex
	calls []string

	compactErr error
	rollingErr error
}

func (d *dialCallRecorder) Compact(_ context.Context, sessionID string,
	model compaction.ProviderProfileRef, oldestPct float64) (string, error) {
	d.mu.Lock()
	d.calls = append(d.calls, fmt.Sprintf(
		"Compact(session=%q, model=%q, oldest_pct=%.2f)", sessionID, model.String(), oldestPct))
	d.mu.Unlock()
	if d.compactErr != nil {
		return "", d.compactErr
	}
	return "summary-1", nil
}

func (d *dialCallRecorder) RollingSummarize(_ context.Context, sessionID string,
	model compaction.ProviderProfileRef, recentWindow int) (string, error) {
	d.mu.Lock()
	d.calls = append(d.calls, fmt.Sprintf(
		"RollingSummarize(session=%q, model=%q, recent_window=%d)", sessionID, model.String(), recentWindow))
	d.mu.Unlock()
	if d.rollingErr != nil {
		return "", d.rollingErr
	}
	return "rolling-1", nil
}

func (d *dialCallRecorder) snapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.calls))
	copy(out, d.calls)
	return out
}

// dialScenario is one row of the matrix: a session shape plus whatever
// the engine is scripted to do when reached.
type dialScenario struct {
	name string
	// historyTokens sizes the pre-existing persisted history.
	historyTokens int
	// capTokens is what MaxContextTokens reports. 0 => (0, false),
	// i.e. "unknown model".
	capTokens int
	// noCapLookup drops the MaxContextTokens dep entirely (a chassis
	// that wired the engine but not the capability catalog).
	noCapLookup bool
	// noDeps drops CompactionDeps entirely (test-fixture / boot-failure
	// path).
	noDeps bool
	// envOff sets HARNESS_COMPACTION=off for this case.
	envOff bool
	// compactErr / rollingErr script the engine's replies.
	compactErr error
	rollingErr error
}

// dialScenarios is the scenario axis. Every branch reachable inside the
// pre-send tier switch is represented at least once; the tier axis then
// crosses each of them with all five dial settings, including the
// combinations that are no-ops (an "off" tier never reaches an engine
// error, and pinning that it does not is exactly the point).
func dialScenarios() []dialScenario {
	return []dialScenario{
		{name: "far-under-trigger", historyTokens: 10, capTokens: 100_000},
		{name: "at-70pct-of-cap", historyTokens: 65, capTokens: 100},
		{name: "at-90pct-of-cap", historyTokens: 85, capTokens: 100},
		{name: "over-cap", historyTokens: 150, capTokens: 100},
		{name: "unknown-model-cap", historyTokens: 150, capTokens: 0},
		{name: "no-cap-lookup-dep", historyTokens: 150, noCapLookup: true},
		{
			name:          "engine-model-too-small",
			historyTokens: 85, capTokens: 100,
			compactErr: &compaction.ErrCompactionModelTooSmall{NeedsTokens: 10000, ModelMaxTokens: 1000},
			rollingErr: &compaction.ErrCompactionModelTooSmall{NeedsTokens: 10000, ModelMaxTokens: 1000},
		},
		{
			name:          "engine-generic-error",
			historyTokens: 85, capTokens: 100,
			compactErr: errors.New("provider hiccup"),
			rollingErr: errors.New("provider hiccup"),
		},
		{name: "env-var-off", historyTokens: 150, capTokens: 100, envOff: true},
		{name: "no-compaction-deps", historyTokens: 150, capTokens: 100, noDeps: true},
	}
}

// dialTiers is the tier axis, in the dial's own display order.
func dialTiers() []compactionpolicy.CompactionAggressiveness {
	return []compactionpolicy.CompactionAggressiveness{
		compactionpolicy.AggressivenessOff,
		compactionpolicy.AggressivenessConservative,
		compactionpolicy.AggressivenessBalanced,
		compactionpolicy.AggressivenessAggressive,
		compactionpolicy.AggressivenessMaximal,
	}
}

// observeCompactionDial is the SINGLE point in this file that names the
// production compaction entry point.
//
// WP07 pointed it at ChatRunner.runPreSendCompaction, the pre-kernel
// pass. WP08 re-points it here, at the real thing: a kernel run over a
// graph shaped like chat_default.yaml's head — history_read feeding a
// `compact` node — with the run's Env carrying the bound FR-041
// pipeline as its Compactor. Nothing else in this file changed, and the
// golden file did not move a byte. That is the proof the WP is a
// relocation and not a redesign.
//
// The path exercised end to end is: kernel promotion -> compactExecutor
// -> Pipeline.Compact -> cascading-config resolution -> PresetForTier's
// SiteManual entry -> StrategySessionRewrite -> the tier switch ->
// compaction.SessionEngine. Every layer WP08 introduced is in it.
func observeCompactionDial(t *testing.T, tier compactionpolicy.CompactionAggressiveness, sc dialScenario) ([]string, string) {
	t.Helper()

	rec := &dialCallRecorder{compactErr: sc.compactErr, rollingErr: sc.rollingErr}

	// The node is handed what history_read loads. The pre-send pass
	// counted (persisted history + the new user turn) as two separate
	// inputs; the node counts one slice that already contains both,
	// which is why the fixture spells the user turn out as a message.
	hist := []coreag.Message{
		fillerMessage("user", sc.historyTokens),
		{Role: "user", Content: "the next user turn"},
	}

	cfg := Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        &recordingBroker{},
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: hist},
		GraphLoader:   func() (coreag.Graph, error) { return minimalChatGraph(), nil },
		MaxTurns:      func() int { return 25 },
		// The tier reaches the pipeline as the global-layer preset, the
		// same way production seeds it (core/rpc/api.go).
		CompactionPipeline: newTierPipeline(string(tier)),
	}
	if !sc.noDeps {
		deps := &CompactionDeps{
			Engine:         rec,
			Aggressiveness: func() compactionpolicy.CompactionAggressiveness { return tier },
			CompactionModel: func() (compaction.ProviderProfileRef, bool) {
				return compaction.ProviderProfileRef{}, false
			},
			RecentWindow: func() int { return 4 },
		}
		if !sc.noCapLookup {
			capTokens := sc.capTokens
			deps.MaxContextTokens = func(_ compaction.ProviderProfileRef) (int, bool) {
				if capTokens <= 0 {
					return 0, false
				}
				return capTokens, true
			}
		}
		cfg.Compaction = deps
	}

	runner, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// ---- the one production path this golden pins ----
	env := &coreag.Env{
		RunID:     "run-1",
		SessionID: "session-1",
		Graph:     compactHeadGraph(),
		History:   historyAdapterFunc(cfg.History.History),
		Compactor: runner.bindCompactor("profile-1", "model-1"),
	}
	cerr := coreag.NewKernel().Run(context.Background(), env)
	// ---- end ----

	msg := "<nil>"
	if cerr != nil {
		root := rootError(cerr)
		msg = root.Error()
		if errors.Is(cerr, compaction.ErrSessionFull) {
			msg += " [is:ErrSessionFull]"
		}
	}
	return rec.snapshot(), msg
}

// newTierPipeline builds the FR-041 pipeline the chassis would build
// for one dial tier: the tier's preset seeded at the global layer, and
// the ordinary strategy set registered. The session-rewrite strategy is
// NOT registered here — it is bound per run by the chat runner, which
// is the behaviour under test.
func newTierPipeline(tier string) *compaction.Pipeline {
	p := compaction.NewPipeline(
		compaction.WithResolver(compaction.NewMemoryResolverWithDefaults(compaction.PresetForTier(tier))),
	)
	p.RegisterStrategy(compaction.NewDropOldestStrategy())
	return p
}

// compactHeadGraph is the head of chat_default.yaml: history_read
// feeding a compact node. Everything downstream of the compact node in
// the real graph needs an LLM, an AskBus and a session writer, none of
// which this golden is about.
func compactHeadGraph() *coreag.Graph {
	return &coreag.Graph{
		ID:          "compact_head",
		Name:        "compaction head of chat_default",
		Entrypoints: []string{"history_in"},
		Nodes: []coreag.Node{
			{
				ID:    "history_in",
				Kind:  coreag.NodeKindHistoryRead,
				Attrs: coreag.HistoryReadAttrs{N: 0},
			},
			{
				ID:    "compact_history",
				Kind:  coreag.NodeKindCompact,
				Attrs: coreag.CompactAttrs{Strategy: "session_rewrite"},
			},
		},
		Edges: []coreag.Edge{
			{
				From: coreag.EndpointRef{Node: "history_in", Port: "messages"},
				To:   coreag.EndpointRef{Node: "compact_history", Port: "input"},
			},
		},
	}
}

// rootError unwraps to the innermost error so the golden records the
// decision rather than the call stack that carried it. The pre-kernel
// pass returned its sentinels bare; a node returns them through the
// executor's and the kernel's error wrapping. Same decision, more
// envelopes — and the envelope is not what this test is pinning.
func rootError(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
}

// TestCompactionDial_Golden renders the whole tier × scenario matrix to
// a byte-exact transcript and diffs it against testdata.
//
// Refresh with: go test ./core/rpc/views/agentgraph/chat/ -run
// TestCompactionDial_Golden -update-compaction-golden
//
// Refreshing is a deliberate act: it means the compaction dial's
// observable behaviour changed, which for Phase 4 is a WP failure
// unless the change is the one the WP explicitly set out to make.
func TestCompactionDial_Golden(t *testing.T) {
	// Not parallel: the env-var-off scenario mutates process env.
	var b strings.Builder
	b.WriteString("# Compaction dial — observable behaviour per tier per scenario.\n")
	b.WriteString("# Generated by TestCompactionDial_Golden (WP07,\n")
	b.WriteString("# agentgraph-total-convergence-01PMGX01 Phase 4). Each block records the\n")
	b.WriteString("# compaction.SessionEngine calls the dial makes, in order, with full arguments,\n")
	b.WriteString("# followed by the error returned to the chat surface.\n")
	b.WriteString("#\n")
	b.WriteString("# Session fixture: one persisted user message sized to the stated token\n")
	b.WriteString("# count, plus the new user turn \"the next user turn\".\n")

	for _, sc := range dialScenarios() {
		b.WriteString("\n================================================================\n")
		fmt.Fprintf(&b, "scenario: %s\n", sc.name)
		fmt.Fprintf(&b, "  history_tokens=%d cap=%s deps=%s env_off=%t\n",
			sc.historyTokens, capLabel(sc), depsLabel(sc), sc.envOff)
		b.WriteString("================================================================\n")

		for _, tier := range dialTiers() {
			calls, errMsg := runDialCase(t, tier, sc)
			fmt.Fprintf(&b, "\ntier=%s\n", tier)
			if len(calls) == 0 {
				b.WriteString("  engine calls: (none)\n")
			} else {
				b.WriteString("  engine calls:\n")
				for i, c := range calls {
					fmt.Fprintf(&b, "    %d. %s\n", i+1, c)
				}
			}
			fmt.Fprintf(&b, "  returned error: %s\n", errMsg)
		}
	}

	got := b.String()
	goldenPath := filepath.Join("testdata", "compaction_dial_golden.txt")

	if os.Getenv("UPDATE_COMPACTION_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden refreshed: %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v (set UPDATE_COMPACTION_GOLDEN=1 to create it)", goldenPath, err)
	}
	if got != string(want) {
		t.Errorf("compaction dial behaviour changed.\n%s", firstDiff(string(want), got))
	}
}

// runDialCase isolates the env-var mutation to the single case that
// needs it (t.Setenv restores on subtest exit).
func runDialCase(t *testing.T, tier compactionpolicy.CompactionAggressiveness, sc dialScenario) ([]string, string) {
	t.Helper()
	var calls []string
	var errMsg string
	t.Run(fmt.Sprintf("%s/%s", sc.name, tier), func(t *testing.T) {
		if sc.envOff {
			t.Setenv("HARNESS_COMPACTION", "off")
		}
		calls, errMsg = observeCompactionDial(t, tier, sc)
	})
	return calls, errMsg
}

func capLabel(sc dialScenario) string {
	if sc.noCapLookup {
		return "no-lookup-dep"
	}
	if sc.capTokens <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d", sc.capTokens)
}

func depsLabel(sc dialScenario) string {
	if sc.noDeps {
		return "unwired"
	}
	return "wired"
}

// firstDiff renders the first differing line with a little context so a
// failure is readable without an external diff tool.
func firstDiff(want, got string) string {
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	n := len(wl)
	if len(gl) < n {
		n = len(gl)
	}
	for i := 0; i < n; i++ {
		if wl[i] != gl[i] {
			var b strings.Builder
			fmt.Fprintf(&b, "first difference at line %d:\n", i+1)
			for j := i - 3; j <= i+3; j++ {
				if j < 0 || j >= n {
					continue
				}
				marker := "  "
				if j == i {
					marker = "!!"
				}
				fmt.Fprintf(&b, "%s want[%d]: %s\n", marker, j+1, wl[j])
				fmt.Fprintf(&b, "%s  got[%d]: %s\n", marker, j+1, gl[j])
			}
			return b.String()
		}
	}
	return fmt.Sprintf("line counts differ: want %d lines, got %d lines", len(wl), len(gl))
}
