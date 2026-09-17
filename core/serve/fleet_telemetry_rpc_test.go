package serve_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func servedRPC(t *testing.T, baseURL, body string) (result json.RawMessage, rpcErr string) {
	t.Helper()
	resp, err := http.Post(baseURL+"/rpc", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /rpc: %v", err)
	}
	defer resp.Body.Close()
	var env struct {
		Error  string          `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env.Result, env.Error
}

// A workbench user must be able to read and change consent. These were
// desktop-only, which pinned every workbench at "none" forever.
func TestServedRPC_TelemetryConsentIsReachable(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "")
	defer cancel()

	res, rpcErr := servedRPC(t, baseURL, `{"method":"Fleet_GetTelemetryConsent","params":{}}`)
	if rpcErr != "" {
		t.Fatalf("Fleet_GetTelemetryConsent: %s", rpcErr)
	}
	if string(res) != `"none"` {
		t.Errorf("default consent = %s, want \"none\"", res)
	}

	// Tier gate still applies in served mode: the test chassis is tier=free.
	_, rpcErr = servedRPC(t, baseURL, `{"method":"Fleet_SetTelemetryConsent","params":{"level":"full"}}`)
	if rpcErr == "" || strings.Contains(rpcErr, "not ported") {
		t.Errorf("full consent on a free tier: err=%q, want the tier-gate refusal", rpcErr)
	}
	_, rpcErr = servedRPC(t, baseURL, `{"method":"Fleet_SetTelemetryConsent","params":{"level":"bogus"}}`)
	if rpcErr == "" {
		t.Error("an unknown level was accepted")
	}
	_, rpcErr = servedRPC(t, baseURL, `{"method":"Fleet_SetTelemetryConsent","params":{"level":"none"}}`)
	if rpcErr != "" {
		t.Errorf("setting none: %s", rpcErr)
	}
}

func TestServedRPC_TelemetryStatus_IsPayloadFree(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "")
	defer cancel()
	res, rpcErr := servedRPC(t, baseURL, `{"method":"Fleet_TelemetryStatus","params":{}}`)
	if rpcErr != "" {
		t.Fatalf("Fleet_TelemetryStatus: %s", rpcErr)
	}
	var st map[string]any
	if err := json.Unmarshal(res, &st); err != nil {
		t.Fatalf("decode: %v (%s)", err, res)
	}
	if st["effective_consent"] != "none" || st["enrolled"] != false {
		t.Errorf("status = %s", res)
	}
	for k := range st {
		switch k {
		case "wired", "enrolled", "stored_consent", "effective_consent", "org_tier",
			"open_conversations", "pipeline", "enroll",
			"opted_in_classes", "preferences_fetched_at":
		default:
			t.Errorf("unexpected status field %q — the surface must stay payload-free", k)
		}
	}
}
