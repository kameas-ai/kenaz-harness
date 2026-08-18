// Package update is the view-scoped RPC accessor for the auto-update
// subsystem (mission auto-update, v0.4.0).
//
// The frontend update banner + settings panel read state through Status
// and drive the lifecycle through StartCheck → StartDownload → Apply.
// Background polling is owned by core/update.Service.BackgroundPoll;
// progress events are fanned to the frontend on the broker topics
// declared in this package.
//
// Privacy: this view never logs version-string content from the
// manifest body. Audit attrs only carry kinds + booleans (e.g.
// "available=true", "state=downloading"); release notes are passed
// through unchanged in the StatusOutput payload but not logged.
package update

import "context"

// StatusOutput is the consolidated wire shape the frontend reads each
// time it renders the update banner / settings panel. Every field is
// JSON-tagged with a frontend-stable name; new fields are additive.
type StatusOutput struct {
	// CurrentVersion is the running build's semver string.
	CurrentVersion string `json:"currentVersion"`
	// Available is true iff a newer version exists and the user has
	// not skipped it.
	Available bool `json:"available"`
	// AvailableVersion is the manifest's `version` field. Empty when
	// no check has run yet or the most recent check failed.
	AvailableVersion string `json:"availableVersion,omitempty"`
	// Channel is "stable" or "prerelease".
	Channel string `json:"channel"`
	// DownloadState is one of "idle" | "downloading" | "staged" | "failed".
	DownloadState string `json:"downloadState"`
	// DownloadProgress is the most recent percent (0-100) reported by
	// the download pump. 0 when DownloadState is not "downloading".
	DownloadProgress int `json:"downloadProgress,omitempty"`
	// DownloadError is the reason the most recent download attempt
	// failed — set by markFailed alongside DownloadState=="failed",
	// carrying the same string TopicDownloadFailed publishes. Cleared
	// by the next StartDownload. Never the manifest version (DC-7:
	// only the error message goes on the wire).
	DownloadError string `json:"downloadError,omitempty"`
	// Notes is the manifest-supplied release-notes blurb (or URL).
	// Opaque to this package; not logged.
	Notes string `json:"notes,omitempty"`
	// ReleaseURL is a deep link to the release page (currently
	// derived as the manifest Notes when it looks like a URL; left
	// empty otherwise).
	ReleaseURL string `json:"releaseUrl,omitempty"`
	// SkippedByUser is true iff the user previously skipped the
	// available version.
	SkippedByUser bool `json:"skippedByUser"`
	// LastCheckedAt is the unix-seconds timestamp of the most recent
	// successful Check. 0 when no check has run yet.
	LastCheckedAt int64 `json:"lastCheckedAt,omitempty"`
}

// UpdateAPI is the view-scoped accessor.
//
// A nil/disabled implementation surfaces ErrServiceUnavailable on every
// state-mutating method and a zero StatusOutput (with CurrentVersion
// populated when available) on Status, so the frontend can render an
// empty state without the chassis crashing.
type UpdateAPI interface {
	// Status returns the current wire view. Cheap; reads only the
	// in-memory mirror — does NOT trigger a network check.
	Status(ctx context.Context) (StatusOutput, error)
	// StartCheck triggers a synchronous manifest fetch and updates the
	// in-memory state. Returns nil on success even if no update is
	// available; returns the underlying network/parse error otherwise.
	StartCheck(ctx context.Context) error
	// StartDownload begins (or resumes) downloading the staged
	// artifact for the most recent Available info. Spawns a goroutine
	// that drains the progress channel and republishes ticks on the
	// broker topic TopicDownloadProgress. Returns immediately; UI
	// follows via Status polls or the broker stream. Returns
	// ErrNoUpdateAvailable when no actionable Info is cached.
	StartDownload(ctx context.Context) error
	// Apply calls Service.ApplyAndRestart with the staged artifact
	// from the most recent successful download. Returns
	// ErrNothingStaged when no download has completed.
	Apply(ctx context.Context) error
	// SkipVersion records the given version as skipped and refreshes
	// the in-memory mirror so subsequent Status returns
	// SkippedByUser=true.
	SkipVersion(ctx context.Context, version string) error
	// ListSkippedVersions returns the user's persisted skip set.
	ListSkippedVersions(ctx context.Context) ([]string, error)
	// UnskipVersion removes the given version from the skip set so the
	// banner re-appears.
	UnskipVersion(ctx context.Context, version string) error
}

// Broker topics this view emits on. Frontend's update banner /
// settings panel listen on these via the existing useEventStream
// composable.
const (
	// TopicDownloadProgress carries DownloadProgressPayload ticks
	// from the download pump. Emitted at most ~10/s while a download
	// is in flight.
	TopicDownloadProgress = "update:download-progress"
	// TopicDownloadComplete carries StagedPayload on a successful
	// sha-verified download.
	TopicDownloadComplete = "update:download-complete"
	// TopicDownloadFailed carries DownloadFailedPayload when the pump
	// reports a terminal error (network, sha mismatch, write error).
	TopicDownloadFailed = "update:download-failed"
)

// DownloadProgressPayload is the wire shape for TopicDownloadProgress.
type DownloadProgressPayload struct {
	Bytes   int64 `json:"bytes"`
	Total   int64 `json:"total"`
	Percent int   `json:"percent"`
}

// StagedPayload is the wire shape for TopicDownloadComplete. The
// manager's `Apply` does not need this — it reads the cached staged
// update directly — but the frontend uses TargetVersion to render the
// "ready to install" CTA.
type StagedPayload struct {
	TargetVersion string `json:"targetVersion"`
}

// DownloadFailedPayload is the wire shape for TopicDownloadFailed. We
// pass the error message through unchanged; the manifest-supplied
// version is excluded so the privacy invariant (no manifest body
// content in audit-style channels) holds.
type DownloadFailedPayload struct {
	Err string `json:"err"`
}
