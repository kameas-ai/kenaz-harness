package serve

// Connectors_List / Connectors_Status — the served-mode read-only
// connector surface (spec 091 D11, FR-010).
//
// Both methods are projections over the connector supervisor's boot
// outcomes joined with the live MCP health snapshot. They carry metadata
// only: ids, display names, states, reason classes. No credential
// material, no tool arguments, no recipe bodies.

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/mcp/connectors"
	mcpview "github.com/kameas-ai/kenaz-harness/core/rpc/views/mcp"
)

// runningState mirrors transport.StateRunning; a connector is "healthy"
// when its pool entry reports this state.
const runningState = "running"

// ConnectorEntry is one row of the Connectors_List result.
type ConnectorEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Category    string `json:"category,omitempty"`
	Transport   string `json:"transport,omitempty"`
	// Enabled is true when the connector resolved in the embedded catalog
	// and was enabled at boot. A whitelisted id missing from the image's
	// catalog (version skew) renders enabled:false — "connector
	// unavailable", per the ADR's warn-and-drop rule.
	Enabled bool `json:"enabled"`
	// Healthy is true when the live pool reports the server running.
	Healthy bool `json:"healthy"`
}

// ConnectorsListResult is the Connectors_List response shape.
type ConnectorsListResult struct {
	// Provisioned is false when the boot saw no well-formed whitelist
	// (absent, empty, or malformed KENAZ_MCP_ALLOWLIST — all block-all).
	// The UI distinguishes "connectors not provisioned" from "operator
	// whitelisted nothing that works".
	Provisioned bool             `json:"provisioned"`
	Connectors  []ConnectorEntry `json:"connectors"`
}

// ConnectorStatusEntry is one row of the Connectors_Status result: the
// boot outcome plus, when the pool tracks the server, its live health.
type ConnectorStatusEntry struct {
	connectors.State
	Health *mcpview.HealthEntry `json:"health,omitempty"`
}

// ConnectorsStatusResult is the Connectors_Status response shape.
type ConnectorsStatusResult struct {
	Provisioned bool                   `json:"provisioned"`
	Connectors  []ConnectorStatusEntry `json:"connectors"`
}

// healthSnapshot returns the live per-recipe health map, or nil when the
// surface is unavailable (nil-core test chassis).
func (s *Server) healthSnapshot(ctx context.Context) map[string]mcpview.HealthEntry {
	if s.api == nil {
		return nil
	}
	mcpAPI := s.api.MCP()
	if mcpAPI == nil {
		return nil
	}
	snap, err := mcpAPI.HealthSnapshot(ctx)
	if err != nil {
		s.log.Warn("serve: connector health snapshot failed", "err", err.Error())
		return nil
	}
	return snap
}

func (s *Server) connectorsList(ctx context.Context) ConnectorsListResult {
	out := ConnectorsListResult{Connectors: []ConnectorEntry{}}
	if s.connectors == nil {
		return out
	}
	out.Provisioned = s.connectors.Provisioning().Provisioned
	health := s.healthSnapshot(ctx)
	for _, st := range s.connectors.States() {
		entry := ConnectorEntry{
			ID:          st.ID,
			DisplayName: st.DisplayName,
			Category:    st.Category,
			Transport:   st.Transport,
			Enabled:     st.Enabled,
		}
		if h, ok := health[st.ID]; ok {
			entry.Healthy = h.State == runningState
		}
		out.Connectors = append(out.Connectors, entry)
	}
	return out
}

func (s *Server) connectorsStatus(ctx context.Context) ConnectorsStatusResult {
	out := ConnectorsStatusResult{Connectors: []ConnectorStatusEntry{}}
	if s.connectors == nil {
		return out
	}
	out.Provisioned = s.connectors.Provisioning().Provisioned
	health := s.healthSnapshot(ctx)
	for _, st := range s.connectors.States() {
		entry := ConnectorStatusEntry{State: st}
		if h, ok := health[st.ID]; ok {
			hcopy := h
			entry.Health = &hcopy
		}
		out.Connectors = append(out.Connectors, entry)
	}
	return out
}
