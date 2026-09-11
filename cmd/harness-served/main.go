//go:build serve

// Command kenaz-harness-served is the CGO-free, Wails-free harness binary
// for in-VM headless deployments. It runs the HTTP/WebSocket serve mode
// defined in core/serve and serves the embedded frontend/dist-served bundle.
//
// # Build
//
// The frontend bundle must be copied into place before building:
//
//	cd frontend && npm run build:served
//	cp -r frontend/dist-served cmd/harness-served/frontend/dist-served
//	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
//	    go build -tags serve -o bin/kenaz-harness-served ./cmd/harness-served
//
// The copy is required because Go's //go:embed does not allow ".." in
// embed patterns, so the frontend assets must live under this package's
// directory tree.
//
// # Usage
//
//	kenaz-harness-served --serve --listen 0.0.0.0:7880
//
// The --serve flag is accepted-and-ignored for CLI compatibility with the
// root binary's --serve flag. The --listen flag (or HARNESS_SERVE_LISTEN env
// var) controls the bind address. Token auth is enabled when
// HARNESS_SERVE_TOKEN is set.
//
// This binary has zero dependency on github.com/wailsapp/wails — it does NOT
// import or link against webkit2gtk, so it runs on headless aarch64-linux
// images that lack webkit2gtk (the workbench VM).
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kameas-ai/kenaz-harness/cmd/harness-served/dispatch"
	"github.com/kameas-ai/kenaz-harness/cmd/mcpsubcmd"
	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/mcp/connectors"
	"github.com/kameas-ai/kenaz-harness/core/paths"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	"github.com/kameas-ai/kenaz-harness/core/serve"
	"github.com/kameas-ai/kenaz-harness/core/serve/authbroker"
)

// servedAssets holds the dist-served/ bundle (built by `npm run build:served`
// in frontend/).  It is embedded at compile time from a locally-staged copy.
//
// Build dependency: copy frontend/dist-served into this package's directory
// tree before building (see package doc above).
//
//go:embed all:frontend/dist-served
var servedAssets embed.FS

// Version is injected by the release pipeline via:
//
//	go build -tags serve -ldflags "-X main.Version=v0.1.2"
//
// "dev" is the local, untagged default.
var Version = "dev"

