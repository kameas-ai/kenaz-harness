package update

import (
	"context"
	"time"
)

// Info is the result of a manifest check. It is shaped for the
// frontend banner: every field needed to render "v0.4.0 is available,
// see what's new, install now, skip" is here.
type Info struct {
	// CurrentVersion is the running build's version string (from
	// main.Version, threaded into Service via Config). Always
	// populated, even when no update is available.
	CurrentVersion string `json:"currentVersion"`
	// AvailableVersion is the manifest's `version` field. Empty on a
	// manifest fetch error.
	AvailableVersion string `json:"availableVersion"`
	// Channel is "stable" or "prerelease".
	Channel string `json:"channel"`
	// Notes is a brief release-notes blurb (or a URL pointing at the
	// release page on docs.kameas.ai). Manifest-supplied; opaque to
	// this package.
	Notes string `json:"notes,omitempty"`
	// DownloadURL is the platform-specific binary URL pulled from
	// assets[].url for the current GOOS/GOARCH. Empty if the
	// manifest does not advertise an asset for this platform.
	DownloadURL string `json:"downloadUrl,omitempty"`
	// Sha256 is the hex digest from assets[].sha256. The Download
	// step verifies the streamed bytes against this value before
	// returning a StagedUpdate.
	Sha256 string `json:"sha256,omitempty"`
	// Available is true iff semver-compare(CurrentVersion,
	// AvailableVersion) < 0. False when equal, ahead (dev builds),
	// or when the manifest is unparseable.
	Available bool `json:"available"`
	// SkippedByUser is true iff the user previously clicked
	// "skip this version" on AvailableVersion. Frontend uses this
	// to suppress the banner without suppressing the underlying
	// "an update exists" signal.
	SkippedByUser bool `json:"skippedByUser"`
}

// DownloadProgress is one tick on the channel returned by
// Service.Download. The pump emits at most ~10/s; the final tick
// always has Done=true and either a nil or non-nil Err.
type DownloadProgress struct {
	// Bytes is the number of payload bytes streamed so far.
	Bytes int64 `json:"bytes"`
	// Total is the Content-Length header value if present, otherwise
	// 0 (caller should render an indeterminate spinner).
	Total int64 `json:"total"`
	// Done is true on the final tick of a single Download call.
	Done bool `json:"done"`
	// Err is non-nil on the final tick if the download failed
	// (network, sha mismatch, write error).
	Err error `json:"-"`
}

// StagedUpdate is the verified-but-not-yet-applied payload returned
// by Service.Download. The on-disk artifact lives under
// <DataDir>/update/staging/ and is owned by the update subsystem
// until ApplyAndRestart consumes it.
//
// StagedUpdate is opaque to callers — they pass it back into
// Service.ApplyAndRestart unchanged.
type StagedUpdate struct {
	// Path is the absolute path to the staged binary on disk. On
	// macOS this points at the "<DataDir>/update/staging/kenaz-harness.app.new"
	// bundle directory; on Linux/Windows at the staged executable.
	Path string `json:"path"`
	// TargetVersion is the version the staged artifact represents.
	TargetVersion string `json:"targetVersion"`
	// Sha256 is the verified hex digest of the staged artifact (the
	// caller can re-verify before swap if paranoid; ApplyAndRestart
	// does this internally).
	Sha256 string `json:"sha256"`
	// Platform is the GOOS/GOARCH tuple the artifact was built for.
	Platform string `json:"platform"`
}

// Publisher is the broker-emit surface the update service uses to
// announce a newly-available version. The rpc layer adapts its
// StreamBroker to this interface; tests can pass a recording fake.
// A nil Publisher is treated as "no broker installed" and silently
// disables event emission (BackgroundPoll keeps polling).
type Publisher interface {
	Publish(topic string, payload any)
}

// AvailableTopic is the broker topic BackgroundPoll publishes on
// when a new actionable update is found. Frontend's update banner
// listens here via the existing event-stream composable. Payload
// shape: Info (see this file).
const AvailableTopic = "update:available"

// Service is the public surface of core/update. The rpc layer
// constructs one at boot and exposes Check / Download /
// ApplyAndRestart / SkipVersion through view methods.
type Service interface {
	// Check fetches manifest.json (cheap; one HTTPS round-trip) and
	// returns the resolved Info. The returned Info always has
	// CurrentVersion populated; AvailableVersion may be empty if
	// the manifest fetch failed (the error explains why).
	Check(ctx context.Context) (Info, error)

	// Download streams the platform binary to a staged path under
	// <DataDir>/update/staging/. Verifies sha256 before returning.
	// Emits DownloadProgress events on the returned channel; the
	// channel closes after the final Done=true tick.
	Download(ctx context.Context, info Info) (<-chan DownloadProgress, StagedUpdate, error)

	// ApplyAndRestart performs the platform-specific install:
	//   macOS/Linux: swap binary, fork-exec new version, os.Exit(0)
	//   Windows:     write <DataDir>/update/pending.json marker, os.Exit(0)
	//                (boot-swap shim picks it up on next launch)
	//
	// The os.Exit / fork-exec are abstracted behind a Swapper so
	// tests can drive ApplyAndRestart without nuking the test process.
	ApplyAndRestart(ctx context.Context, staged StagedUpdate) error

	// SkipVersion marks a specific version as skipped. Subsequent
	// Check calls return Info{SkippedByUser: true} when the manifest
	// version equals the skipped one. Skipping is per-version: a
	// later release supersedes the skip.
	SkipVersion(ctx context.Context, version string) error

	// BackgroundPoll runs Check on the configured interval and emits
	// Info via the broker topic AvailableTopic when an actionable
	// update is found (Available=true && !SkippedByUser). Returns
	// when ctx is cancelled. The first poll runs immediately; an
	// error from the first poll is returned synchronously, later
	// errors are logged and retried.
	BackgroundPoll(ctx context.Context, interval time.Duration, channel string) error
}
