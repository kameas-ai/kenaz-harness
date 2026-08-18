package menu

import "context"

// UpdateCheckerFunc adapts any func(ctx context.Context) to UpdateChecker.
// This allows main.go to pass a closure rather than wiring the full
// update.Manager interface into this package.
type UpdateCheckerFunc func(ctx context.Context)

// CheckNow implements UpdateChecker.
func (f UpdateCheckerFunc) CheckNow(ctx context.Context) { f(ctx) }

// UpdateControllerFuncs adapts three closures to UpdateChecker +
// UpdateDownloader + UpdateApplier in one value (self-update-repair
// -01PMUP01 WP05 — the Help menu now dispatches on all five
// UpdateMenuState values, not just idle, so main.go needs to wire more
// than CheckNow). Any nil func field is a safe no-op. main.go passes
// closures over rpc.API.UpdateStartCheck / UpdateStartDownload /
// UpdateApply rather than handing this package the full rpc surface —
// same reasoning as UpdateCheckerFunc above.
type UpdateControllerFuncs struct {
	CheckNowFunc      func(ctx context.Context)
	StartDownloadFunc func(ctx context.Context)
	ApplyFunc         func(ctx context.Context)
}

// CheckNow implements UpdateChecker.
func (f UpdateControllerFuncs) CheckNow(ctx context.Context) {
	if f.CheckNowFunc != nil {
		f.CheckNowFunc(ctx)
	}
}

// StartDownload implements UpdateDownloader.
func (f UpdateControllerFuncs) StartDownload(ctx context.Context) {
	if f.StartDownloadFunc != nil {
		f.StartDownloadFunc(ctx)
	}
}

// Apply implements UpdateApplier.
func (f UpdateControllerFuncs) Apply(ctx context.Context) {
	if f.ApplyFunc != nil {
		f.ApplyFunc(ctx)
	}
}

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
