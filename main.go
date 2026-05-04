package main

import (
	"context"
	"embed"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"github.com/sigil-tech/kaneaz-harness/core"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
	"github.com/sigil-tech/kaneaz-harness/core/rpc"
	"github.com/sigil-tech/kaneaz-harness/core/update/bootswap"
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
	// Eager-open the file logger so the first lines of the boot
	// sequence (data-dir setup, core.New) land in ~/.kenaz/harness.log.
	logging.L().Info("harness.boot", "pid", os.Getpid(), "version", Version)

	// Dev-only flag: --enable-manifest-hot-reload turns on the polling
	// watcher under <DataDir>/agent_graph/nodes/ so authoring a new
	// override and saving immediately reflects in the palette without
	// a chassis restart (mission agent-kernel-graph-node-catalog WP07
	// / FR-023). Default off — production graphs MUST get a stable
	// manifest set.
	enableHotReload := flag.Bool("enable-manifest-hot-reload", false,
		"dev: poll <DataDir>/agent_graph/nodes/ and reload the node manifest catalog on change")
	flag.Parse()

	dataDir, err := defaultDataDir()
	if err != nil {
		log.Fatalf("data dir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("mkdir data dir: %v", err)
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

func defaultDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".harness"), nil
}
