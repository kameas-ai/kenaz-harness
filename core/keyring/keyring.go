// Package keyring is the single seam through which the harness talks to
// the OS credential store — macOS Keychain, Windows Credential Manager,
// libsecret on Linux — via github.com/zalando/go-keyring. It exists so
// exactly one package in the tree imports that vendor SDK; every other
// caller (core/fleet, core/rpc, core/secrets, core/mcp/builtin/sites, ...)
// goes through Get/Set/Delete/ErrNotFound here instead.
// scripts/ci/check-keyring-seam.sh enforces the boundary.
//
// # Why this exists
//
// Seven packages independently patched their own TestMain with
// keyring.MockInit() across several releases — most recently
// core/rpc/views/catalog and core/rpc/views/contexts (PR #348, 2026-09-15)
// — because each one had a process-global OS-keychain call reachable from
// a test, and per-package mocking is whack-a-mole: any new test that
// establishes signed-in fleet state (directly, or transitively through
// fleet.SaveTokens / a keychainWriter / a CapabilityPoller, etc.) can
// silently reintroduce it. The symptoms were real, not cosmetic:
// repeated "fleet:prod:access_token cannot be found" Keychain popups on
// the owner's own machine (tests carry no KENAZ_HARNESS_ENV and so
// default to the prod account namespace — see core/paths for the
// namespace split), and sequential hangs of
// scripts/ci/check-tests-are-hermetic.sh, because under a sandboxed HOME
// macOS's security agent HANGS (does not error) inside go-keyring's exec
// of /usr/bin/security with no interactive session to answer the prompt.
//
// # Test-mode refusal
//
// Under `go test`, the first call to Get/Set/Delete/ActiveBackend installs
// go-keyring's in-memory mock backend automatically — closing the class
// structurally instead of per-package. Detection uses
// flag.Lookup("test.v") != nil, the same idiom core/rpc/api.go already
// uses at its BootstrapLockdownStatus guard (see api.go around the
// SetContext lockdown-bootstrap comment), NOT testing.Testing() and NOT
// an import of "testing": scripts/ci/cmd/checknilopts's
// isTestDoublePackage() treats any package with a non-_test.go file that
// imports "testing" as a fixture package and drops it from the I18
// production-assignment scan, which silently excluded all of core/rpc the
// last time this mistake was made. Production behaviour is byte-identical
// — the real OS backend is selected exactly as before, with the same
// calls, the same errors, the same ErrNotFound sentinel.
//
// The decision is made on first CALL, not in a package-level var
// initializer or a func init(): Go's generated test-binary main invokes
// testing.MainStart, which is what registers the -test.v flag (via
// testing.Init()), and that main() function runs only AFTER every
// imported package's init() has already completed. flag.Lookup("test.v")
// evaluated at package-init time is therefore always nil — test binary or
// not — which was confirmed empirically while writing this package (an
// earlier func-init version of this check failed
// TestSeamSelectsMockUnderTest unconditionally). Deferring the check to
// the first real Get/Set/Delete/ActiveBackend call means it runs after
// m.Run() has started in a test binary, by which point the flag is
// registered, while remaining exactly as early as it needs to be in
// production (nothing calls this package during another package's init(),
// verified by inspection of core/fleet, core/rpc and core/secrets).
//
// This still removes the specific data race documented at
// core/rpc/testmain_test.go and core/rpc/api.go's SetContext: go-keyring's
// mock provider mutates a bare map[string]map[string]string with no
// internal synchronisation, so calling MockInit() again after some other
// goroutine (a poller, a sibling test) has already started reading/
// writing it is a real, reproducible data race — not a hypothetical one,
// and not one that a second, third, or Nth per-package TestMain can fix,
// because the fix has to run before any two goroutines can race on it.
// The mutex below serialises the first-call decision itself (see
// ensureSelectedLocked), so a concurrent first caller from two goroutines
// is race-free too.
//
// # Serialisation
//
// Every exported call here also takes a single package-level mutex before
// touching the backend, which — beyond serialising the one-time backend
// decision above — closes the same class of concurrent Get/Set/Delete
// race for EVERY future caller in one place instead of at each call site
// individually (the CapabilityPoller race class, finding #81: two
// separate instances were fixed ad hoc before this seam existed). This is
// a global lock across all service/key pairs, trading a small amount of
// concurrency for closing the class completely; go-keyring's own
// operations are simple map/syscall round trips, so the added contention
// is not expected to be observable.
package keyring

