package chat

import (
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

// The per-turn token ceiling must not be written straight into
// MaxTokensPerRun, which Kernel.checkBudget compares against the
// CUMULATIVE run counter.
//
// This is the regression test for a live failure: a session at 262k context
// on the most permissive tier died with
//
//	reached the token budget cap (3471969 used of 2097152 allowed)
//	at the "autonomous" autonomy tier
//
// 2_097_152 is TierAutonomous's per-TURN ceiling. Because chat_default is a
// per-session kernel run that pauses on AskNode between user turns, one run
// spans the whole session, so that value was roughly eight turns of total
// headroom -- on the tier that is supposed to be the least restrictive.
func TestApplyTokenCeilingKnob_PerTurnCeilingIsNotAPerRunCap(t *testing.T) {
	t.Parallel()

	const graphDeclared = 20_000_000 // chat_default.yaml's max_tokens_per_run

	t.Run("autonomous keeps the graph cap, not the per-turn ceiling", func(t *testing.T) {
		t.Parallel()

		knobs := autonomy.ResolvedKnobs{
			TokenCeilingPerTurn: 2_097_152, // TierAutonomous preset
			MaxIterations:       0,         // TierAutonomous preset: unbounded
		}
		got := applyTokenCeilingKnob(coreag.Budget{MaxTokensPerRun: graphDeclared}, knobs)

		if got.MaxTokensPerRun == 2_097_152 {
			t.Fatalf("per-turn ceiling leaked into the per-run cap: MaxTokensPerRun = %d; "+
				"this is the exact shape of the reported failure", got.MaxTokensPerRun)
		}
		if got.MaxTokensPerRun != graphDeclared {
			t.Errorf("MaxTokensPerRun = %d, want the graph's declared %d "+
				"(unbounded turns => the knob expresses no per-run opinion)",
				got.MaxTokensPerRun, graphDeclared)
		}
	})

	t.Run("bounded tiers derive perTurn*maxIterations", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name       string
			perTurn    int
			iters      int
			wantPerRun int
		}{
			// Every one of these is strictly LARGER than the pre-fix value
			// (which was perTurn alone), so the fix cannot newly cap anyone.
			{"strict", 8_192, 5, 40_960},
			{"cautious", 32_768, 15, 491_520},
			{"default", 131_072, 40, 5_242_880},
			// bold's product (52,428,800) exceeds the graph cap and must clamp.
			{"bold clamps to graph", 524_288, 100, graphDeclared},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				knobs := autonomy.ResolvedKnobs{TokenCeilingPerTurn: tc.perTurn, MaxIterations: tc.iters}
				got := applyTokenCeilingKnob(coreag.Budget{MaxTokensPerRun: graphDeclared}, knobs)
				if got.MaxTokensPerRun != tc.wantPerRun {
					t.Errorf("MaxTokensPerRun = %d, want %d", got.MaxTokensPerRun, tc.wantPerRun)
				}
			})
		}
	})

	t.Run("never raises the graph's declared cap", func(t *testing.T) {
		t.Parallel()

		// A graph that declared a deliberately tight cap must keep it: a
		// Settings dial must not defeat the graph author's safety limit.
		const tight = 1_000
		knobs := autonomy.ResolvedKnobs{TokenCeilingPerTurn: 131_072, MaxIterations: 40}
		got := applyTokenCeilingKnob(coreag.Budget{MaxTokensPerRun: tight}, knobs)
		if got.MaxTokensPerRun != tight {
			t.Errorf("MaxTokensPerRun = %d, want %d — the dial may only LOWER, never raise",
				got.MaxTokensPerRun, tight)
		}
	})

	t.Run("zero ceiling is no opinion", func(t *testing.T) {
		t.Parallel()

		knobs := autonomy.ResolvedKnobs{TokenCeilingPerTurn: 0, MaxIterations: 40}
		got := applyTokenCeilingKnob(coreag.Budget{MaxTokensPerRun: graphDeclared}, knobs)
		if got.MaxTokensPerRun != graphDeclared {
			t.Errorf("MaxTokensPerRun = %d, want %d", got.MaxTokensPerRun, graphDeclared)
		}
	})

	t.Run("overflowing product must not read as no-cap", func(t *testing.T) {
		t.Parallel()

		// A wrapped-negative product would fall into the "graph has no cap"
		// branch and silently REMOVE the limit, which is worse than the bug
		// being fixed. Treated as "no opinion" instead.
		knobs := autonomy.ResolvedKnobs{TokenCeilingPerTurn: 1 << 40, MaxIterations: 1 << 24}
		got := applyTokenCeilingKnob(coreag.Budget{MaxTokensPerRun: graphDeclared}, knobs)
		if got.MaxTokensPerRun != graphDeclared {
			t.Errorf("MaxTokensPerRun = %d, want the graph's %d preserved", got.MaxTokensPerRun, graphDeclared)
		}
	})
}
