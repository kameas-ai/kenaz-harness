package serve_test

import (
	"encoding/json"
	"testing"
)

// fleet-policy push-down: the in-VM UI must be able to read which fleet
// bundle is applied and whether fleet rules are in force. Both methods were
// desktop-only, so the served FleetHealthChip could never render.
func TestServedRPC_FleetPolicyStatusIsReachable(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "")
	defer cancel()

	for _, method := range []string{"Settings_FleetConfigPullStatus", "Settings_FleetHealth"} {
		res, rpcErr := servedRPC(t, baseURL, `{"method":"`+method+`","params":{}}`)
		if rpcErr != "" {
			t.Fatalf("%s: %s", method, rpcErr)
		}
		var st map[string]any
		if err := json.Unmarshal(res, &st); err != nil {
			t.Fatalf("%s decode: %v (%s)", method, err, res)
		}
		for _, k := range []string{"lastAppliedId", "lastAppliedAt", "fleetRulesActive", "fleetCedarRules", "mcpAllowlistActive", "configDistributionEnabled"} {
			if _, ok := st[k]; !ok {
				t.Errorf("%s: missing field %q in %s", method, k, res)
			}
		}
		if _, ok := st["lastError"]; !ok {
			if _, ok2 := st["configLastError"]; !ok2 {
				t.Errorf("%s: no last-error field in %s", method, res)
			}
		}
		if st["fleetRulesActive"] != false {
			t.Errorf("%s: fleetRulesActive on a fresh chassis = %v", method, st["fleetRulesActive"])
		}
	}
}
