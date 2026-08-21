package main

import (
	"context"
	"embed"
	"flag"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/kameas-ai/kenaz-harness/cmd/mcpsubcmd"
	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/mcp/connectors"
	coremenus "github.com/kameas-ai/kenaz-harness/core/menu"
	"github.com/kameas-ai/kenaz-harness/core/paths"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	updateview "github.com/kameas-ai/kenaz-harness/core/rpc/views/update"
	coresentry "github.com/kameas-ai/kenaz-harness/core/sentry"
	"github.com/kameas-ai/kenaz-harness/core/serve"
	"github.com/kameas-ai/kenaz-harness/core/serve/authbroker"
	"github.com/kameas-ai/kenaz-harness/core/update/bootswap"
)

//go:embed all:frontend/dist
var assets embed.FS

// servedAssets holds the dist-served/ bundle (built by `npm run build:served`
// in frontend/).  It is embedded at compile time; if the directory is absent
// the build will fail with a helpful message from the go:embed directive.
//
// Build dependency: run `cd frontend && npm run build:served` before
// `go build ./...` so that frontend/dist-served/served.html exists.
//
//go:embed all:frontend/dist-served
var servedAssets embed.FS

// Version is injected by the release pipeline via:
//
//	wails build -ldflags "-X main.Version=v0.1.2"
//
// Release-please bumps this string indirectly: the source of truth is the
// release-please manifest + git tag; the ldflag wires the resolved tag into
// the binary at link time. "dev" is the local, untagged default.
var Version = "dev"

