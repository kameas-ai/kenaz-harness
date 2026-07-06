package menu

import "context"

// UpdateCheckerFunc adapts any func(ctx context.Context) to UpdateChecker.
// This allows main.go to pass a closure rather than wiring the full
// update.Manager interface into this package.
type UpdateCheckerFunc func(ctx context.Context)

// CheckNow implements UpdateChecker.
func (f UpdateCheckerFunc) CheckNow(ctx context.Context) { f(ctx) }

// RecentSessionsFetcher is the interface the menu controller uses to
// lazily populate File → Open Recent. Implemented by
// core/rpc/views/sessions or similar; tests inject a fake.
type RecentSessionsFetcher interface {
	// FetchRecent returns up to limit sessions ordered by most-recently
	// active first.
	FetchRecent(ctx context.Context, limit int) ([]SessionRef, error)
}

// RecentSessionsFetcherFunc adapts a func to RecentSessionsFetcher.
type RecentSessionsFetcherFunc func(ctx context.Context, limit int) ([]SessionRef, error)

func (f RecentSessionsFetcherFunc) FetchRecent(ctx context.Context, limit int) ([]SessionRef, error) {
	return f(ctx, limit)
}
