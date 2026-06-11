package main

import (
	"context"
	"embed"
	"flag"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"github.com/kameas-ai/kenaz-harness/cmd/mcpsubcmd"
	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/paths"
	"github.com/kameas-ai/kenaz-harness/core/rpc"
	coresentry "github.com/kameas-ai/kenaz-harness/core/sentry"
	"github.com/kameas-ai/kenaz-harness/core/update/bootswap"
)

//go:embed all:frontend/dist
var assets embed.FS

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
	flag.Parse()

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

	err = wails.Run(&options.App{
		Title:  "kaneaz-harness",
		Width:  1280,
		Height: 800,
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
		OnStartup: func(ctx context.Context) {
			api.SetContext(ctx)
			if err := c.Start(ctx); err != nil {
				log.Printf("core start: %v", err)
			}
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
			_ = c.Shutdown(ctx)
		},
		Bind: api.Bindings(),
	})
	if err != nil {
		log.Fatalf("wails run: %v", err)
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
	tier := coresentry.ResolveTier(s.CrashReportingTier, false /* fleet login state TBD */)
	if initErr := coresentry.Init(tier, s.SentryDSN, Version, ""); initErr != nil {
		logging.L().Warn("sentry.init.error", "err", initErr.Error())
	}
	// wire-up point 2: chain the SlogHandler when tier != Off so ERROR-level
	// slog lines are captured as redacted breadcrumbs.
	if tier != coresentry.TierOff {
		current := logging.FileHandler()
		if current != nil {
			logging.Replace(&coresentry.SlogHandler{Inner: current})
		}
	}
}

