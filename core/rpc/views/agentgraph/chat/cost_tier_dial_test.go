package chat

import (
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

// The cost ceiling is a tier dial that is DELIBERATELY DISABLED at
// TierAutonomous (owner ruling 2026-09-12: "it should exist as a dial but
// in autonomous it should be disabled").
//
// Both halves matter and each would be a product bug on its own:
//   - if autonomous acquired a cost cap, an hours-long run would be stopped
//     by spend, which is the thing the ruling exists to prevent;
//   - if the lower tiers had NO cost cap, nothing would bound spend at all,
//     because the graphs declare no max_cost_usd_per_run and the token
//     backstop is now deliberately sized never to bind first.
func TestApplyBudgetTierDial_CostCeiling(t *testing.T) {
	t.Parallel()

	t.Run("autonomous leaves cost capping disabled", func(t *testing.T) {
		t.Parallel()

		// The graphs declare no cost cap, so zero in means zero out.
		got := applyBudgetTierDial(coreag.Budget{}, autonomy.TierAutonomous)
		if got.MaxCostUSDPerRun != 0 {
			t.Fatalf("MaxCostUSDPerRun = %v, want 0 (disabled at autonomous). "+
				"Kernel.checkBudget only enforces this cap when it is > 0, so any "+
				"non-zero value here reintroduces a spend stop on hours-long runs",
				got.MaxCostUSDPerRun)
		}
	})

	t.Run("lower tiers establish a cost cap", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			tier autonomy.Tier
			want float64
		}{
			{autonomy.TierStrict, 1},
			{autonomy.TierCautious, 5},
			{autonomy.TierDefault, 15},
			{autonomy.TierBold, 50},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.tier.String(), func(t *testing.T) {
				t.Parallel()
				got := applyBudgetTierDial(coreag.Budget{}, tc.tier)
				if got.MaxCostUSDPerRun != tc.want {
					t.Errorf("MaxCostUSDPerRun = %v, want %v", got.MaxCostUSDPerRun, tc.want)
				}
			})
		}
	})

	t.Run("ladder is monotonic and autonomous is the only disabled tier", func(t *testing.T) {
		t.Parallel()

		order := []autonomy.Tier{
			autonomy.TierStrict, autonomy.TierCautious,
			autonomy.TierDefault, autonomy.TierBold,
		}
		prev := 0.0
		for _, tr := range order {
			c := autonomy.BudgetCeilingForTier(tr).MaxCostUSDPerRun
			if c <= prev {
				t.Errorf("tier %s cost ceiling %v is not greater than the previous tier's %v — "+
					"a non-monotonic ladder means raising autonomy could tighten the spend guard",
					tr.String(), c, prev)
			}
			prev = c
		}
		if autonomy.BudgetCeilingForTier(autonomy.TierAutonomous).MaxCostUSDPerRun != 0 {
			t.Error("autonomous must be the disabled entry")
		}
	})

	t.Run("dial may not raise a graph-declared cost cap", func(t *testing.T) {
		t.Parallel()

		// A graph that declared a tight cost cap keeps it: the same
		// only-lower rule the call-volume and token dials follow.
		got := applyBudgetTierDial(coreag.Budget{MaxCostUSDPerRun: 2}, autonomy.TierBold)
		if got.MaxCostUSDPerRun != 2 {
			t.Errorf("MaxCostUSDPerRun = %v, want the graph's 2 to hold", got.MaxCostUSDPerRun)
		}
	})

	t.Run("cost dial does not disturb the call-volume caps", func(t *testing.T) {
		t.Parallel()

		got := applyBudgetTierDial(coreag.Budget{
			MaxLLMCallsPerRun:  5000,
			MaxToolCallsPerRun: 10000,
		}, autonomy.TierAutonomous)
		if got.MaxLLMCallsPerRun != 5000 || got.MaxToolCallsPerRun != 10000 {
			t.Errorf("call caps disturbed: %+v", got)
		}
	})
}
