package fleet

// export_test.go exposes internal construction helpers to the external
// fleet_test package (integration_test.go). These symbols are only
// compiled into test binaries and are invisible at production link time.

import (
	"net/http"
	"time"
)

// BuildClientForTesting constructs a *Client that talks to the given
// base URL using the supplied HTTP client. Intended for integration tests
// that need a Client pointing at an httptest.Server without going through
// the OS keychain for profile configuration.
//
// The client is wired with a minimal EnvProfile whose Configured() returns
// true so the poller's fetch path does not short-circuit.
func BuildClientForTesting(baseURL string, httpClient *http.Client) *Client {
	SeedFleetConfigForTesting(baseURL, FleetConfig{
		Issuer:     "https://example.com",
		ClientID:   "client-id",
		APIBaseURL: baseURL,
	})
	return &Client{
		profile: EnvProfile{
			Name:           "test",
			FleetBaseURL:   baseURL,
			ZitadelIssuer:  "https://example.com",
			NativeClientID: "client-id",
			APIAudience:    "aud",
		},
		httpClient:  httpClient,
		httpTimeout: 5 * time.Second,
	}
}
