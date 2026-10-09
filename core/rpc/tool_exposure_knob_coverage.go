// tool_exposure_knob_coverage.go registers the three tool-exposure
// fields of settings.Settings with core/wiring/knobcoverage
// (tool-context-budget-01TCBUD01).
//
// ToolExposure reaches the request: toolexposure.Resolve folds it into
// each tool's tier (step 4, user layer) and the chat request builder
// sends only full and activated tools. The schema budget and the
// activation TTL are resolved onto ResolvedCatalog but nothing evicts or
// expires against them yet: each is RegisterDeferred, naming the WP that
// wires its consumer.
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
