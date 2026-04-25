package main

import (
	"context"
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/sigil-tech/kaneaz-harness/core"
	"github.com/sigil-tech/kaneaz-harness/core/rpc"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	dataDir, err := defaultDataDir()
	if err != nil {
		log.Fatalf("data dir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatalf("mkdir data dir: %v", err)
	}

	c, err := core.New(core.Options{DataDir: dataDir})
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
