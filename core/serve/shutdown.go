package serve

import (
	"context"
	"log/slog"
	"time"
)

// apiShutdowner and coreShutdowner are the narrow method sets
// ShutdownServedCore actually needs from *rpc.API and *core.Core. Keeping
// them as small local interfaces (rather than taking the concrete types
// directly) lets ShutdownServedCore be unit-tested with fakes instead of
// a full core boot, mirroring the existing precedent in main.go's
// installServeShutdownSignal, which was extracted for the same reason.
type apiShutdowner interface {
	Shutdown()
}

type coreShutdowner interface {
	Shutdown(ctx context.Context) error
}

// coreShutdownTimeout bounds core.Core.Shutdown's own flush/drain work —
// telemetry's TracerProvider/MeterProvider/LoggerProvider.Shutdown and
// the fleet OTLP pipeline's BatchSpanProcessor drain — independently of
// whatever ctx the caller passes to ShutdownServedCore (see that
// function's doc comment for why the caller's ctx cannot be reused
// here). Five seconds: the same bound
// core/rpc/views/agentgraph/chat/partial_flush.go already uses for a
// comparable flush-on-teardown path — long enough for a normal in-
// process span/log batch to drain, short enough that a SIGTERM still
// resolves promptly. On a real `kill`, the process supervisor's own
// grace period (systemd's TimeoutStopSec, Docker's `docker stop -t`,
// etc.) is the actual outer bound; this only needs to sit comfortably
// inside that, not match it.
const coreShutdownTimeout = 5 * time.Second

// ShutdownServedCore tears down a served-mode process's API-layer
// background pollers (api.Shutdown()) BEFORE closing everything
// core.Core owns — storage, MCP child processes, events, telemetry
// (core.Core.Shutdown) — in that order.
//
// Order matters: api.Shutdown() stops the update/workflow/compaction/
// chat-cron/eval/settings-sync/ctx-graph-sync/unit-sync/fleet background
// goroutines, several of which still call into storage or MCP while
// draining. core.Core.Shutdown then closes the substrate those pollers
// depend on. Calling them in the other order risks a poller calling into
// a closed DB handle or a torn-down MCP pool mid-drain.
//
// Both served entry points — main.go's runServeMode and
// cmd/harness-served/main.go — call this so neither can silently drift
// from the other (finding #70: before this, both called neither
// Shutdown, so a served exit closed nothing core owned — no final WAL
// checkpoint, no orderly MCP child teardown, no telemetry flush).
//
// Safe to call even when Serve returned a real error — callers should
// run this before deciding whether to os.Exit, not skip it on the error
// path.
//
// ctx is accepted for call-site symmetry with the ambient serve context
// both entry points already thread through (and so a future caller with
// a genuine reason to bound this from the outside has somewhere to pass
// it), but it is DELIBERATELY NOT forwarded to c.Shutdown(). Both
// current call sites (main.go's runServeMode, cmd/harness-served's
// main) construct ctx via context.WithCancel(context.Background()) and
// cancel it from their SIGTERM/SIGINT handler to unblock srv.Serve(ctx)
// — so by the time control reaches this function, ctx is already Done.
// core.Core.Shutdown's telemetry/fleet-OTLP flush paths each `select` on
// ctx.Done() the same way the OTel SDK's own BatchSpanProcessor.Shutdown
// does, so passing the already-cancelled ctx straight through made them
// take the cancelled branch immediately and return context.Canceled
// without draining anything — the telemetry-flushes-on-SIGTERM claim
// this function's finding-#70 history makes was therefore false for
// this one sub-path (caught in review, not by any test — see
// shutdown_test.go's TestShutdownServedCore_FlushSurvivesAlreadyCancelledCtx).
// coreShutdownTimeout (above) gives those flushes a real deadline to
// race instead of a fait accompli. Do NOT "fix" this by changing the
// line below to context.WithTimeout(ctx, coreShutdownTimeout) — a child
// of an already-cancelled parent is itself already Done regardless of
// the timeout, which silently reintroduces exactly this bug.
func ShutdownServedCore(ctx context.Context, api apiShutdowner, c coreShutdowner, log *slog.Logger, label string) {
	api.Shutdown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), coreShutdownTimeout)
	defer cancel()
	if err := c.Shutdown(shutdownCtx); err != nil {
		log.Warn(label+": core shutdown", "err", err)
	}
}
