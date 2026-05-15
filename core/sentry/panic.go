package sentry

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/context/audit"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
)

// RecoverMain is a deferred function for the main goroutine. When a panic
// propagates to the main goroutine, RecoverMain:
//  1. Captures the event via the process Sentry client (after redaction).
//  2. Flushes the client with a 5-second timeout.
//  3. Re-panics so the runtime prints the stack and exits non-zero.
//
// Usage in main.go:
//
//	defer sentry.RecoverMain()
func RecoverMain() {
	r := recover()
	if r == nil {
		return
	}
	msg := redactPanicValue(r)
	logging.L().Error("panic.main", "summary", msg)
	CaptureMessage(msg, "fatal", map[string]any{"goroutine": "main"})
	Flush(5 * time.Second)
	// Re-panic so the runtime's crash reporter also fires and the process
	// exits non-zero.
	panic(r)
}

// RecoverGoroutine is a deferred function for non-main goroutines. When a
// panic is recovered, it:
//  1. Captures the event via the process Sentry client (after redaction).
//  2. Emits a KindPanicRecovered audit event.
//  3. Swallows the panic (the goroutine exits cleanly).
//
// label is a human-readable name for the goroutine (e.g. "scheduler.tick").
// emitter may be nil; in that case the audit event is skipped.
//
// Usage:
//
//	go func() {
//	    defer sentry.RecoverGoroutine(ctx, "my-worker", auditEmitter)
//	    // ...
//	}()
func RecoverGoroutine(ctx context.Context, label string, emitter audit.Emitter) {
	r := recover()
	if r == nil {
		return
	}
	msg := redactPanicValue(r)
	logging.L().Error("panic.goroutine", "label", label, "summary", msg)
	CaptureMessage(msg, "error", map[string]any{"goroutine": label})

	// Emit audit event — fire-and-forget; errors are logged, not propagated.
	if emitter != nil {
		payload := audit.PanicRecoveredPayload{
			GoroutineLabel: label,
			Summary:        msg,
		}
		if err := audit.Emit(ctx, emitter, audit.KindPanicRecovered, payload, time.Now().UTC()); err != nil {
			logging.L().Warn("panic.audit.error", "label", label, "err", err.Error())
		}
	}

	// Swallow — the goroutine exits instead of crashing the process.
}

// RecoverBinding is a deferred function for RPC binding methods. It captures
// the panic via Sentry (with the binding name in extra) and then re-panics so
// the Wails runtime can handle it (logging + error response to frontend).
//
// Usage at the top of every binding method:
//
//	defer sentry.RecoverBinding("Settings_Get")
func RecoverBinding(bindingName string) {
	r := recover()
	if r == nil {
		return
	}
	msg := redactPanicValue(r)
	CaptureMessage(msg, "error", map[string]any{
		"binding": bindingName,
		"kind":    "binding_panic",
	})
	// Re-panic so the Wails runtime can surface an error to the frontend.
	panic(r)
}

// redactPanicValue converts a recover() value to a redacted string.
func redactPanicValue(r any) string {
	var raw string
	switch v := r.(type) {
	case error:
		raw = v.Error()
	case string:
		raw = v
	default:
		raw = fmt.Sprintf("%v", v)
	}
	return RedactString(raw)
}

// Exit is a helper that flushes Sentry then calls os.Exit. Use this in
// place of os.Exit when the call site is reachable from a Sentry-aware path.
func Exit(code int) {
	Flush(5 * time.Second)
	os.Exit(code)
}
