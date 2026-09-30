package mlsidecar

import (
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
)

// EngineRelease is the harness's pin of ONE engine release (design §6.4:
// "Engine semver — pinned in each client's release manifest"): which
// version this harness build installs, where from, what its bytes must
// hash to, and how large the download is (disclosed to the user BEFORE
// they click — spec §2c).
type EngineRelease struct {
	Version        string
	ChannelKind    string
	ChannelURL     string
	ChannelPath    string
	ArtifactPath   string // channel-relative path of the signed+notarized .dmg
	ExpectedSHA256 string // "sha256:<hex>" over the DMG bytes as published
	Signature      *manifest.SignatureRef
	SizeBytes      int64  // download size, for the pre-download disclosure
	Source         string // provenance string recorded into install.json
}

// InstallRequest converts the pin into the Install input.
func (r EngineRelease) InstallRequest() InstallRequest {
	return InstallRequest{
		ChannelKind:    r.ChannelKind,
		ChannelURL:     r.ChannelURL,
		ChannelPath:    r.ChannelPath,
		Version:        r.Version,
		ArtifactPath:   r.ArtifactPath,
		ExpectedSHA256: r.ExpectedSHA256,
		Signature:      r.Signature,
		Source:         r.Source,
	}
}

// SizeMB is the disclosed download size in whole megabytes, rounded up
// (never understates), 0 when unknown.
func (r EngineRelease) SizeMB() int {
	if r.SizeBytes <= 0 {
		return 0
	}
	const mb = 1 << 20
	return int((r.SizeBytes + mb - 1) / mb)
}

// ErrNoPublishedRelease is returned by PinnedEngineRelease until the
// engine artifact is published to the kameas release channel.
var ErrNoPublishedRelease = errors.New("the Kameas ML engine is not published to the release channel yet")

// ReleaseSource resolves the harness's current engine pin.
type ReleaseSource func(ctx context.Context) (EngineRelease, error)

// PinnedEngineRelease is the production ReleaseSource.
//
// DATED NOTE (2026-09-30, owner: release-infra / laya-advisors WP13
// follow-up): this returns ErrNoPublishedRelease because the prod
// download URL + channel wiring does not exist yet. The BLOCKER is the
// channel-publishing infra task — publishing kenaz-ml CI's
// `kenaz-ml-macos-arm64-notarized` .dmg into the env-specific kameas
// release channel WITH an A-1 manifest signed by an anchor the harness
// trusts. The entire download -> verify-over-DMG-bytes -> mount -> copy ->
// clear-quarantine -> start flow is built and tested against a local
// fixture (install_dmg_test.go); this function is the single line that
// changes (to return the pinned EngineRelease from the harness release
// manifest) when that infra exists. Until then the Settings surface
// honestly reports the action as unavailable rather than offering a
// button that cannot work.
func PinnedEngineRelease(context.Context) (EngineRelease, error) {
	return EngineRelease{}, ErrNoPublishedRelease
}

// PlatformSupport reports whether the engine artifact exists for this
// platform: today only the notarized macOS arm64 DMG does (design §6.3 —
// Windows has no freeze target; the Linux artifact is not a DMG and is
// not published by the kenaz-ml job WP13 targets). The reason is the
// user-facing sentence.
func PlatformSupport(goos, goarch string) (bool, string) {
	if goos == "darwin" && goarch == "arm64" {
		return true, ""
	}
	return false, fmt.Sprintf("Local recommendations are available on macOS with Apple silicon only (this device: %s/%s).", goos, goarch)
}

// CurrentPlatformSupport is PlatformSupport for the running process.
func CurrentPlatformSupport() (bool, string) { return PlatformSupport(runtime.GOOS, runtime.GOARCH) }
