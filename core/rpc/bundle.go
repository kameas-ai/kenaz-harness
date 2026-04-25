package rpc

import (
	"context"
	"errors"

	"github.com/sigil-tech/kaneaz-harness/core"
	"github.com/sigil-tech/kaneaz-harness/core/bundle"
)

type BundleAPI struct{ c *core.Core }

func NewBundleAPI(c *core.Core) *BundleAPI { return &BundleAPI{c: c} }

func (b *BundleAPI) Resolve(ctx context.Context, set []bundle.Dependency) (*bundle.Lockfile, error) {
	if b.c.Bundles == nil {
		return nil, errors.New("bundle: resolver not configured")
	}
	return b.c.Bundles.Resolve(ctx, set)
}

func (b *BundleAPI) Materialize(ctx context.Context, lock *bundle.Lockfile) (*bundle.Materialized, error) {
	if b.c.Bundles == nil {
		return nil, errors.New("bundle: resolver not configured")
	}
	return b.c.Bundles.Materialize(ctx, lock)
}
