package bundle

import "context"

type Store interface {
	Has(ctx context.Context, digest string) (bool, error)
	Fetch(ctx context.Context, dep Dependency) (digest string, root string, err error)
	Path(digest string) string
}
