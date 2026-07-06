// Package mcpsubcmd implements the `harness mcp <server>` dispatch path.
// It is entered BEFORE flag.Parse and before any Wails or SQLite
// initialisation so the subprocess starts in < 200 ms on a cold binary
// (NFR-001).
//
// Usage:
//
//	harness mcp sites
//
// Dispatch routes "sites" to the fleet-sites stdio MCP server.
// Unknown subcommands print usage to stderr and exit 1.
//
// No credentials are passed on the command line or via environment
// variables. The sites server loads tokens from the OS keychain at
// call time (inside the subprocess).
//
// Mission: sites-mcp-server-01NSITE05 WP05.
package mcpsubcmd

import (
	"context"
	"fmt"
	"os"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/mcp/builtin/sites"
	"github.com/kameas-ai/kenaz-harness/core/paths"
)

// Dispatch routes the `mcp <args>` subcommand. args is os.Args[2:]
// (everything after "mcp"). It blocks until the server exits or ctx
// is cancelled. On unrecognised subcommands it writes usage to stderr
// and calls os.Exit(1).
//
// The function intentionally takes no *flag.FlagSet — it must not call
// flag.Parse so that the Wails boot path (which parses its own flags)
// remains unaffected when this dispatch fires first.
func Dispatch(ctx context.Context, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: harness mcp <server>")
		fmt.Fprintln(os.Stderr, "available servers: sites")
		os.Exit(1)
	}

	switch args[0] {
	case "sites":
		runSites(ctx)
	default:
		fmt.Fprintf(os.Stderr, "harness mcp: unknown server %q\n", args[0])
		fmt.Fprintln(os.Stderr, "available servers: sites")
		os.Exit(1)
	}
}

// runSites starts the fleet-sites MCP server over stdin/stdout.
func runSites(ctx context.Context) {
	dataDir, err := paths.DataDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness mcp sites: resolve data dir: %v\n", err)
		os.Exit(1)
	}

	// Construct the fleet client. When fleet is disabled the client is a
	// nop and all fleet-dependent tools return isError:true messages.
	// No credentials are passed as arguments — the sites server loads
	// tokens from the OS keychain at call time (FR-301).
	fc, err := fleet.NewClient(fleet.ClientOpts{DataDir: dataDir})
	if err != nil {
		fmt.Fprintf(os.Stderr, "harness mcp sites: fleet client: %v\n", err)
		os.Exit(1)
	}

	if err := sites.Run(ctx, dataDir, fc); err != nil {
		// EOF is normal exit; any other error is abnormal.
		fmt.Fprintf(os.Stderr, "harness mcp sites: %v\n", err)
		os.Exit(1)
	}
}
