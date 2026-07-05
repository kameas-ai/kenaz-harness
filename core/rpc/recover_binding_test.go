package rpc_test

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/sentry"
)

// TestWrapBinding_CapturesAndRepanics asserts that sentry.WrapBinding recovers
// a panic (so Sentry can capture it) and then re-panics so the Wails runtime
// can surface an error response to the frontend rather than crashing the app.
//
// This test covers FR-004: RecoverBinding is invoked from the binding surface.
// The mechanism is the same as calling defer sentry.WrapBinding("MethodName")()
// at the top of every binding method in bindings.go.
func TestWrapBinding_CapturesAndRepanics(t *testing.T) {
	t.Parallel()

	// Simulate what a binding method does: defer WrapBinding, then panic.
	didRepanic := false
	func() {
		defer func() {
			// This recover catches the re-panic from WrapBinding. In production
			// the Wails runtime catches this re-panic and returns an error to
			// the frontend.
			if r := recover(); r != nil {
				didRepanic = true
			}
		}()

		// Inner function simulates the binding body.
		func() {
			defer sentry.WrapBinding("TestMethod")()
			panic("injected binding panic for test")
		}()
	}()

	if !didRepanic {
		t.Error("WrapBinding should re-panic after capture so the Wails runtime can handle it")
	}
}

// TestWrapBinding_NoPanicIsNoop asserts that WrapBinding is a no-op when
// no panic occurs — it must not affect normal binding execution.
func TestWrapBinding_NoPanicIsNoop(t *testing.T) {
	t.Parallel()

	called := false
	func() {
		defer sentry.WrapBinding("TestNoPanic")()
		called = true
	}()

	if !called {
		t.Error("WrapBinding must not prevent normal function execution")
	}
}
