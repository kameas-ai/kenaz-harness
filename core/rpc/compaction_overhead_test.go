package rpc

// compaction_overhead_test.go — chat-turn-integrity-01PMZ606 WP12: the
// join CK-08 + owner ruling X-7 wanted. compactionLLM and compactionAudit
// (core/rpc/api.go ~:4838/:4842 -> the llmStack struct) were constructed
// at boot inside newLLMStack, but a repo-wide grep found only the two
// field declarations, the construction call, and the two assignments into
// llmStack{} — nothing after newLLMStack returned ever read
// stack.compactionLLM / stack.compactionAudit. Overhead() and Recent()
// had no caller anywhere in production, and the SessionsView
// compaction-overhead header row was permanently hidden as a result.
//
// Two things are asserted separately, because they are separate defects
// of the same class:
//
//  1. TestNew_CompactionWiringReachesAPI — the JOIN itself: does a1eal
//     boot (core.New + rpc.New, with a real session store so
//     buildCompactionWiring's nil-guards don't short-circuit) leave
//     a.compactionLLM / a.compactionAudit non-nil? Before this WP the
//     answer was "constructed, then dropped on the floor" — llmStack held
//     them but API never copied them out.
//  2. TestAPI_CompactionOverhead_ReadsStoredTotalsAndRecentTiers — given
//     the join exists, does CompactionOverhead() correctly project
//     Overhead()'s running tally and Recent()'s audit ring into the wire
//     shape? Drives BOTH compactionLLM.Overhead() (via a real
//     CallForSummary against a fake corellm.Registry — there is no public
//     setter, so a real call is the only way to get non-zero data) and
//     compactionAudit.Recent() (via a real Emit), so both are exercised
//     as production code paths, not asserted against hand-built structs.

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	compactionwiring "github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction/wiring"
	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// fakeOverheadRegistry is a minimal corellm.Registry whose Stream() call
// returns a canned Response carrying real Usage + Cost data, so
// compactionwiring.LLMCaller.CallForSummary has something non-zero to
// fold into its running OverheadTotals. Modeled on chat's stubRegistry /
// schemaAwareRegistry test fakes (core/rpc/views/agentgraph/chat).
type fakeOverheadRegistry struct{}

func (fakeOverheadRegistry) RegisterAdapter(_ corellm.ProviderAdapter)      {}
func (fakeOverheadRegistry) LoadProfiles(_ []corellm.ProviderProfile) error { return nil }
func (fakeOverheadRegistry) Evict(_ string) error                           { return nil }
func (fakeOverheadRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{}, nil
}
func (fakeOverheadRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult { return nil }
func (fakeOverheadRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	return &fakeOverheadStream{}, nil
}

type fakeOverheadStream struct{}

func (s *fakeOverheadStream) Events() <-chan corellm.StreamEvent {
	ch := make(chan corellm.StreamEvent)
	close(ch)
	return ch
}
func (s *fakeOverheadStream) Cancel() error { return nil }
func (s *fakeOverheadStream) Final() (corellm.Response, error) {
	return corellm.Response{
		Content:      []corellm.ContentBlock{{Type: "text", Text: "summary"}},
		FinishReason: "stop",
		Usage:        corellm.Usage{InputTokens: 900, OutputTokens: 150},
		Cost:         corellm.Cost{Currency: "USD", Total: 0.0081},
	}, nil
}

// TestAPI_CompactionOverhead_ReadsStoredTotalsAndRecentTiers is AC-015's
// core: with overhead totals present (a real compaction LLM call landed)
// the RPC surfaces them, and the audit ring's tier history rides along.
func TestAPI_CompactionOverhead_ReadsStoredTotalsAndRecentTiers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	caller := compactionwiring.NewLLMCaller(fakeOverheadRegistry{})
	if caller == nil {
		t.Fatal("NewLLMCaller returned nil for a non-nil registry")
	}
	// Drive a REAL CallForSummary — this is the only way to get
	// non-zero OverheadTotals; there is no public seed/setter, by
	// design (LLMCaller.recordOverhead is private and only called from
	// inside CallForSummary itself).
	ref := compaction.ProviderProfileRef{ProviderID: "test-provider", ModelID: "test-model"}
	if _, _, _, err := caller.CallForSummary(ctx, ref, "system", "user"); err != nil {
		t.Fatalf("CallForSummary: %v", err)
	}

	emitter := compactionwiring.NewAuditEmitter()
	emitter.Emit(ctx, audit.KindSessionCompacted, audit.SessionCompactedPayload{
		SessionID:          "sess-1",
		AggressivenessTier: "balanced",
		ModelUsed:          "test-model",
		TokensInSpan:       4096,
		TokensAfterSummary: 512,
		CompressionRatio:   0.125,
	})

	a := &API{compactionLLM: caller, compactionAudit: emitter}
	got, err := a.CompactionOverhead(ctx)
	if err != nil {
		t.Fatalf("CompactionOverhead: %v", err)
	}

	if got.Calls != 1 {
		t.Errorf("Calls = %d, want 1", got.Calls)
	}
	if got.Total != 0.0081 {
		t.Errorf("Total = %v, want 0.0081", got.Total)
	}
	if got.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", got.Currency)
	}
	if got.InputTokens != 900 || got.OutputTokens != 150 {
		t.Errorf("InputTokens/OutputTokens = %d/%d, want 900/150", got.InputTokens, got.OutputTokens)
	}
	if got.IndeterminateCalls != 0 {
		t.Errorf("IndeterminateCalls = %d, want 0", got.IndeterminateCalls)
	}
	if len(got.RecentTiers) != 1 || got.RecentTiers[0] != "balanced" {
		t.Errorf("RecentTiers = %v, want [balanced] — Recent() is CK-08's other unread half", got.RecentTiers)
	}
}