func main() {
	// Wrap main with the Sentry panic handler so any unrecovered panic in
	// the main goroutine is captured + flushed before re-panicking.
	// wire-up point 1 for sentry (sentry-error-monitoring-01KX5R8G WP02).
	defer coresentry.RecoverMain()

	// Early dispatch: `harness mcp <server>` routes to the stdio MCP server
	// BEFORE flag.Parse, Wails, SQLite, or any other subsystem init.
	// This keeps the subprocess cold-start well under 200 ms (NFR-001).
	// No credentials are passed as argv — they are loaded from the keychain
	// inside the subprocess (FR-301 / §5 OSS security boundary).
	if len(os.Args) >= 2 && os.Args[1] == "mcp" {
		mcpsubcmd.Dispatch(context.Background(), os.Args[2:])
		return // unreachable — Dispatch calls os.Exit on completion
	}

	// Dev-only flag: --enable-manifest-hot-reload turns on the polling
	// watcher under <DataDir>/agent_graph/nodes/ so authoring a new
	// override and saving immediately reflects in the palette without
	// a chassis restart (mission agent-kernel-graph-node-catalog WP07
	// / FR-023). Default off — production graphs MUST get a stable
	// manifest set.
	enableHotReload := flag.Bool("enable-manifest-hot-reload", false,
		"dev: poll <DataDir>/agent_graph/nodes/ and reload the node manifest catalog on change")

	// --serve: skip Wails and run as an HTTP/WS server instead.
	// The address is controlled by --listen (default 0.0.0.0:7880) or the
	// HARNESS_SERVE_LISTEN env var.  Token auth is required when
	// HARNESS_SERVE_TOKEN is set.
	serveMode := flag.Bool("serve", false,
		"run in served mode: start an HTTP/WS server instead of launching the Wails desktop app")
	listenAddr := flag.String("listen", "",
		"address to listen on in served mode (default 0.0.0.0:7880 or HARNESS_SERVE_LISTEN env)")

	flag.Parse()

	// Served mode: skip Wails and start the HTTP/WS server.
	if *serveMode {
		runServeMode(*listenAddr)
		return
	}

	// Resolve the per-env data dir (~/.kenaz/harness/<env>) and adopt any
	// legacy ~/.harness data BEFORE the logger opens, so the per-env log file
	// lands inside the (possibly just-migrated) data directory.
	dataDir, err := paths.DataDir()
	if err != nil {
		log.Fatalf("data dir: %v", err)
	}
	var migrateNote string
	if legacy, lerr := paths.LegacyDataDir(); lerr == nil && paths.EnvName() == paths.EnvProd {
		if res, merr := paths.MigrateLegacy(legacy, dataDir); merr != nil {
			migrateNote = "legacy data-dir migration error: " + merr.Error()
		} else if res.Migrated {
			migrateNote = "adopted legacy data dir " + res.From + " → " + res.To
			if res.Skipped != "" {
				migrateNote += " (" + res.Skipped + ")"
			}
		} else if res.Skipped != "" && res.Skipped != "no legacy data dir" {
			migrateNote = "legacy data-dir migration skipped: " + res.Skipped
		}
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("mkdir data dir: %v", err)
	}

	// Point the file logger at the per-env logs dir, THEN emit the first line
	// so the boot sequence lands in ~/.kenaz/harness/<env>/logs/harness.log.
	if logDir, lerr := paths.LogDir(); lerr == nil {
		logging.Configure(logDir)
	}
	logging.L().Info("harness.boot",
		"pid", os.Getpid(), "version", Version,
		"env", paths.EnvName(), "data_dir", dataDir)
	if migrateNote != "" {
		logging.L().Info("harness.datadir.migrate", "note", migrateNote)
	}

	// FR-002: assert fleet signing-key presence at boot. A shipped binary
	// without a key logs exactly one WARN so the absence is never silent.
	// Dev builds (Version == "dev") skip this so local development is unaffected.
	if !fleet.ConfigDistributionEnabled() {
		if Version != "dev" {
			logging.L().Warn("fleet.config_distribution.disabled",
				"reason", "signing key not injected at build time",
				"action", "fleet config bundles will not be applied; contact ops to provision FLEET_SIGNING_PUBKEY")
		} else {
			logging.L().Debug("fleet.config_distribution.disabled",
				"reason", "dev build — signing key absent by design")
		}
	} else {
		logging.L().Info("fleet.config_distribution.enabled")
	}

	// Boot-time auto-update swap (mission auto-update WP02). On
	// Windows this completes a deferred swap from a previous session;
	// on macOS/Linux it is a no-op (the marker is never written).
	// Errors are NON-FATAL: we log and continue so a corrupt staged
	// update never prevents the user from launching their existing
	// (un-updated) build. The Relauncher is nil → real os.Exit path.
	if res, err := bootswap.MaybeSwapAndRelaunch(bootswap.Config{
		DataDir: dataDir,
		Args:    os.Args[1:],
	}); err != nil {
		logging.L().Warn("update.bootswap.error", "err", err.Error())
	} else if res.Swapped {
		logging.L().Info("update.bootswap.completed", "version", res.TargetVersion)
	}

	c, err := core.New(core.Options{
		DataDir:                 dataDir,
		EnableManifestHotReload: *enableHotReload,
		BuildVersion:            Version,
	})
	if err != nil {
		log.Fatalf("core init: %v", err)
	}

	api := rpc.New(c)

	// controls-and-readouts-that-tell-the-truth-01PMZ808 WP06 (FR-006,
	// spec D-5): resolve the persisted window size before wails.Run so
	// options.App.Width/Height carry the user's last size instead of a
	// literal 1280x800. options.App.Width/Height are consumed before
	// OnStartup fires, so a restore-in-OnStartup approach would produce a
	// visible resize flash on every launch; reading settings here (the
	// store is already constructed inside rpc.New, independent of
	// core.Start/OnStartup) avoids it entirely.
	initWidth, initHeight := resolveInitialWindowSize(context.Background(), api)

	// os-menu-bar-01NDFSEX16 WP03: build the initial native application menu.
	// Handlers wire the broker (topic publish) + updater (CheckNow /
	// StartDownload / Apply adapter — widened in self-update-repair
	// -01PMUP01 WP05 so the Help item dispatches on all five
	// UpdateMenuState values instead of always re-checking).
	menuHandlers := coremenus.NewHandlers(
		api.Broker(),
		coremenus.UpdateControllerFuncs{
			CheckNowFunc:      func(ctx context.Context) { api.UpdateStartCheck(ctx) },
			StartDownloadFunc: func(ctx context.Context) { api.UpdateStartDownload(ctx) },
			ApplyFunc:         func(ctx context.Context) { api.UpdateApply(ctx) },
		},
		nil, // ContextProvider is set in OnStartup via SetMenuCtxProv (below)
	)
	// Derive initial state before first paint: theme from settings store,
	// update/recent-sessions start empty (populated after first poll/events).
	initialMenuState := deriveMenuState(api)
	appMenu := coremenus.Build(initialMenuState, menuHandlers)

	// menuMu protects concurrent access to menuState + the rebuild debounce.
	var menuMu sync.Mutex
	var menuState = initialMenuState
	var menuDebounce *time.Timer
	var menuCtx context.Context

	// rebuildMenu reconstructs and re-applies the native menu.
	// Must be called with menuMu held.
	rebuildMenuLocked := func() {
		if menuCtx == nil {
			return // OnStartup has not fired yet
		}
		appMenu = coremenus.Build(menuState, menuHandlers)
		wailsruntime.MenuSetApplicationMenu(menuCtx, appMenu)
	}

	// scheduleRebuild debounces menu rebuilds to at most one per 100 ms.
	scheduleRebuild := func() {
		menuMu.Lock()
		defer menuMu.Unlock()
		if menuDebounce != nil {
			menuDebounce.Stop()
		}
		menuDebounce = time.AfterFunc(100*time.Millisecond, func() {
			menuMu.Lock()
			defer menuMu.Unlock()
			rebuildMenuLocked()
		})
	}

	err = wails.Run(&options.App{
		Title:  "kenaz-harness",
		Width:  initWidth,
		Height: initHeight,
		AssetServer: &assetserver.Options{
			Assets:     assets,
			Middleware: rpc.NewCSPMiddleware(),
		},
		BackgroundColour: &options.RGBA{R: 10, G: 10, B: 11, A: 1},
		// macOS: traffic-light buttons (incl. green fullscreen pill) are
		// hidden by Wails defaults in some dev configurations. Explicit
		// TitleBarHiddenInset keeps the traffic lights visible inset over
		// the in-app titlebar (modern Mac app pattern) and lets the user
		// hit the green button to enter fullscreen.
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			Appearance:           mac.NSAppearanceNameDarkAqua,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
		// Wire the initial menu before the window opens.
		Menu: appMenu,
		OnStartup: func(ctx context.Context) {
			api.SetContext(ctx)

			// Store the Wails ctx for runtime.MenuSetApplicationMenu calls.
			menuMu.Lock()
			menuCtx = ctx
			menuMu.Unlock()

			// Wire the ContextProvider on the handlers so BrowserOpenURL works.
			menuHandlers.SetCtxProv(coremenus.ContextProviderFunc(func() context.Context { return ctx }))

			if err := c.Start(ctx); err != nil {
				log.Printf("core start: %v", err)
			}

			// Subscribe to broker events that require a menu rebuild.
			// runtime.EventsOn wires a Go-side listener on the same topic
			// the broker's WailsEmitter published to.
			//
			// theme:changed — flip the radio checkmark in View → Theme.
			wailsruntime.EventsOn(ctx, "theme:changed", func(data ...interface{}) {
				menuMu.Lock()
				if len(data) > 0 {
					if m, ok := data[0].(map[string]interface{}); ok {
						if mode, ok := m["mode"].(string); ok {
							menuState.ThemeMode = coremenus.ThemeMode(mode)
						}
					}
				}
				menuMu.Unlock()
				scheduleRebuild()
			})
			// update:available + update:download-{progress,complete,failed}
			// — relabel Help → Check for Updates. self-update-repair-01PMUP01
			// WP03: the three download topics are new; update:available was
			// pre-existing. All four route through coremenus.UpdateTopicState
			// (the pure, independently-tested topic→state mapping — "the
			// subscriber" per AC-8) so this closure is thin IPC plumbing, not
			// where the state logic lives.
			//
			// ACCELERATOR ONLY (spec §4.1 DC-1): the frontend's installLatest
			// correctness never depends on these — it polls Update_Status to
			// a terminal state on its own (WP02). This is the Go-side
			// consumer that makes UpdateDownloading / UpdateStaged /
			// UpdateFailed reachable in production (spec §1.4) — before this,
			// only UpdateAvailable ever had a writer, so three of the five
			// UpdateMenuLabel values were dead code with a live-looking label
			// function. WP05 makes the resulting labels dispatch correctly.
			onUpdateTopic := func(topic string) func(...interface{}) {
				return func(_ ...interface{}) {
					if state, ok := coremenus.UpdateTopicState(topic); ok {
						menuMu.Lock()
						menuState.UpdateState = state
						menuMu.Unlock()
						scheduleRebuild()
					}
				}
			}
			wailsruntime.EventsOn(ctx, "update:available", onUpdateTopic("update:available"))
			wailsruntime.EventsOn(ctx, updateview.TopicDownloadProgress, onUpdateTopic(updateview.TopicDownloadProgress))
			wailsruntime.EventsOn(ctx, updateview.TopicDownloadComplete, onUpdateTopic(updateview.TopicDownloadComplete))
			wailsruntime.EventsOn(ctx, updateview.TopicDownloadFailed, onUpdateTopic(updateview.TopicDownloadFailed))
			// session.list_changed — repopulate File → Open Recent (max 10).
			// Broker topic rpc.TopicSessionListChanged = "session.list_changed".
			wailsruntime.EventsOn(ctx, rpc.TopicSessionListChanged, func(_ ...interface{}) {
				if sessAPI := api.Sessions(); sessAPI != nil {
					if sessions, err := sessAPI.List(context.Background()); err == nil {
						refs := make([]coremenus.SessionRef, 0, 10)
						for i, s := range sessions {
							if i >= 10 {
								break
							}
							refs = append(refs, coremenus.SessionRef{ID: s.ID, Title: s.Name})
						}
						menuMu.Lock()
						menuState.RecentSessions = refs
						menuMu.Unlock()
						scheduleRebuild()
					}
				}
			})

			// Initialise Sentry after core.Start so the settings store is
			// ready. wire-up point 1 for sentry (sentry-error-monitoring-01KX5R8G WP02).
			// api.Bindings() returns []any{<*Bindings>}; assert back to the concrete
			// type so initSentryFromSettings can call Settings_Get on it.
			if bs := api.Bindings(); len(bs) > 0 {
				if b, ok := bs[0].(*rpc.Bindings); ok {
					initSentryFromSettings(b)
				}
			}
		},
		OnShutdown: func(ctx context.Context) {
			// controls-and-readouts-that-tell-the-truth-01PMZ808 WP06
			// (FR-006): persist the live window size so the next launch
			// restores it, instead of leaving Settings -> About stuck
			// reporting the boot-time literal forever. Runs before
			// c.Shutdown so the settings store is still open.
			persistWindowSize(ctx, api, wailsruntime.WindowGetSize)
			_ = c.Shutdown(ctx)
		},
		Bind: api.Bindings(),
	})
	if err != nil {
		log.Fatalf("wails run: %v", err)
	}
}

