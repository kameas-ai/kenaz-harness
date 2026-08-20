package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
)

// manifest mirrors the JSON shape served at
// https://downloads.kameas.ai/kenaz-harness/manifest.json (the same file
// the docs download page consumes; published by release.yml). Unknown
// fields are ignored by encoding/json so future server-side fields don't
// break older clients.
type manifest struct {
	Version string          `json:"version"`
	Notes   string          `json:"notes,omitempty"`
	Assets  []manifestAsset `json:"assets"`
}

type manifestAsset struct {
	// Platform is the published "GOOS-GOARCH" string (e.g. "darwin-arm64").
	// Historic fixtures used a "GOOS/GOARCH" slash form; matching is
	// separator-insensitive (see pickAssetFor).
	Platform string `json:"platform"`
	// OS and Arch are the canonical match keys in the published manifest;
	// when present they are matched against runtime.GOOS/GOARCH directly,
	// which is robust to the platform-string separator.
	OS     string `json:"os,omitempty"`
	Arch   string `json:"arch,omitempty"`
	URL    string `json:"url"`
	Sha256 string `json:"sha256"`
}

// stableManifestURL and prereleaseManifestURL are the production
// manifest endpoints. Tests override them via Config.ManifestURL.
//
// These point at the release CDN (downloads.kameas.ai), which is where
// release.yml publishes the per-product manifest. The stage CNAME backs
// the prerelease (release-candidate) channel.
//
// frontend/src/components/updates/useUpdateStore.ts's MANIFEST_URL
// constant (the Layer-3 direct-fetch fallback) must equal
// stableManifestURL exactly (controls-and-readouts-that-tell-the-truth-
// 01PMZ808 WP08, AC-018) — it pointed at the wrong host AND path for the
// life of that fallback layer until WP08. Pinned on the TS side by
// useUpdateStore.fallback.spec.ts's "MANIFEST_URL (AC-018)" describe
// block; pinned here by TestStableManifestURL_MatchesFrontendConstant.
const (
	stableManifestURL     = "https://downloads.kameas.ai/kenaz-harness/manifest.json"
	prereleaseManifestURL = "https://stage-downloads.kameas.ai/kenaz-harness/manifest.json"
)

// channelManifestURL returns the manifest URL for the given channel.
// "prerelease" maps to the prerelease URL; everything else (including
// the empty string) maps to stable.
func channelManifestURL(channel string) string {
	if channel == "prerelease" {
		return prereleaseManifestURL
	}
	return stableManifestURL
}

// fetchManifest GETs the URL and decodes the JSON body. Network and
// decode errors are returned with enough context to debug from a log
// line alone; the manifest endpoint is operationally critical and
// silent failures are never acceptable.
//
// On a 404 for a prerelease URL the caller (Service.Check) falls
// back to the stable manifest with a TODO log line; this is the
// agreed contract until the prerelease channel ships its own JSON.
func fetchManifest(ctx context.Context, client *http.Client, url string) (manifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return manifest{}, fmt.Errorf("update: build manifest request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return manifest{}, fmt.Errorf("update: fetch manifest %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return manifest{}, errManifestNotFound
	}
	if resp.StatusCode/100 != 2 {
		return manifest{}, fmt.Errorf("update: manifest %s returned %s", url, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return manifest{}, fmt.Errorf("update: read manifest body: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return manifest{}, fmt.Errorf("update: decode manifest: %w", err)
	}
	if m.Version == "" {
		return manifest{}, fmt.Errorf("update: manifest %s missing version", url)
	}
	return m, nil
}

// pickAsset selects the asset entry whose Platform matches the running
// build (GOOS/GOARCH). Returns the asset and true, or an empty asset
// and false if no match.
func pickAsset(m manifest) (manifestAsset, bool) {
	return pickAssetFor(m, platformTuple())
}

// platformTuple returns "GOOS/GOARCH" for the running binary. Exposed
// as a separate fn so tests can stub it via the Service.platform
// override below.
func platformTuple() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