import (
	"flag"
	"sync"

	upstream "github.com/zalando/go-keyring"
)

// ErrNotFound is returned by Get and Delete when the (service, key) pair
// has no stored value. It is upstream's sentinel, re-exported so callers
// need no import of the vendor package to check it with errors.Is.
var ErrNotFound = upstream.ErrNotFound

// mu serialises every Get/Set/Delete call against the selected backend,
// AND guards the one-time backend selection below. See the package doc's
// "Serialisation" section.
var mu sync.Mutex

// selected / testMode record the one-time backend decision. Deliberately
// NOT computed at package-init time (a `var x = flag.Lookup(...)` or a
// func init() would both run too early): go's generated test-binary main
// calls testing.MainStart, which is what registers the -test.v flag via
// testing.Init(), and MainStart runs from the test binary's main() —
// which executes AFTER every imported package's init() has already run.
// flag.Lookup("test.v") at package-init time is therefore ALWAYS nil,
// test binary or not (confirmed empirically while writing this package:
// a func-init version of this check failed TestSeamSelectsMockUnderTest
// unconditionally). Deciding lazily, on the first real call instead,
// happens after m.Run() has started in a test binary — by which point the
// flag is registered — and the mu lock above serialises that first-call
// decision the same way it serialises everything else, so a concurrent
// first caller from two goroutines is still race-free.
var (
	selected bool
	testMode bool
)

// ensureSelectedLocked performs the one-time backend decision. Callers
// must hold mu.
func ensureSelectedLocked() {
	if selected {
		return
	}
	// The under-test check is flag.Lookup("test.v"), NOT
	// testing.Testing() — and this package deliberately does not import
	// "testing" — for the same reason documented at core/rpc/api.go's
	// SetContext lockdown-bootstrap guard: scripts/ci/cmd/checknilopts's
	// isTestDoublePackage() treats any package with a non-_test.go file
	// that imports "testing" as a fixture package and excludes it from
	// the I18 production-assignment scan. flag.Lookup is the pre-Go1.21
	// idiom for this and equally reliable: testing.Init() registers
	// -test.v before any test runs, for every test binary.
	testMode = flag.Lookup("test.v") != nil
	if testMode {
		upstream.MockInit()
	}
	selected = true
}

// ActiveBackend reports which backend this process selected: "mock" under
// `go test`, "os" otherwise. It exists so a test can assert the refusal
// took effect WITHOUT performing a real Get/Set/Delete against the
// backend — a live probe of the "os" backend can HANG under a sandboxed
// HOME (macOS's security agent blocks waiting for a Keychain prompt with
// nowhere to go; this is exactly the hazard core/rpc/views/sites'
// now-removed TestMain documented). Reporting which branch was taken is
// sufficient to prove the refusal fired, and cannot itself touch the real
// keychain.
func ActiveBackend() string {
	mu.Lock()
	defer mu.Unlock()
	ensureSelectedLocked()
	if testMode {
		return "mock"
	}
	return "os"
}

// Get reads service/key from the selected backend. Returns ErrNotFound
// when the pair is absent.
func Get(service, key string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	ensureSelectedLocked()
	return upstream.Get(service, key)
}

// Set writes value under service/key to the selected backend.
func Set(service, key, value string) error {
	mu.Lock()
	defer mu.Unlock()
	ensureSelectedLocked()
	return upstream.Set(service, key, value)
}

// Delete removes service/key from the selected backend. Returns
// ErrNotFound when the pair is already absent.
func Delete(service, key string) error {
	mu.Lock()
	defer mu.Unlock()
	ensureSelectedLocked()
	return upstream.Delete(service, key)
}
