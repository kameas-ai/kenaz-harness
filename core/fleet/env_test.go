package fleet

import (
	"testing"
)

func TestResolveProfile_Matrix(t *testing.T) {
	tests := []struct {
		name       string
		envName    string
		envIssuer  string
		envClient  string
		envAud     string
		envBase    string
		wantName   string
		wantColor  string
	}{
		{
			name:      "unset → prod",
			envName:   "",
			wantName:  EnvProd,
			wantColor: "",
		},
		{
			name:      "prod → prod",
			envName:   "prod",
			wantName:  EnvProd,
			wantColor: "",
		},
		{
			name:      "dev → dev",
			envName:   "dev",
			wantName:  EnvDev,
			wantColor: "yellow",
		},
		{
			name:      "stage → stage",
			envName:   "stage",
			wantName:  EnvStage,
			wantColor: "blue",
		},
		{
			name:       "local with overrides",
			envName:    "local",
			envIssuer:  "http://custom-issuer:8080",
			envClient:  "my-client-id",
			envAud:     "my-audience",
			envBase:    "http://custom-fleet:9000",
			wantName:   EnvLocal,
			wantColor:  "red",
		},
		{
			name:      "local without overrides → defaults",
			envName:   "local",
			wantName:  EnvLocal,
			wantColor: "red",
		},
		{
			name:      "banana (unrecognized) → prod",
			envName:   "banana",
			wantName:  EnvProd,
			wantColor: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("KENAZ_HARNESS_ENV", tt.envName)
			if tt.envIssuer != "" {
				t.Setenv("HARNESS_FLEET_ISSUER", tt.envIssuer)
			}
			if tt.envClient != "" {
				t.Setenv("HARNESS_FLEET_CLIENT_ID", tt.envClient)
			}
			if tt.envAud != "" {
				t.Setenv("HARNESS_FLEET_AUDIENCE", tt.envAud)
			}
			if tt.envBase != "" {
				t.Setenv("HARNESS_FLEET_BASE_URL", tt.envBase)
			}

			p := ResolveProfile()
			if p.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", p.Name, tt.wantName)
			}
			if p.BadgeColor() != tt.wantColor {
				t.Errorf("BadgeColor = %q, want %q", p.BadgeColor(), tt.wantColor)
			}
			if tt.envIssuer != "" && p.ZitadelIssuer != tt.envIssuer {
				t.Errorf("ZitadelIssuer = %q, want %q", p.ZitadelIssuer, tt.envIssuer)
			}
			if tt.envBase != "" && p.FleetBaseURL != tt.envBase {
				t.Errorf("FleetBaseURL = %q, want %q", p.FleetBaseURL, tt.envBase)
			}
			// Scopes must always be set.
			if len(p.OIDCScopes) == 0 {
				t.Error("OIDCScopes is empty")
			}
		})
	}
}

func TestResolveProfile_LocalDefaults(t *testing.T) {
	t.Setenv("KENAZ_HARNESS_ENV", "local")
	// Clear overrides.
	t.Setenv("HARNESS_FLEET_ISSUER", "")
	t.Setenv("HARNESS_FLEET_BASE_URL", "")

	p := ResolveProfile()
	if p.FleetBaseURL != "http://localhost:8090" {
		t.Errorf("default FleetBaseURL = %q, want http://localhost:8090", p.FleetBaseURL)
	}
	if p.ZitadelIssuer != "http://localhost:8080" {
		t.Errorf("default ZitadelIssuer = %q, want http://localhost:8080", p.ZitadelIssuer)
	}
}

func TestEnvProfile_Configured(t *testing.T) {
	empty := EnvProfile{}
	if empty.Configured() {
		t.Error("empty profile should not be Configured()")
	}
	full := EnvProfile{
		ZitadelIssuer:  "https://issuer.example.com",
		NativeClientID: "native-client-id",
		FleetBaseURL:   "https://fleet.example.com",
	}
	if !full.Configured() {
		t.Error("fully populated profile should be Configured()")
	}
	partial := EnvProfile{
		ZitadelIssuer: "https://issuer.example.com",
	}
	if partial.Configured() {
		t.Error("partial profile (missing ClientID+BaseURL) should not be Configured()")
	}
}
