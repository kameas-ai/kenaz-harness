package fleet_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// TestOTLPBaseURL derives the ingest endpoint from the API host carried by
// the resolved fleet config — never from the dashboard origin.
func TestOTLPBaseURL(t *testing.T) {
	tests := []struct {
		name string
		cfg  fleet.FleetConfig
		want string
	}{
		{
			name: "api host gains the /otlp prefix",
			cfg:  fleet.FleetConfig{APIBaseURL: "https://api.fleet.kameas.ai"},
			want: "https://api.fleet.kameas.ai/otlp",
		},
		{
			name: "trailing slash is normalised away",
			cfg:  fleet.FleetConfig{APIBaseURL: "https://api.fleet.kameas.ai/"},
			want: "https://api.fleet.kameas.ai/otlp",
		},
		{
			name: "local dev api host",
			cfg:  fleet.FleetConfig{APIBaseURL: "http://localhost:8090"},
			want: "http://localhost:8090/otlp",
		},
		{
			name: "unresolved config yields no endpoint",
			cfg:  fleet.FleetConfig{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fleet.OTLPBaseURL(tt.cfg); got != tt.want {
				t.Errorf("OTLPBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestOTLPBaseURL_IsNotTheDashboardOrigin is the regression guard for the
// silent-telemetry-loss bug.
//
// FleetBaseURL is the dashboard origin: CloudFront in front of an S3 SPA
// bucket, which returns 200 + index.html for every path. Deriving the OTLP
// endpoint from it meant every export "succeeded" and every span was thrown
// away. The ingest endpoint must come from the API host instead, and the two
// hostnames must not be conflated.
func TestOTLPBaseURL_IsNotTheDashboardOrigin(t *testing.T) {
	const (
		dashboardHost = "https://fleet.kameas.ai"
		apiHost       = "https://api.fleet.kameas.ai"
	)

	// The profile the user configures still points at the dashboard...
	profile := fleet.EnvProfile{Name: "prod", FleetBaseURL: dashboardHost}
	// ...while the config discovered from it names a different API host.
	cfg := fleet.FleetConfig{APIBaseURL: apiHost}

	got := fleet.OTLPBaseURL(cfg)

	if !strings.HasPrefix(got, apiHost) {
		t.Errorf("OTLP endpoint %q should be rooted at the API host %q", got, apiHost)
	}
	if strings.HasPrefix(got, profile.FleetBaseURL) {
		t.Errorf("OTLP endpoint %q is rooted at the dashboard origin %q — "+
			"telemetry sent there hits the SPA bucket and is silently discarded",
			got, profile.FleetBaseURL)
	}
}

// TestConfigDiscoveryStillUsesDashboardOrigin pins the other half of the
// separation. The two uses are distinct and both must stay correct: the
// dashboard origin is still the right input to /config.json discovery — that
// is how the API host is learned in the first place — so this could not be
// fixed by repointing a single string.
func TestConfigDiscoveryStillUsesDashboardOrigin(t *testing.T) {
	const apiHost = "https://api.fleet.example.com"

	var configPath string
	dashboard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		configPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"api_base_url":"` + apiHost + `"}`))
	}))
	defer dashboard.Close()

	cfg, err := fleet.FetchFleetConfig(context.Background(), dashboard.URL)
	if err != nil {
		t.Fatalf("FetchFleetConfig: %v", err)
	}

	if configPath != "/config.json" {
		t.Errorf("discovery fetched %q, want /config.json from the dashboard origin", configPath)
	}
	if cfg.APIBaseURL != apiHost {
		t.Errorf("APIBaseURL = %q, want %q", cfg.APIBaseURL, apiHost)
	}

	// And the endpoint the pipeline will use follows the discovered API host,
	// not the dashboard host it was discovered from.
	if want := apiHost + "/otlp"; fleet.OTLPBaseURL(cfg) != want {
		t.Errorf("OTLPBaseURL() = %q, want %q", fleet.OTLPBaseURL(cfg), want)
	}
}
