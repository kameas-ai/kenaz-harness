// Package connectors implements the served-mode MCP connector control
// plane for spec 091 (App Library MCP Connector Registry).
//
// A Kenaz workbench profile whitelists connector recipe ids on the host;
// the ids reach the served harness as the KENAZ_MCP_ALLOWLIST environment
// variable (comma-joined, KENAZMETA → EnvironmentFile seam). Served mode
// INVERTS the harness's host-mode default (nil allow-list = unrestricted):
// an absent, empty, or malformed value means BLOCK-ALL — fail closed, per
// spec 091 FR-004 and ADR-connector-catalog-consumption §5.
//
// The package owns:
//   - Provisioning: strict parsing of KENAZ_MCP_ALLOWLIST and the
//     block-all application through recipes.ApplyServedAllowlist.
//   - Supervisor: boot-time resolution of whitelisted ids against the
//     embedded catalog, per-recipe env de-namespacing, spawn onto the
//     dispatch pool, and the connector.* ledger events (FR-014).
package connectors

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// EnvMCPAllowlist is the environment variable carrying the comma-joined
// whitelisted recipe ids. Written by the Kenaz host onto the KENAZMETA
// disk; ids only, never recipe bodies (ADR-connector-catalog-consumption §4).
const EnvMCPAllowlist = "KENAZ_MCP_ALLOWLIST"

// Provisioning reason classes. Ids and counts are loggable; the raw env
// value is NOT (a malformed value could carry a mis-pasted secret).
const (
	// ReasonNotProvisioned — the env var is absent or empty. This is the
	// COMMON case (omitempty profile round-trip), not the corner: every
	// legacy image and every profile without connectors boots this way.
	ReasonNotProvisioned = "not_provisioned"
	// ReasonMalformed — the env var is present but at least one id fails
	// validation. The whole list is refused (block-all), never partially
	// applied: a value we cannot fully parse is a value we do not trust.
	ReasonMalformed = "malformed"
)

// Provisioning is the outcome of parsing KENAZ_MCP_ALLOWLIST.
type Provisioning struct {
	// Provisioned is true only when a well-formed, non-empty whitelist
	// was supplied. False means block-all is in force and the UI should
	// surface "connectors not provisioned".
	Provisioned bool
	// Reason is "" when Provisioned, else one of the Reason* classes.
	Reason string
	// IDs is the parsed whitelist in first-seen order, de-duplicated.
	// Empty unless Provisioned.
	IDs []string
}

// ParseAllowlist strictly parses a KENAZ_MCP_ALLOWLIST value. Each
// comma-separated entry is trimmed and validated as a recipe id
// (^[a-z][a-z0-9-]{0,63}$ via recipes.ValidateRecipeID). An empty value or
// ANY malformed entry yields the block-all outcome — the list is never
// partially applied.
func ParseAllowlist(value string) Provisioning {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return Provisioning{Reason: ReasonNotProvisioned}
	}
	parts := strings.Split(trimmed, ",")
	seen := make(map[string]bool, len(parts))
	ids := make([]string, 0, len(parts))
	for _, p := range parts {
		id := strings.TrimSpace(p)
		if err := recipes.ValidateRecipeID(id); err != nil {
			return Provisioning{Reason: ReasonMalformed}
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return Provisioning{Provisioned: true, IDs: ids}
}

// ProvisionFromEnv reads KENAZ_MCP_ALLOWLIST via getenv, parses it
// strictly, and installs the outcome as the process-global served
// allow-list — block-all unless a well-formed non-empty whitelist was
// supplied. It MUST run before any recipe load or bootstrap (both served
// entry points call it before core boot).
//
// Logging: id count and reason class only. Never the raw value — a
// malformed value could carry credential bytes.
func ProvisionFromEnv(getenv func(string) string, log *slog.Logger) Provisioning {
	if log == nil {
		log = slog.Default()
	}
	prov := ParseAllowlist(getenv(EnvMCPAllowlist))
	if !prov.Provisioned {
		recipes.ApplyServedAllowlist([]string{})
		switch prov.Reason {
		case ReasonMalformed:
			log.Warn("connectors: KENAZ_MCP_ALLOWLIST is malformed — block-all in force")
		default:
			log.Info("connectors: not provisioned — block-all in force")
		}
		return prov
	}
	recipes.ApplyServedAllowlist(prov.IDs)
	log.Info(fmt.Sprintf("connectors: allow-list provisioned (%d connector(s))", len(prov.IDs)))
	return prov
}
