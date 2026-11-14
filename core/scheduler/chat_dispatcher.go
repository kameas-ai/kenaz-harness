// Package scheduler — chat-run dispatcher for scheduled-chat-runs-01KX5R8B.
//
// ChatRunDispatcher is the dispatch contract for the scheduled-chat-run job
// kind. It is intentionally narrow: real wiring (LLM stack, Wails events)
// lives in the rpc layer that constructs the concrete implementation; tests
// inject a stub.
package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// ChatRunDispatcher executes a scheduled chat-run job.
//
// Implementations must be safe for concurrent use (the cron engine fires
// jobs from a pool goroutine).
type ChatRunDispatcher interface {
	// DispatchChatRun renders the prompt template, creates a headless
	// session, runs the prompt, and routes output to the configured sink.
	// Returns a ChatRunHistoryRecord populated with the outcome; the
	// caller persists it via ScheduledChatStore.AppendHistory.
	DispatchChatRun(ctx context.Context, job Job, now time.Time) (ChatRunHistoryRecord, error)
}

// RunStartReporter is implemented by a ChatRunDispatcher that calls
// MarkRunDispatched before a run's first durable effect. The cron engine
// uses it to tell a fire that engine Stop cancelled before anything was
// dispatched (a one-shot stays enabled and fires on the next start) from
// one cancelled after the run began (the attempt is used up, so tools
// with side effects never run twice). A dispatcher that does not
// implement it is treated as having started the run as soon as
// DispatchChatRun is called — the safe direction.
type RunStartReporter interface {
	ReportsRunStart()
}

type dispatchMarkKey struct{}

// MarkRunDispatched records, on a ctx the cron engine handed to
// DispatchChatRun, that the run has started its first durable effect
// (the prompt turn is about to be written). A no-op on any other ctx
// (RunNow, tests).
func MarkRunDispatched(ctx context.Context) {
	if m, ok := ctx.Value(dispatchMarkKey{}).(*atomic.Bool); ok {
		m.Store(true)
	}
}

// WithDispatchMark returns ctx carrying a fresh dispatch mark and a
// func reporting whether MarkRunDispatched was called on it. The cron
// engine uses it around every DispatchChatRun; dispatcher tests use it to
// check where a RunStartReporter marks.
func WithDispatchMark(ctx context.Context) (context.Context, func() bool) {
	m := new(atomic.Bool)
	return context.WithValue(ctx, dispatchMarkKey{}, m), m.Load
}

// RenderPromptTemplate substitutes the standard variables in a prompt
// template string and returns the rendered prompt.
//
// Supported variables:
//
//	{{date}}      — now formatted as "2006-01-02" (UTC)
//	{{time}}      — now formatted as "15:04" (UTC)
//	{{cron_expr}} — the cron expression from job.Trigger.Cron
func RenderPromptTemplate(tmpl string, job Job, now time.Time) string {
	utc := now.UTC()
	r := strings.NewReplacer(
		"{{date}}", utc.Format("2006-01-02"),
		"{{time}}", utc.Format("15:04"),
		"{{cron_expr}}", job.Trigger.Cron,
	)
	return r.Replace(tmpl)
}

// ParseOutputSink decomposes a raw output_sink value into a kind and an
// optional file path.
//
//   - "banner"        → kind="banner", path=""
//   - "file:/tmp/out" → kind="file",   path="/tmp/out"
//   - "none"          → kind="none",   path=""
//   - ""              → kind="banner", path=""  (default)
//
// Any unrecognised prefix is treated as "none" and the raw value is
// included in the returned error for diagnostics.
func ParseOutputSink(raw string) (kind, path string, err error) {
	if raw == "" || raw == "banner" {
		return "banner", "", nil
	}
	if raw == "none" {
		return "none", "", nil
	}
	if strings.HasPrefix(raw, "file:") {
		p := strings.TrimPrefix(raw, "file:")
		if p == "" {
			return "file", "", fmt.Errorf("scheduler: output_sink file path is empty")
		}
		return "file", p, nil
	}
	return "none", "", fmt.Errorf("scheduler: unrecognised output_sink %q", raw)
}

// NoopChatRunDispatcher does not exist. A nil ChatRunDispatcher is a
// configuration error, not a fallback: no production or test caller may
// fabricate a "completed" outcome for a run that did not happen (FR-002).
// See core/rpc/views/scheduledchat/impl.go RunNow, which returns
// ErrDispatcherUnavailable instead.
