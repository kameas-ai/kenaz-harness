// audit.go — auto-update audit emission helpers (auto-update mission,
// v0.4.0 WP06).
//
// These helpers are thin wrappers around audit.Emit so the Service
// methods (Check / Download / ApplyAndRestart / SkipVersion) can fire
// lifecycle events without re-implementing the payload-marshalling
// boilerplate. The privacy invariant lives here: every payload field
// is either a version string, a size, a duration, a platform tuple, a
// boolean, or a typed error class label. Manifest URLs, download
// URLs, manifest body bytes, and release-notes content are NEVER
// recorded.
package update

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// emitChecked records a completed Service.Check (or BackgroundPoll
// tick) on the audit log. resultVersion is the manifest's advertised
// version (empty when the call returned no Info.AvailableVersion);
// took is the wall-clock cost of the manifest fetch + parse.
//
// Caller wires audit.Emitter through Config.Audit; nil is a no-op so
// the test harness and the test-chassis path can run without a wired
// emitter.
func emitChecked(ctx context.Context, em audit.Emitter, channel, resultVersion string, took time.Duration) {
	if em == nil {
		return
	}
	ms := took.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	audit.MustEmit(ctx, em, audit.KindUpdateChecked,
		audit.UpdateCheckedAttrs{
			Channel:       channel,
			ResultVersion: resultVersion,
			Took:          int(ms),
		}, time.Now().UTC())
}

// emitAvailable records the false→true transition of Info.Available
// during a BackgroundPoll tick (a manifest-side promotion to a newer
// release the user has not yet skipped).
func emitAvailable(ctx context.Context, em audit.Emitter, current, available, channel string) {
	if em == nil {
		return
	}
	audit.MustEmit(ctx, em, audit.KindUpdateAvailable,
		audit.UpdateAvailableAttrs{
			CurrentVersion:   current,
			AvailableVersion: available,
			Channel:          channel,
		}, time.Now().UTC())
}

// emitDownloaded records a verified Service.Download success.
func emitDownloaded(ctx context.Context, em audit.Emitter, version string, bytes int64) {
	if em == nil {
		return
	}
	audit.MustEmit(ctx, em, audit.KindUpdateDownloaded,
		audit.UpdateDownloadedAttrs{
			Version:     version,
			Bytes:       bytes,
			Sha256Match: true,
		}, time.Now().UTC())
}

// emitApplied is fired immediately BEFORE the platform Swap call in
// Service.ApplyAndRestart, so the event lands in the audit log even if
// the subsequent fork-exec fails (Restart never returns on the
// macOS/Linux happy path).
func emitApplied(ctx context.Context, em audit.Emitter, from, to, platform string) {
	if em == nil {
		return
	}
	audit.MustEmit(ctx, em, audit.KindUpdateApplied,
		audit.UpdateAppliedAttrs{
			FromVersion: from,
			ToVersion:   to,
			Platform:    platform,
		}, time.Now().UTC())
}

// emitSkipped records a successful SkipVersion call.
func emitSkipped(ctx context.Context, em audit.Emitter, version, reason string) {
	if em == nil {
		return
	}
	audit.MustEmit(ctx, em, audit.KindUpdateSkipped,
		audit.UpdateSkippedAttrs{
			Version: version,
			Reason:  reason,
		}, time.Now().UTC())
}

// emitFailed records a classified failure from Check / Download /
// Apply. The raw error message is NEVER recorded — only the
// classification — so the audit log stays free of URL fragments or
// user-input bytes.
func emitFailed(ctx context.Context, em audit.Emitter, action string, err error) {
	if em == nil || err == nil {
		return
	}
	audit.MustEmit(ctx, em, audit.KindUpdateFailed,
		audit.UpdateFailedAttrs{
			Action:     action,
			ErrorClass: classifyUpdateError(err),
		}, time.Now().UTC())
}

// classifyUpdateError reduces a raw error to one of the typed classes
// the audit consumer pivots on. The classification is deliberately
// conservative — unknown errors collapse to "other" so a future error
// shape doesn't accidentally leak a new error string into the log.
func classifyUpdateError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errSha256Mismatch) {
		return "sha_mismatch"
	}
	if errors.Is(err, errSwapNotSupported) {
		return "swap_failed"
	}
	if errors.Is(err, errManifestNotFound) {
		return "manifest_invalid"
	}
	// Network errors: any net.Error or wrapped one. We probe via
	// errors.As so wrapped DNS / dial / read errors all classify
	// uniformly.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return "network"
	}
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "missing version"),
		strings.Contains(low, "decode manifest"),
		strings.Contains(low, "manifest"):
		return "manifest_invalid"
	case strings.Contains(low, "swap"),
		strings.Contains(low, "rename"):
		return "swap_failed"
	case strings.Contains(low, "sha256"),
		strings.Contains(low, "digest"):
		return "sha_mismatch"
	case strings.Contains(low, "network"),
		strings.Contains(low, "connection"),
		strings.Contains(low, "timeout"),
		strings.Contains(low, "deadline"),
		strings.Contains(low, "dial"),
		strings.Contains(low, "tls"),
		strings.Contains(low, "no such host"),
		strings.Contains(low, "fetch manifest"),
		strings.Contains(low, "get http"),
		strings.Contains(low, "read body"),
		strings.Contains(low, "returned 5"),
		strings.Contains(low, "returned 4"):
		return "network"
	default:
		return "other"
	}
}
