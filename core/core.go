package core

import (
	"context"
	"errors"

	"github.com/sigil-tech/kaneaz-harness/core/blob"
	"github.com/sigil-tech/kaneaz-harness/core/bundle"
	"github.com/sigil-tech/kaneaz-harness/core/config"
	"github.com/sigil-tech/kaneaz-harness/core/event"
	"github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/memory"
	"github.com/sigil-tech/kaneaz-harness/core/scheduler"
	"github.com/sigil-tech/kaneaz-harness/core/session"
)

type Options struct {
	DataDir string
}

type Core struct {
	opts Options

	Events    event.Log
	Bundles   bundle.Resolver
	Sessions  session.Executor
	Scheduler scheduler.Scheduler
	Blobs     blob.CAS
	Config    config.Store
	Memory    memory.View
	LLMs      llm.Registry
	MCP       mcp.Pool
}

func New(opts Options) (*Core, error) {
	if opts.DataDir == "" {
		return nil, errors.New("core: DataDir required")
	}
	return &Core{opts: opts}, nil
}

func (c *Core) Start(ctx context.Context) error {
	if c.Scheduler != nil {
		return c.Scheduler.Start(ctx)
	}
	return nil
}

func (c *Core) Shutdown(ctx context.Context) error {
	if c.Scheduler != nil {
		_ = c.Scheduler.Stop(ctx)
	}
	if c.MCP != nil {
		_ = c.MCP.Close(ctx)
	}
	if c.Events != nil {
		_ = c.Events.Close()
	}
	return nil
}

func (c *Core) DataDir() string { return c.opts.DataDir }
