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

// ErrNoPublishedRelease is returned by PinnedEngineRelease when this
// build carries no engine pin (the zero-value pinned_release_gen.go).
var ErrNoPublishedRelease = errors.New("the Kameas ML engine is not published to the release channel yet")

// ReleaseSource resolves the harness's current engine pin.
type ReleaseSource func(ctx context.Context) (EngineRelease, error)

// PinnedEngineRelease is the production ReleaseSource: the build-time pin
// in pinned_release_gen.go (engine-publication-01ENPUB01 WP-H3), or
// ErrNoPublishedRelease when that pin is the zero value.
//
// The pin is written by `go run ./cmd/kenaz-ml-sign pin-gen` from the
// engine release the kenaz-ml publish job put on the env-specific
// channel (https://<env downloads>/kenaz-ml/<ver>/, a .dmg plus its raw
// ed25519 .sig). The download -> verify-over-DMG-bytes -> mount -> copy
// -> clear-quarantine -> start flow behind it is built and tested
// (install_dmg_test.go; cmd/kenaz-ml-sign's sign->Install proof).
//
// DATED NOTE (2026-10-04, owner: release-infra): the checked-in pin is
// zero, so every build still honestly reports the action unavailable.
// The BLOCKER is external (mission spec §External): kameas-infra must
// extend gh-deploy-<env> OIDC trust to kenaz-ml, and the owner must
// generate the release keypair (kenaz-ml-sign keygen), set
// KENAZ_ML_RELEASE_SIGNING_KEY on kenaz-ml and commit the public key
// into release_signing_key.pub. When the first signed engine is
// published, enable the commented engine-pin step in
// .github/workflows/release.yml (it runs pin-gen before wails build);
// this note is deleted in that change.
func PinnedEngineRelease(context.Context) (EngineRelease, error) {
	r := pinnedRelease
	if r.Version == "" {
		return EngineRelease{}, ErrNoPublishedRelease
	}
	if r.Signature != nil {
		sig := *r.Signature
		r.Signature = &sig // callers must not be able to mutate the pin
	}
	return r, nil
}

// SetPinnedReleaseForTesting replaces the build-time pin and returns a
// restore func. Test seam only — production sets the pin exclusively by
// regenerating pinned_release_gen.go. Exported because the Status/Enable
// views that consume PinnedEngineRelease live in core/rpc/views/sidecar.
func SetPinnedReleaseForTesting(r EngineRelease) (restore func()) {
	saved := pinnedRelease
	pinnedRelease = r
	return func() { pinnedRelease = saved }
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
