package fleet

import (
	"fmt"
	"os"
	"strings"
)

// EnvProfile holds the Zitadel and fleet-server coordinates for a single
// deployment environment.
type EnvProfile struct {
	Name           string
	ZitadelIssuer  string
	NativeClientID string
	APIAudience    string
	FleetBaseURL   string
	OIDCScopes     []string
}

// Configured reports whether this profile has all required fields populated.
// Returns false when the build pipeline has not injected ldflag values.
func (p EnvProfile) Configured() bool {
	return p.ZitadelIssuer != "" && p.NativeClientID != "" && p.FleetBaseURL != ""
}

// BadgeColor returns a UI badge color for non-production environments.
// Returns "" for prod (no badge), "yellow" for dev, "blue" for stage,
// "red" for local.
func (p EnvProfile) BadgeColor() string {
	switch p.Name {
	case EnvDev:
		return "yellow"
	case EnvStage:
		return "blue"
	case EnvLocal:
		return "red"
	default:
		return ""
	}
}

// Environment name constants.
const (
	EnvDev   = "dev"
	EnvStage = "stage"
	EnvProd  = "prod"
	EnvLocal = "local"
)

// DefaultOIDCScopes is the standard OIDC scope set requested during sign-in.
// offline_access is required for refresh tokens; the zitadel roles scope is
// required for role assertion.
var DefaultOIDCScopes = []string{
	"openid",
	"profile",
	"email",
	"urn:zitadel:iam:org:project:roles",
	"offline_access",
}

// ldflag-populated variables. These are empty strings in development builds
// and are injected at release time via go build -ldflags "-X ...".
// nolint:unused — populated via ldflags at build time.
var (
	devIssuer, devNativeClientID, devAPIAudience, devFleetBaseURL         string
	stageIssuer, stageNativeClientID, stageAPIAudience, stageFleetBaseURL string
	prodIssuer, prodNativeClientID, prodAPIAudience, prodFleetBaseURL     string
)

// ResolveProfile reads KENAZ_HARNESS_ENV and returns the corresponding
// EnvProfile. The selection logic is:
//
//   - "prod" or unset/empty → prod profile (ldflag-populated fields)
//   - "dev"                  → dev profile (ldflag-populated fields)
//   - "stage"                → stage profile (ldflag-populated fields)
//   - "local"                → reads HARNESS_FLEET_{ISSUER,CLIENT_ID,AUDIENCE,BASE_URL}
//     from the environment; unset values default to localhost defaults
//   - anything else          → prod profile with a warning to stderr
//
// When a profile's key fields are empty (build pipeline has not injected
// ldflag values), Configured() returns false and any Client method that
// requires network access returns ErrProfileNotConfigured.
func ResolveProfile() EnvProfile {
	env := strings.TrimSpace(strings.ToLower(os.Getenv("KENAZ_HARNESS_ENV")))
	switch env {
	case EnvDev:
		return EnvProfile{
			Name:           EnvDev,
			ZitadelIssuer:  devIssuer,
			NativeClientID: devNativeClientID,
			APIAudience:    devAPIAudience,
			FleetBaseURL:   devFleetBaseURL,
			OIDCScopes:     DefaultOIDCScopes,
		}
	case EnvStage:
		return EnvProfile{
			Name:           EnvStage,
			ZitadelIssuer:  stageIssuer,
			NativeClientID: stageNativeClientID,
			APIAudience:    stageAPIAudience,
			FleetBaseURL:   stageFleetBaseURL,
			OIDCScopes:     DefaultOIDCScopes,
		}
	case EnvLocal:
		issuer := os.Getenv("HARNESS_FLEET_ISSUER")
		clientID := os.Getenv("HARNESS_FLEET_CLIENT_ID")
		audience := os.Getenv("HARNESS_FLEET_AUDIENCE")
		baseURL := os.Getenv("HARNESS_FLEET_BASE_URL")
		if issuer == "" {
			issuer = "http://localhost:8080" // LLE Zitadel default
		}
		if baseURL == "" {
			baseURL = "http://localhost:8090"
		}
		return EnvProfile{
			Name:           EnvLocal,
			ZitadelIssuer:  issuer,
			NativeClientID: clientID,
			APIAudience:    audience,
			FleetBaseURL:   baseURL,
			OIDCScopes:     DefaultOIDCScopes,
		}
	case EnvProd, "":
		return EnvProfile{
			Name:           EnvProd,
			ZitadelIssuer:  prodIssuer,
			NativeClientID: prodNativeClientID,
			APIAudience:    prodAPIAudience,
			FleetBaseURL:   prodFleetBaseURL,
			OIDCScopes:     DefaultOIDCScopes,
		}
	default:
		fmt.Fprintf(os.Stderr, "fleet: unrecognized KENAZ_HARNESS_ENV %q; defaulting to prod\n", env)
		return EnvProfile{
			Name:           EnvProd,
			ZitadelIssuer:  prodIssuer,
			NativeClientID: prodNativeClientID,
			APIAudience:    prodAPIAudience,
			FleetBaseURL:   prodFleetBaseURL,
			OIDCScopes:     DefaultOIDCScopes,
		}
	}
}