func main() {
	// Early dispatch: `harness-served mcp <server>` routes to the stdio MCP
	// server BEFORE flag.Parse, core init, or SQLite — mirroring main.go's
	// (:68-69) `if len(os.Args) >= 2 && os.Args[1] == "mcp"` exactly (the
	// decision itself lives in the dispatch package's MCPArgs so it can be
	// unit-tested without the `serve` build tag — see that package's
	// header for why it is a separate library package, not a file dropped
	// directly into this one).
	// entry-points-and-crash-reporting-01PMZD13 UNIT-8. Before this, this
	// binary had NO os.Args dispatch at all: `mcp sites` fell through
	// flag.Parse (which discards non-flag args into flag.Args()) straight
	// into opening paths.DataDir() and binding the served HTTP port — a
	// second SQLite handle on a live data directory before the port bind
	// failed. No user could reach this path in production (this binary is
	// built by no release workflow and did not compile before UNIT-1), but
	// the mechanism was real and would have fired the instant it shipped.
	if isMCP, mcpArgs := dispatch.MCPArgs(os.Args); isMCP {
		mcpsubcmd.Dispatch(context.Background(), mcpArgs)
		return // unreachable — Dispatch calls os.Exit on completion
	}

	// --serve is accepted and ignored for CLI compatibility with the root
	// binary's --serve flag. This binary is ALWAYS in serve mode.
	_ = flag.Bool("serve", false, "accepted for arg compatibility; always true in this binary")
	listenAddr := flag.String("listen", "",
		"address to listen on (default 0.0.0.0:7880 or HARNESS_SERVE_LISTEN env)")
	flag.Parse()

	// UNIT-8: an unrecognised non-flag argument is now a hard error instead
	// of a silent boot. flag.Parse leaves everything after the last known
	// flag in flag.Args() with no complaint — that discard-and-continue was
	// the mechanism behind the whole finding above; `mcp` is handled first,
	// but any OTHER stray positional argument would have hit the same
	// silent-boot path.
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "harness-served: unrecognised argument(s): %v\n", flag.Args())
		os.Exit(2)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	dataDir, err := paths.DataDir()
	if err != nil {
		log.Error("harness-served: data dir", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Error("harness-served: mkdir data dir", "err", err)
		os.Exit(1)
	}
	if logDir, lerr := paths.LogDir(); lerr == nil {
		logging.Configure(logDir)
	}
	log.Info("harness-served.boot", "pid", os.Getpid(), "version", Version, "data_dir", dataDir)

	// Spec 091 FR-004: served mode inverts the host-mode allow-all MCP
	// default. The whitelist (KENAZ_MCP_ALLOWLIST) is parsed and applied
	// BEFORE core boot so no recipe load can precede it; absent/empty/
	// malformed all leave block-all standing. Mirrors main.go's
	// runServeMode — both served entry points must agree (Spec 078
	// precedent).
	//
	// "Both served entry points" is a claim this comment could not back
	// until entry-points-and-crash-reporting-01PMZD13 UNIT-1 (2026-08-20):
	// this binary was built by no workflow and did not compile before
	// then, so there was exactly one OBSERVABLE served entry point
	// (main.go's --serve) and one that existed only as source. Whether
	// cmd/harness-served ships at all, or main.go's --serve remains the
	// one served entry point, is E-002 (research/escalations.md) — UNIT-8
	// makes this binary honest regardless of that answer; it does not
	// resolve it.
	mcpProv := connectors.ProvisionFromEnv(os.Getenv, log)
	// The supervisor spawns whitelisted connectors at core start (it
	// replaces the persisted-enabled recipe bootstrap via
	// rpc.WithServedConnectors) and records per-connector outcomes for
	// the Connectors_List / Connectors_Status read RPCs (spec 091 D11).
	// Ledger events (FR-014) ride the reporter ingest socket when the
	// image provides one.
	// KENAZ_AUTH_* is read here (pure env read) so the connector-token
	// client exists before rpc.New; the renewal Session is still created
	// after core start, below. Spec 091 D8: whitelisted OAuth connectors
	// authenticate with host-brokered short-lived tokens — the refresh
	// token never crosses into the VM.
	authCfg := authbroker.ReadConfig(os.Getenv)
	connTokens := authbroker.NewConnectorTokens(authCfg, log)
	connSup := connectors.NewSupervisor(connectors.SupervisorConfig{
		Provisioning: mcpProv,
		Getenv:       os.Getenv,
		Tokens:       connTokens,
		Ledger:       connectors.NewLedgerEmitterFromEnv(os.Getenv, log),
		// D13/US5: include operator-authored user recipes baked under
		// <dataDir>/mcp/recipes so whitelisted custom connector ids
		// resolve in served mode. The whitelist still gates every id.
		Catalog: connectors.CatalogWithUserRecipes(dataDir, log),
		Logger:  log,
	})

	c, err := core.New(core.Options{
		DataDir:      dataDir,
		BuildVersion: Version,
		// Spec 089: the workbench image sets KENAZ_HARNESS_WORKSPACE to the
		// granted /workspace mount so agent work lands on the host-shared
		// directory the user consented to — not a VM-private DataDir path.
		// Unset (host/dev) or unusable values resolve to
		// <DataDir>/agent-workspace inside core (workspace.Resolve).
		WorkspaceDir: os.Getenv("KENAZ_HARNESS_WORKSPACE"),
	})
	if err != nil {
		log.Error("harness-served: core init", "err", err)
		os.Exit(1)
	}
	// One line the operator can find when the workspace is not what they
	// expected. Paths + probe reason only — never directory contents.
	wsRes := c.Workspace()
	log.Info("harness-served: agent workspace resolved",
		"dir", wsRes.Dir,
		"source", wsRes.Source,
		"read_only", wsRes.ReadOnly)
	if wsRes.FallbackReason != "" {
		log.Warn("harness-served: configured workspace unusable; using private fallback",
			"reason", wsRes.FallbackReason)
	}

	// Seed the provider the Kenaz control plane granted this workbench
	// (Spec 078). Mirrors main.go's runServeMode — both served entry points
	// must agree, or which binary the image happens to bake would change
	// whether the workbench boots configured. Same E-002 caveat as the
	// comment above: "both served entry points" describes source-level
	// intent, not a claim that this binary ships today.
	api := rpc.New(c,
		rpc.WithHostProviders(serve.ProbeHostProviders(context.Background(), os.Getenv, serve.HostProviders(os.Getenv, log), log)),
		rpc.WithServedConnectors(connSup),
		rpc.WithConnectorTokens(connTokens))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Start(ctx); err != nil {
		log.Warn("harness-served: core start", "err", err)
	}
	api.SetContext(ctx)

	// F2-WP8: initialise the in-VM auth session from the KENAZ_AUTH_* env
	// vars read above (injected via EnvironmentFile from the KENAZMETA
	// disk, same mechanism as SIGIL_INGEST_TOKEN / HARNESS_VM_TOKEN).
	//
	// Privacy: broker token and access token bytes are never logged.
	authSession := authbroker.NewSession(ctx, authCfg, log)
	log.Info("harness-served: auth session initialised",
		"auth_state", authSession.State().String(),
		"broker_addr", authCfg.BrokerAddr,
	)

	// Fleet credential carryover: when the host injected broker config (we
	// are inside a workbench — no metadisk means no KENAZ_AUTH_* vars), the
	// broker session becomes the process-wide fleet token source. The guest
	// has no OS keychain for the default store to read, and renewal is owned
	// host-side; no refresh token ever crosses the boundary. Guarded on
	// BrokerAddr so a bare local run keeps keychain-backed sign-in.
	if authCfg.BrokerAddr != "" {
		fleet.SetExternalTokenSource(authSession.AccessToken)
		if authSession.State() == authbroker.StateSignedIn {
			// One-shot enroll → activates the fleet OTLP pipeline. On
			// desktop FleetSignIn does this post-login; served mode has no
			// sign-in affordance, so the signed-in seed is the trigger.
			// Best-effort: failure means telemetry stays off, nothing else.
			go func() {
				if _, err := api.Settings().FleetRefreshIdentity(ctx); err != nil {
					log.Info("harness-served: fleet enroll skipped", "reason", err.Error())
				}
			}()
		}
	}

	token := os.Getenv(serve.EnvToken)
	addr := *listenAddr
	if addr == "" {
		addr = os.Getenv(serve.EnvListenAddr)
	}
	if addr == "" {
		addr = serve.DefaultListenAddr
	}

	// Sub-root the embedded FS so serve.Server sees served.html at the root.
	servedFS, err := fs.Sub(servedAssets, "frontend/dist-served")
	if err != nil {
		log.Error("harness-served: sub-root served assets", "err", err)
		os.Exit(1)
	}

	// Wire signal handling so SIGTERM/SIGINT cancel the context and the
	// serve.Server shuts down gracefully.
	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigC
		log.Info("harness-served: shutting down")
		cancel()
	}()

	srv := serve.New(api, addr, token, servedFS, log,
		serve.WithAuthSession(authSession),
		serve.WithConnectors(connSup),
		// SD-16 (served-mode-is-a-real-mode-01PMZ707 WP08): both served
		// entry points must agree — see main.go's identical wiring.
		serve.WithStreamQueueCap(serve.StreamQueueCapFromEnv(os.Getenv)))
	if serveErr := srv.Serve(ctx); serveErr != nil && serveErr != context.Canceled {
		log.Error("harness-served: server error", "err", serveErr)
		os.Exit(1)
	}
}
