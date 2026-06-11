package fleet

import (
	"fmt"
	"time"
)

// Capability is a named feature gate returned by the fleet capability endpoint.
// Each constant maps to the snake_case JSON key documented in
// orgs-tiers-billing-design.md §2.
type Capability string

// The 25 capability keys (fleet-capability-surface-01NDFSEX09).
// Wire values are the snake_case strings the backend emits; the Go
// constants use UpperCamelCase with a Cap prefix.
const (
	CapHostedInference          Capability = "hosted_inference"
	CapLauncherUpdates          Capability = "launcher_updates"
	CapISODistribution          Capability = "iso_distribution"
	CapKameasMLGeneral          Capability = "kameas_ml_general"
	CapKameasMLTeamTuned        Capability = "kameas_ml_team_tuned"
	CapKameasMLOrgTuned         Capability = "kameas_ml_org_tuned"
	CapSharedTeamGraph          Capability = "shared_team_graph"
	CapCrossTeamGraphIsolation  Capability = "cross_team_graph_isolation"
	CapOPAPresetPolicies        Capability = "opa_preset_policies"
	CapOPACustomRego            Capability = "opa_custom_rego"
	CapAttestationTPM           Capability = "attestation_tpm"
	CapAuditLogImmudb           Capability = "audit_log_immudb"
	CapFalcoMonitoring          Capability = "falco_monitoring"
	CapZeekNetworkVerify        Capability = "zeek_network_verify"
	CapEmergencyLockdown        Capability = "emergency_lockdown"
	CapKameasVerifyAttestation  Capability = "kameas_verify_attestation"
	CapPersonalFleetDashboard   Capability = "personal_fleet_dashboard"
	CapTeamAdminDashboard       Capability = "team_admin_dashboard"
	CapPolicyBundleDistribution Capability = "policy_bundle_distribution"
	CapIsolatedFleetInfra       Capability = "isolated_fleet_infra"
	CapSSOSAML                  Capability = "sso_saml"
	CapComplianceDocsSoC2Sum    Capability = "compliance_docs_soc2_summary"
	CapComplianceDocsSoC2Full   Capability = "compliance_docs_soc2_full"
	CapComplianceDocsCustom     Capability = "compliance_docs_custom"
	CapQuarterlyAttestationReports Capability = "quarterly_attestation_reports"

	// CapSitesHosting gates the Fleet Sites feature (Enterprise tier only).
	// Wire value: "sites_hosting" (pinned in kenaz-fleet/docs/contract-harness-sites.md §0).
	// Mission: sites-foundation-01NSITE04, WP01.
	CapSitesHosting Capability = "sites_hosting"
)

// AllCapabilities returns every known Capability constant in declaration
// order. Used by parity checks, codegen, and tests.
func AllCapabilities() []Capability {
	return []Capability{
		CapHostedInference,
		CapLauncherUpdates,
		CapISODistribution,
		CapKameasMLGeneral,
		CapKameasMLTeamTuned,
		CapKameasMLOrgTuned,
		CapSharedTeamGraph,
		CapCrossTeamGraphIsolation,
		CapOPAPresetPolicies,
		CapOPACustomRego,
		CapAttestationTPM,
		CapAuditLogImmudb,
		CapFalcoMonitoring,
		CapZeekNetworkVerify,
		CapEmergencyLockdown,
		CapKameasVerifyAttestation,
		CapPersonalFleetDashboard,
		CapTeamAdminDashboard,
		CapPolicyBundleDistribution,
		CapIsolatedFleetInfra,
		CapSSOSAML,
		CapComplianceDocsSoC2Sum,
		CapComplianceDocsSoC2Full,
		CapComplianceDocsCustom,
		CapQuarterlyAttestationReports,
		CapSitesHosting,
	}
}

// capabilityTTL is the maximum age of a Capabilities snapshot before
// Has() treats every key as disabled.
const capabilityTTL = 24 * time.Hour

// Capabilities holds the current tier + feature gate state for the
// signed-in user. It is fetched from fleet and cached locally.
//
// Source values:
//   - "fleet"        — fresh fetch from GET /api/v1/me/capabilities
//   - "cache"        — restored from the disk cache
//   - "default-deny" — offline, signed-out, or fleet not yet returning the endpoint
type Capabilities struct {
	Tier      string          `json:"tier"`
	Enabled   map[Capability]bool `json:"capabilities"`
	FetchedAt time.Time       `json:"fetched_at"`
	// Source records where this snapshot came from.
	// Not sent by the wire protocol; populated by the harness on load.
	Source string `json:"source,omitempty"`
}

// Has reports true when the capability is explicitly enabled AND the
// snapshot is less than 24 hours old. Unknown keys return false without panic.
func (c *Capabilities) Has(key Capability) bool {
	if c == nil {
		return false
	}
	if c.Enabled == nil {
		return false
	}
	if time.Since(c.FetchedAt) >= capabilityTTL {
		return false
	}
	return c.Enabled[key]
}

// Require returns nil when Has(key) is true, or a wrapped
// ErrCapabilityNotInTier that names the missing capability.
func (c *Capabilities) Require(key Capability) error {
	if c.Has(key) {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrCapabilityNotInTier, key)
}

// DefaultDenyCapabilities returns a Capabilities value that denies every
// capability. Used when fleet is disabled or the user is signed out.
func DefaultDenyCapabilities() Capabilities {
	return Capabilities{
		Tier:      "",
		Enabled:   map[Capability]bool{},
		FetchedAt: time.Time{}, // zero — treated as stale
		Source:    "default-deny",
	}
}
