//go:build !serve

package rpc

// wf_adapters_notifier.go — wfNotifierAdapter for the desktop (Wails) build.
//
// In the desktop build the Wails runtime is present and
// runtime.SendNotification dispatches a real OS notification.
// The serve build provides a no-op stub in wf_adapters_notifier_serve.go.

import (
	"context"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

// wfNotifierAdapter satisfies corewf.Notifier via the Wails runtime
// SendNotification call. The ctxFn is invoked on each Notify call to
// obtain the live Wails context (typically broker.EmitCtx()), so the
// adapter is safe to construct before the Wails OnStartup context is
// set — the fn is not called until the first notification fires.
type wfNotifierAdapter struct {
	ctxFn func() context.Context
}

// Notify implements corewf.Notifier using the Wails OS notification API.
func (a *wfNotifierAdapter) Notify(_ context.Context, title, body string) error {
	ctx := a.ctxFn()
	return wruntime.SendNotification(ctx, wruntime.NotificationOptions{
		Title:    title,
		Subtitle: "",
		Body:     body,
	})
}

var _ corewf.Notifier = (*wfNotifierAdapter)(nil)
