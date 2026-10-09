// tool_exposure_knob_coverage.go registers the three tool-exposure
// fields of settings.Settings with core/wiring/knobcoverage
// (tool-context-budget-01TCBUD01).
//
// Each reaches the request: ToolExposure through toolexposure.Resolve's
// user layer (a summary or off tier keeps a schema out of the call), the
// schema budget through the eviction that fits each call's tools to it,
// and the activation TTL through the expiry that drops unused
// activations at a turn's start.
package rpc

import (
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
)

func init() {
	knobcoverage.Register[settings.Settings](
		"ToolExposure",
		"chat.exposureTurn.selectTools (via toolexposure.Resolve's user layer): "+
			"a summary or off tier keeps a tool's schema out of the request",
	)
	knobcoverage.Register[settings.Settings](
		"ToolSchemaBudgetTokens",
		"chat.exposureTurn.selectTools: toolexposure.EffectiveBudget(setting, window) "+
			"is the budget Partition.FitBudget evicts activated, then pinned, tools "+
			"against, so a lower budget leaves loaded tools out of the request",
	)
	knobcoverage.Register[settings.Settings](
		"ToolActivationTTLTurns",
		"chat.exposureTurn.beginTurn: loadtools.Service.ExpireActivations drops "+
			"non-sticky activations unused for more than TTL turns, so their "+
			"schemas leave the request and their servers return to the digest",
	)
}
