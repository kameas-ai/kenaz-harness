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
// Returns false when client_id has not been injected (e.g. before the build
// pipeline plumbs the sigil-infra TF output).
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
// required for role assertion (enabled on the kameas-native Zitadel app).
var DefaultOIDCScopes = []string{
	"openid",
	"profile",
	"email",
	"urn:zitadel:iam:org:project:roles",
	"offline_access",
}

// Per-environment constants. Issuers and fleet base URLs are non-secret
// public values; client IDs and audiences are also public-by-design
// (kameas-native is a PKCE-only NATIVE OIDC app — see sigil-infra
// aws/terraform/modules/zitadel-kameas-apps/outputs.tf) but are injected
// at release time via -ldflags -X so different release lines (dev/stage
// signed binaries vs prod signed binaries) can pin different orgs.
//
// Defined as package-level vars so ldflags can override.
// lleNativeClientID and lleAPIAudience are the Zitadel `kameas-native` and
// `kameas-api` client IDs in the LLE realm. They are PUBLIC values per
// sigil-infra aws/terraform/modules/zitadel-kameas-apps/outputs.tf
// ("Client IDs are public values by design: ... native apps embed
// kameas_native_client_id ..."). Local, dev, and stage all share these
// because LLE serves both dev and stage fleet deployments per
// kenaz-fleet/deploy/helm/values-lle.yaml.
//
// The LLE values are baked in as source defaults (matching the kenaz
// Makefile's KENAZ_LLE_* defaults) so every build path — release.yml,
// the kenaz-workbench Nix bake, and local `wails dev` — produces a
// Configured() profile without per-pipeline ldflags plumbing. Refresh
// from kameas-infra aws/terraform/live/dev/zitadel-apps:
//   terragrunt output -raw kameas_native_client_id
//   terragrunt output -raw kameas_api_client_id
// ldflags -X can still override for a release line pinned elsewhere.
var (
	lleNativeClientID = "373053669924582106"
	lleAPIAudience    = "373053669958071002"
)

var (
	// Local: LLE Zitadel + localhost fleet.
	localIssuer   = "https://lle-hkig0f.us1.zitadel.cloud"
	localFleetURL = "http://localhost:8090"

	// Dev: LLE Zitadel + dev fleet deployment.
	devIssuer   = "https://lle-hkig0f.us1.zitadel.cloud"
	devFleetURL = "https://dev.fleet.kameas.ai"

	// Stage: LLE Zitadel + stage fleet deployment.
	stageIssuer   = "https://lle-hkig0f.us1.zitadel.cloud"
	stageFleetURL = "https://stage.fleet.kameas.ai"

	// Prod: dedicated prod Zitadel (separate instance from LLE per
	// sigil-infra docs/architecture.md). Baked in as source defaults for
	// the same reason as the LLE values above — every build path (release
	// binaries, the kenaz-workbench Nix bake, local dev) must produce a
	// Configured() prod profile without per-pipeline ldflags plumbing.
	// These match the KENAZ_PROD_* values the kenaz launcher bakes into
	// its own prod builds (kenaz v1.34.0+); refresh from kameas-infra
	// aws/terraform/live/prod/zitadel-apps if the prod apps are recreated.
	// ldflags -X can still override for a release line pinned elsewhere.
	prodIssuer         = "https://prod-bq6woq.us1.zitadel.cloud"
	prodNativeClientID = "374862563197880707"
	prodAPIAudience    = "374862563130765514"
	prodFleetURL       = "https://fleet.kameas.ai"
)

// ResolveProfile reads KENAZ_HARNESS_ENV and returns the corresponding
// fully-configured EnvProfile.
//
//   - unset / empty / "prod" → prod (the safe default)
//   - "dev"                  → LLE Zitadel + dev fleet
//   - "stage"                → LLE Zitadel + stage fleet
//   - "local"                → LLE Zitadel + localhost:8090 fleet
//   - any other value        → logs warning + falls back to prod
//
// No per-field override env vars are read — the single KENAZ_HARNESS_ENV
// variable selects the entire profile. (Previously HARNESS_FLEET_{ISSUER,
// CLIENT_ID,AUDIENCE,BASE_URL} could override individual fields; that
// layer was removed when each profile became self-configured.)
func ResolveProfile() EnvProfile {
	env := strings.TrimSpace(strings.ToLower(os.Getenv("KENAZ_HARNESS_ENV")))
	switch env {
	case EnvLocal:
		return EnvProfile{
			Name:           EnvLocal,
			ZitadelIssuer:  localIssuer,
			NativeClientID: lleNativeClientID,
			APIAudience:    lleAPIAudience,
			FleetBaseURL:   localFleetURL,
			OIDCScopes:     DefaultOIDCScopes,
		}
	case EnvDev:
		return EnvProfile{
			Name:           EnvDev,
			ZitadelIssuer:  devIssuer,
			NativeClientID: lleNativeClientID,
			APIAudience:    lleAPIAudience,
			FleetBaseURL:   devFleetURL,
			OIDCScopes:     DefaultOIDCScopes,
		}
	case EnvStage:
		return EnvProfile{
			Name:           EnvStage,
			ZitadelIssuer:  stageIssuer,
			NativeClientID: lleNativeClientID,
			APIAudience:    lleAPIAudience,
			FleetBaseURL:   stageFleetURL,
			OIDCScopes:     DefaultOIDCScopes,
		}
	case EnvProd, "":
		return EnvProfile{
			Name:           EnvProd,
			ZitadelIssuer:  prodIssuer,
			NativeClientID: prodNativeClientID,
			APIAudience:    prodAPIAudience,
			FleetBaseURL:   prodFleetURL,
			OIDCScopes:     DefaultOIDCScopes,
		}
	default:
		fmt.Fprintf(os.Stderr, "fleet: unrecognized KENAZ_HARNESS_ENV %q; defaulting to prod\n", env)
		return EnvProfile{
			Name:           EnvProd,
			ZitadelIssuer:  prodIssuer,
			NativeClientID: prodNativeClientID,
			APIAudience:    prodAPIAudience,
			FleetBaseURL:   prodFleetURL,
			OIDCScopes:     DefaultOIDCScopes,
		}
	}
}
