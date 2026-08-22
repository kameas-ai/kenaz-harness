// harness_self_mcp_disabled_knob_coverage.go registers
// settings.Settings.HarnessSelfMCPDisabled with core/wiring/knobcoverage
// (harness-self-attach-01PMHS01 UNIT-7).
//
// controls-and-readouts-that-tell-the-truth-01PMZ808 UNIT-17 is the
// mission that brings the whole settings.Settings struct (~78 exported
// fields) under knobcoverage in one pass (see G-4,
// docs/escalation-register-2026-08-19.md Part 9, "HS01 §10.4"), with a
// TestKnobCoverage_Settings asserting knobcoverage.Uncovered[settings.
// Settings]() is empty. That test does not exist yet in this tree — no
// other field of settings.Settings is registered as of this commit — so
// this single registration cannot be verified end-to-end until UNIT-17
// lands. It is added now, ahead of that mission, specifically so UNIT-17
// does not have to discover this field cold: whichever of the two units
// lands second must not double-register "HarnessSelfMCPDisabled" (Register
// panics on a duplicate field) — UNIT-17's own registration pass must
// skip this field or import/reuse this file's registration.
//
// The consumer is onboardingSettingsDialAdapter.IsHarnessSelfMCPDisabled
// (core/rpc/onboarding_wiring.go), which reads the field live via
// settings.SettingsStore.LoadHarnessSelfMCPDisabled on every
// OnboardingAPI.State() call.
package rpc

import (
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
)

func init() {
	knobcoverage.Register[settings.Settings](
		"HarnessSelfMCPDisabled",
		"core/rpc/onboarding_wiring.go onboardingSettingsDialAdapter.IsHarnessSelfMCPDisabled "+
			"(harness-self-attach-01PMHS01 UNIT-7); read live via settings.SettingsStore."+
			"LoadHarnessSelfMCPDisabled on every OnboardingAPI.State() call",
	)
}
