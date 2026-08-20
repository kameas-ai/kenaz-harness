package sentry

import (
	"time"

	gosentry "github.com/getsentry/sentry-go"
)

// liveClient is the Sentry-SDK-backed implementation of Client.
// It is only active when Init is called with a non-Off tier and the kill-
// switch is not set. All events pass through the Redactor before transmission.
type liveClient struct {
	tier Tier
}

func (c *liveClient) Init(tier Tier, dsn, release, gitsha string) error {
	c.tier = tier
	opts := gosentry.ClientOptions{
		Dsn:              dsn,
		Release:          release,
		Dist:             gitsha,
		AttachStacktrace: true,
		// Never send PII by default.
		SendDefaultPII: false,
		// Wire in our custom hook that redacts before sending.
		BeforeSend: c.beforeSend,
	}
	return gosentry.Init(opts)
}

// beforeSend is the sentry SDK hook that fires before each event is
// transmitted. We redact the event in place and return nil to drop it if
// the redactor itself panics (belt-and-suspenders privacy guard).
func (c *liveClient) beforeSend(event *gosentry.Event, _ *gosentry.EventHint) *gosentry.Event {
	defer func() {
		if r := recover(); r != nil {
			// If the redactor panics, swallow the event rather than
			// transmitting potentially un-redacted content.
			event = nil
		}
	}()
	return redactEvent(event)
}

func (c *liveClient) CaptureException(err error, extra map[string]any) {
	if err == nil {
		return
	}
	gosentry.WithScope(func(scope *gosentry.Scope) {
		if len(extra) > 0 {
			redacted := RedactMap(extra)
			scope.SetContext("extra", redacted)
		}
		addBreadcrumbsToScope(scope)
		gosentry.CaptureException(err)
	})
}

func (c *liveClient) CaptureMessage(msg string, level string, extra map[string]any) {
	msg = RedactString(msg)
	gosentry.WithScope(func(scope *gosentry.Scope) {
		if len(extra) > 0 {
			redacted := RedactMap(extra)
			scope.SetContext("extra", redacted)
		}
		addBreadcrumbsToScope(scope)
		sentryLevel := gosentry.LevelInfo
		switch level {
		case "debug":
			sentryLevel = gosentry.LevelDebug
		case "warning", "warn":
			sentryLevel = gosentry.LevelWarning
		case "error":
			sentryLevel = gosentry.LevelError
		case "fatal":
			sentryLevel = gosentry.LevelFatal
		}
		scope.SetLevel(sentryLevel)
		gosentry.CaptureMessage(msg)
	})
}

func (c *liveClient) AddBreadcrumb(b Breadcrumb) {
	// Add to our local ring buffer first.
	globalBreadcrumbs.Add(b)
	// Also push to the SDK's breadcrumb list.
	gosentry.AddBreadcrumb(&gosentry.Breadcrumb{
		Timestamp: b.TS,
		Level:     gosentry.Level(b.Level),
		Message:   RedactString(b.Message),
		Data:      RedactMap(b.Data),
	})
}

func (c *liveClient) Flush(timeout time.Duration) {
	gosentry.Flush(timeout)
}

// SetUser tags (identified=true) or clears (identified=false) the
// scope-wide Sentry user identity. controls-and-readouts-that-tell-the-
// truth-01PMZ808 WP12 (FR-016). Deliberately does not thread real PII
// (email, fleet user id) — SendDefaultPII stays false; ID is a fixed
// non-identifying marker distinguishing "an identified-tier, fleet-
// signed-in user" from "anonymous" in the Sentry UI, which is what
// TierIdentified's own doc promises ("attaches the user's fleet
// identity as a Sentry user tag") without over-collecting.
func (c *liveClient) SetUser(identified bool) {
	gosentry.ConfigureScope(func(scope *gosentry.Scope) {
		if identified {
			scope.SetUser(gosentry.User{ID: "fleet-identified"})
		} else {
			scope.SetUser(gosentry.User{})
		}
	})
}

// addBreadcrumbsToScope injects our ring-buffer breadcrumbs into the scope.
// The SDK already collects its own, but we re-add ours so breadcrumbs
// collected via the global RingBuffer (e.g. from the slog handler before
// scope construction) are included.
func addBreadcrumbsToScope(scope *gosentry.Scope) {
	for _, b := range globalBreadcrumbs.Snapshot() {
		scope.AddBreadcrumb(&gosentry.Breadcrumb{
			Timestamp: b.TS,
			Level:     gosentry.Level(b.Level),
			Message:   RedactString(b.Message),
			Data:      RedactMap(b.Data),
		}, breadcrumbCap)
	}
}

// redactEvent applies the full privacy rule set to a gosentry.Event.
// It modifies the event in place and returns it.
func redactEvent(event *gosentry.Event) *gosentry.Event {
	if event == nil {
		return nil
	}

	// Redact the top-level message.
	event.Message = RedactString(event.Message)

	// Redact Context fields (where extra data lands in sentry-go v0.4x+).
	// gosentry.Context is map[string]interface{}, so ctxVal is already
	// that type — no type assertion needed.
	for ctxKey, ctxVal := range event.Contexts {
		redacted := make(gosentry.Context, len(ctxVal))
		for k, v := range ctxVal {
			if ShouldDropSlogKey(k) {
				continue
			}
			switch val := v.(type) {
			case string:
				redacted[k] = RedactStringDeep(val)
			default:
				redacted[k] = v
			}
		}
		event.Contexts[ctxKey] = redacted
	}

	// Redact Tags.
	for k, v := range event.Tags {
		event.Tags[k] = RedactString(v)
	}

	// Redact exception values and their stack frames.
	for i := range event.Exception {
		ex := &event.Exception[i]
		ex.Value = RedactString(ex.Value)
		if ex.Stacktrace != nil {
			for j := range ex.Stacktrace.Frames {
				f := &ex.Stacktrace.Frames[j]
				f.Filename = RedactString(f.Filename)
				f.AbsPath = RedactString(f.AbsPath)
				f.Module = RedactString(f.Module)
				// Redact local vars.
				for k, v := range f.Vars {
					switch val := v.(type) {
					case string:
						f.Vars[k] = RedactStringDeep(val)
					}
				}
			}
		}
	}

	// Redact breadcrumbs embedded in the event.
	for _, b := range event.Breadcrumbs {
		if b == nil {
			continue
		}
		b.Message = RedactString(b.Message)
		redactedData := make(map[string]any, len(b.Data))
		for k, v := range b.Data {
			if ShouldDropSlogKey(k) {
				continue
			}
			switch val := v.(type) {
			case string:
				redactedData[k] = RedactStringDeep(val)
			default:
				redactedData[k] = v
			}
		}
		b.Data = redactedData
	}

	return event
}