// TestAPI_CompactionOverhead_ZeroValueWhenCompactionDisabled pins the
// degrade contract: nil compactionLLM (HARNESS_COMPACTION=off, or no
// session store at boot) returns a zero CompactionOverheadInfo with no
// error, matching buildCompactionWiring's own nil-on-disabled contract
// rather than surfacing that as a fault.
func TestAPI_CompactionOverhead_ZeroValueWhenCompactionDisabled(t *testing.T) {
	t.Parallel()
	a := &API{}
	got, err := a.CompactionOverhead(context.Background())
	if err != nil {
		t.Fatalf("CompactionOverhead: %v", err)
	}
	if got.Total != 0 || got.Currency != "" || got.Calls != 0 ||
		got.IndeterminateCalls != 0 || got.InputTokens != 0 || got.OutputTokens != 0 ||
		len(got.RecentTiers) != 0 {
		t.Errorf("CompactionOverhead() = %+v, want zero value", got)
	}
}

// TestNew_CompactionWiringReachesAPI is the JOIN falsification: boot a
// real Core (real session store, so buildCompactionWiring's nil-guards
// pass) through the production New() constructor and assert
// a.compactionLLM / a.compactionAudit are non-nil afterward. Before this
// WP they were always nil here — newLLMStack constructed and returned
// them on llmStack, but New() never read stack.compactionLLM /
// stack.compactionAudit, so CompactionOverhead() had nothing to read
// regardless of how correct its own logic was.
func TestNew_CompactionWiringReachesAPI(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	t.Cleanup(api.Shutdown)

	if api.compactionLLM == nil {
		t.Fatal("api.compactionLLM is nil after a real boot with a real session store — " +
			"newLLMStack constructed it but New() never copied stack.compactionLLM onto the API")
	}
	if api.compactionAudit == nil {
		t.Fatal("api.compactionAudit is nil after a real boot with a real session store — " +
			"newLLMStack constructed it but New() never copied stack.compactionAudit onto the API")
	}

	// The RPC surface must be reachable end-to-end too, even though no
	// compaction has actually run in this test (zero value, no error).
	got, err := api.CompactionOverhead(context.Background())
	if err != nil {
		t.Fatalf("CompactionOverhead: %v", err)
	}
	if got.Calls != 0 {
		t.Errorf("Calls = %d, want 0 (no compaction ran in this test)", got.Calls)
	}
}
