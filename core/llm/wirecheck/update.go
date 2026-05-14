package wirecheck

import (
	"fmt"
	"os"
	"testing"
)

// UpdateEnabled reports whether the -update flag is active for the
// given adapter. It checks the TEST_<ADAPTER>_KEY environment variable.
//
// When update mode is disabled (the default), replay mode is used: the
// locked-tier fixtures are loaded from testdata/ and compared against
// the request body produced by the adapter under test.
//
// When update mode is enabled (TEST_<ADAPTER>_KEY is set), the adapter
// makes a real network call and the returned SSE stream is written back
// to the fixture file after scrubbing. The hand-authored request.json is
// never modified.
//
// Example usage in a test:
//
//	if wirecheck.UpdateEnabled(t, "anthropic") {
//	    // record from real API
//	} else {
//	    // replay from fixture
//	}
func UpdateEnabled(t *testing.T, adapter string) bool {
	t.Helper()
	key := credEnvVar(adapter)
	v := os.Getenv(key)
	if v == "" {
		return false
	}
	return true
}

// RequireUpdateKey returns the credential value for the given adapter
// or calls t.Skipf when the environment variable is absent. Use this
// in -update mode paths so missing keys produce a clear skip rather than
// a test failure.
//
//	cred := wirecheck.RequireUpdateKey(t, "anthropic")
//	// use cred for real API call
func RequireUpdateKey(t *testing.T, adapter string) string {
	t.Helper()
	key := credEnvVar(adapter)
	v := os.Getenv(key)
	if v == "" {
		t.Skipf("wirecheck: %s not set — skipping live update for %s adapter "+
			"(set %s to run against the real API)", key, adapter, key)
	}
	return v
}

// CredEnvVar returns the environment variable name that holds the
// credential for the given adapter. The convention is
// TEST_<UPPER_ADAPTER>_KEY.
func CredEnvVar(adapter string) string { return credEnvVar(adapter) }

func credEnvVar(adapter string) string {
	upper := ""
	for _, c := range adapter {
		if c >= 'a' && c <= 'z' {
			upper += string(rune(c - 32))
		} else {
			upper += string(c)
		}
	}
	return fmt.Sprintf("TEST_%s_KEY", upper)
}
