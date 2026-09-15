package keyring

import "testing"

// TestSeamSelectsMockUnderTest is the proof that the test-mode refusal
// fires: under `go test`, the seam must select the in-memory mock
// backend, never the real OS keychain.
//
// This is checked via ActiveBackend() — a report of which branch the
// one-time backend decision took — and deliberately NOT by calling
// Get/Set/Delete against whatever backend got selected. A live probe of a
// wrongly-selected "os" backend is free to perform a real OS Keychain
// round trip, which under a sandboxed HOME (as
// scripts/ci/check-tests-are-hermetic.sh runs with) HANGS rather than
// erroring — see the package doc and ActiveBackend's doc comment.
// Reporting the selection is enough to prove the refusal fired without
// ever risking that hang.
//
// Mutation check performed while writing this seam (see the commit that
// introduced this file): temporarily hardcoding
// `testMode = false /* mutated */` in ensureSelectedLocked (disabling the
// flag.Lookup("test.v") check but leaving everything else compiling)
// makes this test fail immediately with
// `ActiveBackend() = "os", want "mock"` — a fast, clearly-diagnosed
// wrong-backend failure, not a 120s-timeout hang, because ActiveBackend
// never touches the backend itself. The mutation was reverted after
// confirming this; it does not ship.
func TestSeamSelectsMockUnderTest(t *testing.T) {
	if got := ActiveBackend(); got != "mock" {
		t.Fatalf("ActiveBackend() = %q, want %q — go test must never select the real OS keychain", got, "mock")
	}
}

// TestGetSetDeleteRoundTripAgainstMock exercises the seam's own
// Get/Set/Delete against the backend it selected under test (the mock),
// proving the wrapper's mutex + delegation are wired correctly. This is
// safe precisely because TestSeamSelectsMockUnderTest already proved the
// selected backend is the mock, never the OS keychain.
func TestGetSetDeleteRoundTripAgainstMock(t *testing.T) {
	const service = "kenaz-harness-seam-test"
	const key = "probe-key"

	if _, err := Get(service, key); err != ErrNotFound {
		t.Fatalf("Get on unset key: got err=%v, want ErrNotFound", err)
	}

	if err := Set(service, key, "probe-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := Get(service, key)
	if err != nil {
		t.Fatalf("Get after Set: %v", err)
	}
	if got != "probe-value" {
		t.Fatalf("Get after Set = %q, want %q", got, "probe-value")
	}

	if err := Delete(service, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := Get(service, key); err != ErrNotFound {
		t.Fatalf("Get after Delete: got err=%v, want ErrNotFound", err)
	}
}
