// Package paths is the single source of truth for the harness's on-disk
// layout and environment selection. Centralising it here keeps the data
// directory, log directory, and keychain namespace consistent across main,
// core/logging, and core/fleet — and makes the dev/stage/prod isolation
// obvious and uniform.
//
// Layout (all Kenaz products share the ~/.kenaz root so they don't collide
// with each other or with unrelated "harness" tools):
//
//	~/.kenaz/harness/<env>/             data dir (SQLite, contexts, media, …)
//	~/.kenaz/harness/<env>/logs/        per-env log files
//
// <env> is selected by KENAZ_HARNESS_ENV (dev | stage | prod | local),
// defaulting to prod, mirroring core/fleet.ResolveProfile. The same <env>
// name namespaces the OS-keychain items (see core/fleet/token_store.go and
// core/secrets) so a dev build never reads or overwrites a prod build's
// tokens/secrets.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// Environment names.
const (
	EnvDev   = "dev"
	EnvStage = "stage"
	EnvProd  = "prod"
	EnvLocal = "local"
)

// EnvName resolves the deployment environment from KENAZ_HARNESS_ENV.
// Empty or unrecognised values resolve to prod (the safe default), matching
// core/fleet.ResolveProfile.
func EnvName() string {
	switch strings.TrimSpace(strings.ToLower(os.Getenv("KENAZ_HARNESS_ENV"))) {
	case EnvDev:
		return EnvDev
	case EnvStage:
		return EnvStage
	case EnvLocal:
		return EnvLocal
	default:
		return EnvProd
	}
}

// Root returns ~/.kenaz — the shared root for every Kenaz product.
func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kenaz"), nil
}

// ProductDir returns ~/.kenaz/harness — this product's slice of the root.
func ProductDir() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "harness"), nil
}

// DataDir returns the per-environment data directory, ~/.kenaz/harness/<env>.
func DataDir() (string, error) {
	pd, err := ProductDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(pd, EnvName()), nil
}

// LogDir returns the per-environment log directory,
// ~/.kenaz/harness/<env>/logs.
func LogDir() (string, error) {
	dd, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dd, "logs"), nil
}

// LegacyDataDir returns the original, pre-rename data directory (~/.harness).
// It existed before the ~/.kenaz/harness/<env> layout and is adopted once into
// the prod profile by MigrateLegacy.
func LegacyDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".harness"), nil
}

// KeychainNamespace returns the environment token used to namespace OS-keychain
// items (e.g. "fleet:dev:access_token"), keeping each env's tokens/secrets
// isolated. Identical to EnvName; named separately so keychain call sites read
// intent clearly.
func KeychainNamespace() string {
	return EnvName()
}

// FleetKeychainService is the OS-keychain service under which the SHARED fleet
// auth tokens live. It is deliberately product-neutral ("kenaz", not
// "...-harness") because fleet login is shared core: signing into the harness
// should also sign you into kenaz-workbench and any other Kenaz product. The
// per-env account namespace (fleet:<env>:*) still isolates dev/stage/prod.
//
// NOTE: a product-neutral service name is necessary but not sufficient for
// cross-product sharing of ACL-bound keychain items between separately-signed
// apps — that also needs a shared keychain access-group entitlement at release
// signing time (tracked separately).
func FleetKeychainService() string {
	return "kenaz"
}

// FleetSharedDir returns the SHARED, cross-product fleet directory,
// ~/.kenaz/fleet/<env> — the intended home for fleet identity/session state
// that every Kenaz product reads, as opposed to product-private DataDir state.
// Provided so call sites can adopt it; relocating the existing per-product
// fleet state into it is tracked separately.
func FleetSharedDir() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "fleet", EnvName()), nil
}
