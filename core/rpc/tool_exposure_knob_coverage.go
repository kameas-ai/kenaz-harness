// tool_exposure_knob_coverage.go registers the three tool-exposure
// fields of settings.Settings with core/wiring/knobcoverage
// (tool-context-budget-01TCBUD01 WP02).
//
// toolexposure.Resolve reads all three (settings.API satisfies
// toolexposure.SettingsSource), but nothing on the request path calls
// Resolve yet, so no field reaches an observable behaviour: each is
// RegisterDeferred, naming the WP that wires its consumer.
package rpc

import (
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
)

func init() {
	knobcoverage.RegisterDeferred[settings.Settings](
		"ToolExposure",
		"NOT yet consumed: folded into each tool's tier by "+
			"toolexposure.Resolve (step 4, user layer), whose request-builder "+
			"caller lands with tool-context-budget-01TCBUD01 WP03 (builder "+
			"partitions the resolved catalog by tier). Dated 2026-10-08. "+
			"Owner: alec.",
	)
	knobcoverage.RegisterDeferred[settings.Settings](
		"ToolSchemaBudgetTokens",
		"NOT yet consumed: resolved onto ResolvedCatalog.SchemaBudgetTokens "+
			"by toolexposure.Resolve; eviction against it lands with "+
			"tool-context-budget-01TCBUD01 WP04. Dated 2026-10-08. Owner: alec.",
	)
	knobcoverage.RegisterDeferred[settings.Settings](
		"ToolActivationTTLTurns",
		"NOT yet consumed: resolved onto ResolvedCatalog.ActivationTTLTurns "+
			"by toolexposure.Resolve; activation expiry against it lands with "+
			"tool-context-budget-01TCBUD01 WP04. Dated 2026-10-08. Owner: alec.",
	)
}