// runServeMode boots the harness in HTTP/WS served mode and blocks until
// SIGTERM / SIGINT.  It does NOT start the Wails desktop — this path is
// designed for in-VM deployments where a browser connects over HTTP.
//
// Sequence:
//  1. Resolve and prepare the data dir (same as desktop path).
//  2. Boot core + rpc.API (same as desktop path).
//  3. Call core.Start on a background context.
//  4. Read KENAZ_AUTH_* env vars, initialise the auth broker session (F2-WP8).
//  5. Hand the API + auth session to serve.New and block on Serve.
func runServeMode(listenAddr string) {
	serveLog := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	dataDir, err := paths.DataDir()
	if err != nil {
		serveLog.Error("harness.serve: data dir", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		serveLog.Error("harness.serve: mkdir data dir", "err", err)
		os.Exit(1)
	}
	if logDir, lerr := paths.LogDir(); lerr == nil {
		logging.Configure(logDir)
	}
	serveLog.Info("harness.serve.boot", "pid", os.Getpid(), "version", Version, "data_dir", dataDir)

	// Spec 091 FR-004: served mode inverts the host-mode allow-all MCP
	// default. The whitelist (KENAZ_MCP_ALLOWLIST) is parsed and applied
	// BEFORE core boot so no recipe load can precede it; absent/empty/
	// malformed all leave block-all standing. Mirrors cmd/harness-served —
	// both served entry points must agree (Spec 078 precedent). The
	// DESKTOP path never runs this: host mode keeps nil = unrestricted.
	mcpProv := connectors.ProvisionFromEnv(os.Getenv, serveLog)
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
	connTokens := authbroker.NewConnectorTokens(authCfg, serveLog)
	connSup := connectors.NewSupervisor(connectors.SupervisorConfig{
		Provisioning: mcpProv,
		Getenv:       os.Getenv,
		Tokens:       connTokens,
		Ledger:       connectors.NewLedgerEmitterFromEnv(os.Getenv, serveLog),
		// D13/US5: include operator-authored user recipes baked under
		// <dataDir>/mcp/recipes so whitelisted custom connector ids
		// resolve in served mode. The whitelist still gates every id.
		Catalog: connectors.CatalogWithUserRecipes(dataDir, serveLog),
		Logger:  serveLog,
	})

	c, err := core.New(core.Options{
		DataDir:      dataDir,
		BuildVersion: Version,
		// Spec 089: mirror cmd/harness-served — both served entry points
		// must agree (Spec 078 precedent) on the workspace override the
		// workbench image supplies.
		WorkspaceDir: os.Getenv("KENAZ_HARNESS_WORKSPACE"),
	})
	if err != nil {
		serveLog.Error("harness.serve: core init", "err", err)
		os.Exit(1)
	}
	wsRes := c.Workspace()
	serveLog.Info("harness.serve: agent workspace resolved",
		"dir", wsRes.Dir,
		"source", wsRes.Source,
		"read_only", wsRes.ReadOnly)
	if wsRes.FallbackReason != "" {
		serveLog.Warn("harness.serve: configured workspace unusable; using private fallback",
			"reason", wsRes.FallbackReason)
	}

	// Seed the provider the Kenaz control plane granted this workbench
	// (Spec 078). Without this the served harness boots with an empty
	// provider list and no in-VM way to add one.
	api := rpc.New(c,
		rpc.WithHostProviders(serve.HostProviders(os.Getenv, serveLog)),
		rpc.WithServedConnectors(connSup),
		rpc.WithConnectorTokens(connTokens))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := c.Start(ctx); err != nil {
		serveLog.Warn("harness.serve: core start", "err", err)
	}
	api.SetContext(ctx)

	// F2-WP8: initialise the in-VM auth session from the KENAZ_AUTH_* env
	// vars read above (injected via EnvironmentFile from the KENAZMETA
	// disk, same mechanism as SIGIL_INGEST_TOKEN / HARNESS_VM_TOKEN).
	//
	// Privacy: broker token and access token bytes are never logged.
	authSession := authbroker.NewSession(ctx, authCfg, serveLog)
	serveLog.Info("harness.serve: auth session initialised",
		"auth_state", authSession.State().String(),
		"broker_addr", authCfg.BrokerAddr,
	)

	// Fleet credential carryover: when the host injected broker config (we
	// are inside a workbench — no metadisk means no KENAZ_AUTH_* vars), the
	// broker session becomes the process-wide fleet token source. The guest
	// has no OS keychain for the default store to read, and renewal is owned
	// host-side; no refresh token ever crosses the boundary. Guarded on
	// BrokerAddr so a desktop `--serve` run keeps keychain-backed sign-in.
	if authCfg.BrokerAddr != "" {
		fleet.SetExternalTokenSource(authSession.AccessToken)
		if authSession.State() == authbroker.StateSignedIn {
			// One-shot enroll → activates the fleet OTLP pipeline. On
			// desktop FleetSignIn does this post-login; served mode has no
			// sign-in affordance, so the signed-in seed is the trigger.
			// Best-effort: failure means telemetry stays off, nothing else.
			go func() {
				if _, err := api.Settings().FleetRefreshIdentity(ctx); err != nil {
					serveLog.Info("harness.serve: fleet enroll skipped", "reason", err.Error())
				}
			}()
		}
	}

	token := os.Getenv(serve.EnvToken)
	addr := listenAddr
	if addr == "" {
		addr = os.Getenv(serve.EnvListenAddr)
	}
	if addr == "" {
		addr = serve.DefaultListenAddr
	}

	// Sub-root the embedded FS so serve.Server sees served.html at the root.
	servedFS, err := fs.Sub(servedAssets, "frontend/dist-served")
	if err != nil {
		serveLog.Error("harness.serve: sub-root served assets", "err", err)
		os.Exit(1)
	}

	// SD-11 (served-mode-is-a-real-mode-01PMZ707 WP08): this docstring has
	// claimed "blocks until SIGTERM / SIGINT" since it was written, but
	// nothing here ever called signal.Notify — the sibling entry point
	// (cmd/harness-served/main.go) had this wiring and this one did not,
	// so `defer cancel()` and srv.Shutdown never ran on a real SIGTERM: the
	// process just died mid-request, dropping in-flight streams. Mirrors
	// cmd/harness-served/main.go's wiring exactly — "both served entry
	// points must agree" (this file's own comment above) now holds for
	// shutdown, not just for boot-time config reads.
	stopSignalHandler := installServeShutdownSignal(serveLog, "harness.serve", cancel)
	defer stopSignalHandler()

	srv := serve.New(api, addr, token, servedFS, serveLog,
		serve.WithAuthSession(authSession),
		serve.WithConnectors(connSup),
		// SD-16 (WP08): KENAZ_SERVE_STREAM_QUEUE_CAP, the workbench-image
		// config surface WithStreamQueueCap's "constrained workbench" half
		// never had. 0 (absent/invalid) keeps serve.defaultStreamQueueCap.
		serve.WithStreamQueueCap(serve.StreamQueueCapFromEnv(os.Getenv)))
	if serveErr := srv.Serve(ctx); serveErr != nil && serveErr != context.Canceled {
		serveLog.Error("harness.serve: server error", "err", serveErr)
		os.Exit(1)
	}
}

// installServeShutdownSignal wires SIGTERM/SIGINT to cancel, the same shape
// cmd/harness-served/main.go uses inline. Extracted to its own function
// (SD-11, WP08) so the wiring is unit-testable in isolation: main()'s own
// call graph is not — it depends on real env vars, a real data dir and an
// embedded FS — but "does a SIGTERM sent to this process reach cancel()"
// does not need any of that.
//
// Returns a stop func that unregisters the signal channel and releases the
// background goroutine; callers should defer it.
func installServeShutdownSignal(log *slog.Logger, label string, cancel context.CancelFunc) (stop func()) {
	sigC := make(chan os.Signal, 1)
	signal.Notify(sigC, syscall.SIGTERM, syscall.SIGINT)
	done := make(chan struct{})
	go func() {
		select {
		case <-sigC:
			log.Info(label + ": shutting down")
			cancel()
		case <-done:
		}
	}()
	return func() {
		close(done)
		signal.Stop(sigC)
	}
}

// deriveMenuState snapshots the menu-relevant application state from the
// API. Called at app startup to build the initial menu, and before each
// rebuild triggered by broker events.
//
// Best-effort: individual field reads that fail are silently skipped so a
// store read error never prevents the menu from rendering.
// (os-menu-bar-01NDFSEX16 WP03)
func deriveMenuState(api *rpc.API) coremenus.MenuState {
	var state coremenus.MenuState
	state.ThemeMode = coremenus.ThemeSystem // default

	if store := api.SettingsStore(); store != nil {
		if theme, err := store.LoadTheme(); err == nil && theme != "" {
			state.ThemeMode = coremenus.ThemeMode(theme)
		}
	}

	// Populate File → Open Recent (capped at 10, most-recent first).
	// Sessions().List returns sessions ordered by last-active descending.
	if sessAPI := api.Sessions(); sessAPI != nil {
		if sessions, err := sessAPI.List(context.Background()); err == nil {
			refs := make([]coremenus.SessionRef, 0, 10)
			for i, s := range sessions {
				if i >= 10 {
					break
				}
				refs = append(refs, coremenus.SessionRef{ID: s.ID, Title: s.Name})
			}
			state.RecentSessions = refs
		}
	}

	return state
}

// defaultWindowWidth / defaultWindowHeight mirror the literal that used to
// be hardcoded directly into options.App (and still are the fallback for a
// fresh install / unreadable settings file).
const (
	defaultWindowWidth  = 1280
	defaultWindowHeight = 800
)

// resolveInitialWindowSize reads the persisted window size from settings so
// the window opens at the size the user left it at. Falls back to the
// historical 1280x800 literal on a fresh install, a settings read error, or
// a zero-valued (never-persisted) field.
// (controls-and-readouts-that-tell-the-truth-01PMZ808 WP06, FR-006)
func resolveInitialWindowSize(ctx context.Context, api *rpc.API) (width, height int) {
	width, height = defaultWindowWidth, defaultWindowHeight
	if api == nil || api.Settings() == nil {
		return width, height
	}
	s, err := api.Settings().Get(ctx)
	if err != nil {
		return width, height
	}
	if s.WindowSize.Width > 0 {
		width = s.WindowSize.Width
	}
	if s.WindowSize.Height > 0 {
		height = s.WindowSize.Height
	}
	return width, height
}

// persistWindowSize reads the live window size via getSize (the production
// caller passes wailsruntime.WindowGetSize; tests inject a fake so this can
// be exercised without a real windowing backend) and writes it back into
// settings, so Settings -> About stops reporting the boot-time literal
// forever and the next launch restores the real size.
// (controls-and-readouts-that-tell-the-truth-01PMZ808 WP06, FR-006)
func persistWindowSize(ctx context.Context, api *rpc.API, getSize func(context.Context) (int, int)) {
	if api == nil || api.Settings() == nil || getSize == nil {
		return
	}
	w, h := getSize(ctx)
	if w <= 0 || h <= 0 {
		// Headless / not-yet-shown window: never persist a bogus size.
		return
	}
	s, err := api.Settings().Get(ctx)
	if err != nil {
		logging.L().Warn("windowsize.persist.read_error", "err", err.Error())
		return
	}
	if s.WindowSize.Width == w && s.WindowSize.Height == h {
		return
	}
	s.WindowSize.Width = w
	s.WindowSize.Height = h
	if err := api.Settings().Set(ctx, s); err != nil {
		logging.L().Warn("windowsize.persist.write_error", "err", err.Error())
	}
}

// initSentryFromSettings reads the crash-reporting settings, calls
// coresentry.Init, and (when tier != Off) installs the SlogHandler bridge.
// Errors are logged but never fatal — Sentry is optional infrastructure and
// must never block startup.
// wire-up point 1+2 for sentry (sentry-error-monitoring-01KX5R8G WP02/WP03).
func initSentryFromSettings(api *rpc.Bindings) {
	s, err := api.Settings_Get()
	if err != nil {
		logging.L().Warn("sentry.settings.read_error", "err", err.Error())
		return
	}
	// controls-and-readouts-that-tell-the-truth-01PMZ808 WP12 (FR-016):
	// this was hardcoded `false`, so ResolveTier permanently downgraded
	// TierIdentified to TierAnonymous for every user, even one genuinely
	// signed into fleet — Settings_FleetSignedIn (arity 0) is exactly
	// what ResolveTier's second parameter wants. A read error (fleet
	// disabled, no client) is treated as "not signed in" (the safe,
	// anonymous-leaning default).
	fleetSignedIn, fsErr := api.Settings_FleetSignedIn()
	if fsErr != nil {
		fleetSignedIn = false
	}
	tier := coresentry.ResolveTier(s.CrashReportingTier, fleetSignedIn)
	if initErr := coresentry.Init(tier, s.SentryDSN, Version, ""); initErr != nil {
		logging.L().Warn("sentry.init.error", "err", initErr.Error())
	}
	// controls-and-readouts-that-tell-the-truth-01PMZ808 WP12 (FR-018):
	// local_report.go's own doc claimed "SetHarnessVersion is called
	// from main.go" — grep -c SetHarnessVersion main.go was 0. The
	// UPLOADED release tag was already correct (Init above passes
	// Version as the SDK's Release field); only the LOCAL report's
	// embedded harness_version field lied, always reading "dev".
	coresentry.SetHarnessVersion(Version)
	// Both ends of FR-016: threading the real fleetSignedIn value above
	// only fixes WHICH tier gets selected; core/sentry/client.go itself
	// only branched on TierOff before SetUser existed, so
	// TierAnonymous and TierIdentified were observably identical events
	// on the wire. Tag (or explicitly clear) the scope-wide user now
	// that both ends exist.
	coresentry.SetUser(tier == coresentry.TierIdentified)
	// wire-up point 2: chain the SlogHandler when tier != Off so ERROR-level
	// slog lines are captured as redacted breadcrumbs.
	if tier != coresentry.TierOff {
		current := logging.FileHandler()
		if current != nil {
			logging.Replace(&coresentry.SlogHandler{Inner: current})
		}
	}
}
