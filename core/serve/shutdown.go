package serve

import (
	"context"
	"log/slog"
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
func ShutdownServedCore(ctx context.Context, api apiShutdowner, c coreShutdowner, log *slog.Logger, label string) {
	api.Shutdown()
	if err := c.Shutdown(ctx); err != nil {
		log.Warn(label+": core shutdown", "err", err)
	}
}
