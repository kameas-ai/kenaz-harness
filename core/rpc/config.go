package rpc

import (
	"context"
	"errors"

	"github.com/sigil-tech/kaneaz-harness/core"
	"github.com/sigil-tech/kaneaz-harness/core/config"
)

type ConfigAPI struct{ c *core.Core }

func NewConfigAPI(c *core.Core) *ConfigAPI { return &ConfigAPI{c: c} }

func (cfg *ConfigAPI) Current(ctx context.Context) (*config.Document, error) {
	if cfg.c.Config == nil {
		return nil, errors.New("config: store not configured")
	}
	return cfg.c.Config.Current(ctx)
}

func (cfg *ConfigAPI) Put(ctx context.Context, doc *config.Document) (*config.Document, error) {
	if cfg.c.Config == nil {
		return nil, errors.New("config: store not configured")
	}
	return cfg.c.Config.Put(ctx, doc)
}

func (cfg *ConfigAPI) History(ctx context.Context, limit int) ([]config.Document, error) {
	if cfg.c.Config == nil {
		return nil, errors.New("config: store not configured")
	}
	return cfg.c.Config.History(ctx, limit)
}
