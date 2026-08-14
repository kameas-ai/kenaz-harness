package fleet

import (
	"testing"
)

func TestResolveProfile_Matrix(t *testing.T) {
	tests := []struct {
		name       string
		envName    string
		wantName   string
		wantColor  string
		wantIssuer string
		wantFleet  string
	}{
		{
			name:       "unset → prod",
			envName:    "",
			wantName:   EnvProd,
			wantColor:  "",
			wantIssuer: prodIssuer,
			wantFleet:  prodFleetURL,
		},
		{
			name:       "prod → prod",
			envName:    "prod",
			wantName:   EnvProd,
			wantColor:  "",
			wantIssuer: prodIssuer,
			wantFleet:  prodFleetURL,
		},
		{
			name:       "dev → dev",
			envName:    "dev",
			wantName:   EnvDev,
			wantColor:  "yellow",
			wantIssuer: devIssuer,
			wantFleet:  devFleetURL,
		},
		{
			name:       "stage → stage",
			envName:    "stage",
			wantName:   EnvStage,
			wantColor:  "blue",
			wantIssuer: stageIssuer,
			wantFleet:  stageFleetURL,
		},
		{
			name:       "local → local",
			envName:    "local",
			wantName:   EnvLocal,
			wantColor:  "red",
			wantIssuer: localIssuer,
			wantFleet:  localFleetURL,
		},
		{
			name:       "banana (unrecognized) → prod",
			envName:    "banana",
			wantName:   EnvProd,
			wantColor:  "",
			wantIssuer: prodIssuer,
			wantFleet:  prodFleetURL,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("KENAZ_HARNESS_ENV", tt.envName)
			p := ResolveProfile()
			if p.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", p.Name, tt.wantName)
			}
			if p.BadgeColor() != tt.wantColor {
				t.Errorf("BadgeColor = %q, want %q", p.BadgeColor(), tt.wantColor)
			}
			if p.ZitadelIssuer != tt.wantIssuer {
				t.Errorf("ZitadelIssuer = %q, want %q", p.ZitadelIssuer, tt.wantIssuer)
			}
			if p.FleetBaseURL != tt.wantFleet {
				t.Errorf("FleetBaseURL = %q, want %q", p.FleetBaseURL, tt.wantFleet)
			}
			if len(p.OIDCScopes) == 0 {
				t.Error("OIDCScopes is empty")
			}
		})
	}
}

// TestResolveProfile_NoPerFieldOverrides verifies that the per-field env
// vars (HARNESS_FLEET_ISSUER, etc.) are NOT honored — the simplified
// single-env-var design means KENAZ_HARNESS_ENV is the only knob.
func TestResolveProfile_NoPerFieldOverrides(t *testing.T) {
	t.Setenv("KENAZ_HARNESS_ENV", "local")
	t.Setenv("HARNESS_FLEET_ISSUER", "http://should-be-ignored")
	t.Setenv("HARNESS_FLEET_CLIENT_ID", "should-be-ignored")
	t.Setenv("HARNESS_FLEET_BASE_URL", "http://should-be-ignored")
	p := ResolveProfile()
	if p.ZitadelIssuer != localIssuer {
		t.Errorf("ZitadelIssuer = %q, want %q (per-field override should not be honored)", p.ZitadelIssuer, localIssuer)
	}
	if p.FleetBaseURL != localFleetURL {
		t.Errorf("FleetBaseURL = %q, want %q (per-field override should not be honored)", p.FleetBaseURL, localFleetURL)
	}
}

// TestResolveProfile_LLEConfigured guards against the release defect where
// every shipped build carried empty LLE client IDs (the ldflags were never
// plumbed in any pipeline), so Configured() was false everywhere and the
// OTLP pipeline was never constructed. The LLE-backed profiles must be
// configured out of the box, with no build-time injection required.
func TestResolveProfile_LLEConfigured(t *testing.T) {
	for _, env := range []string{EnvLocal, EnvDev, EnvStage} {
		t.Run(env, func(t *testing.T) {
			t.Setenv("KENAZ_HARNESS_ENV", env)
			p := ResolveProfile()
			if !p.Configured() {
				t.Errorf("%s profile not Configured(): NativeClientID=%q APIAudience=%q — the fleet emitter cannot start", env, p.NativeClientID, p.APIAudience)
			}
		})
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
