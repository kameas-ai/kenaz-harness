//go:build serve

package rpc

// wf_adapters_notifier_serve.go — wfNotifierAdapter stub for the serve (headless) build.
//
// In serve mode there is no Wails frontend to dispatch OS notifications
// through; the notify step soft-fails with ErrNotifyTargetUnconfigured
// for the "os" surface (the same behaviour as when Notifier is nil),
// which the notifyRunner logs as "unconfigured" and does not fail the run.

import (
	"context"
	"fmt"

	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

// wfNotifierAdapter satisfies corewf.Notifier. In serve mode OS
// notifications are not available so Notify always returns an error
// that isUnconfigured() in runners_notify.go treats as a soft failure.
type wfNotifierAdapter struct {
	ctxFn func() context.Context
}

// Notify returns a soft-fail error indicating OS notifications are not
// available in headless serve mode.
func (a *wfNotifierAdapter) Notify(_ context.Context, _, _ string) error {
	return fmt.Errorf("os notifications unavailable in headless serve mode: %w", corewf.ErrNotifyTargetUnconfigured)
}

var _ corewf.Notifier = (*wfNotifierAdapter)(nil)
