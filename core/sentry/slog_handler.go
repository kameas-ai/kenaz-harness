package sentry

import (
	"context"
	"log/slog"
	"time"
)

// SlogHandler wraps an inner slog.Handler and intercepts ERROR-level records.
// For each ERROR, it:
//  1. Applies the redactor to the message and all attributes.
//  2. Drops any attribute whose key starts with "private.".
//  3. Appends a redacted Breadcrumb to the global ring buffer (so the context
//     is available when the next panic / exception event fires).
//
// It does NOT send ERROR records directly to Sentry — that would double-count
// when the error is also captured via CaptureException. The breadcrumb path
// ensures context without duplication.
//
// wire-up point 2 for sentry: core/logging/setup.go chains this handler when
// tier != Off (sentry-error-monitoring-01KX5R8G WP03).
type SlogHandler struct {
	Inner slog.Handler
}

var _ slog.Handler = (*SlogHandler)(nil)

// Enabled delegates to the inner handler.
func (h *SlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.Inner.Enabled(ctx, level)
}

// Handle processes the record. For ERROR records, a redacted breadcrumb is
// appended to the ring buffer after the inner handler runs.
func (h *SlogHandler) Handle(ctx context.Context, r slog.Record) error {
	// Run the inner handler first so log output is always written.
	err := h.Inner.Handle(ctx, r)

	if r.Level >= slog.LevelError {
		b := Breadcrumb{
			TS:      r.Time,
			Level:   "error",
			Message: RedactString(r.Message),
		}
		// Collect attributes from the record.
		data := make(map[string]any)
		r.Attrs(func(a slog.Attr) bool {
			if ShouldDropSlogKey(a.Key) {
				return true
			}
			v := a.Value.Any()
			switch val := v.(type) {
			case string:
				data[a.Key] = RedactStringDeep(val)
			default:
				data[a.Key] = v
			}
			return true
		})
		if len(data) > 0 {
			b.Data = data
		}
		// Set timestamp if zero (rare in production but common in tests).
		if b.TS.IsZero() {
			b.TS = time.Now().UTC()
		}
		globalBreadcrumbs.Add(b)
	}

	return err
}

// WithAttrs returns a new handler with the given attributes pre-applied.
// Attributes with private. keys are dropped before forwarding.
func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	filtered := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		if !ShouldDropSlogKey(a.Key) {
			filtered = append(filtered, a)
		}
	}
	return &SlogHandler{Inner: h.Inner.WithAttrs(filtered)}
}

// WithGroup delegates to the inner handler.
func (h *SlogHandler) WithGroup(name string) slog.Handler {
	return &SlogHandler{Inner: h.Inner.WithGroup(name)}
}
