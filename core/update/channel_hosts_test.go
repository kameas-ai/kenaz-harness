package update

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// releaseWorkflowCDNBase returns release.yml's CDN_BASE for one resolve-env
// target (dev | stage | prod), read from the case arm that publishes it.
func releaseWorkflowCDNBase(t *testing.T, target string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	re := regexp.MustCompile(`(?ms)^\s*` + regexp.QuoteMeta(target) + `\)\s*$.*?CDN_BASE="([^"]+)"`)
	m := re.FindSubmatch(raw)
	if m == nil {
		t.Fatalf("release.yml has no %s) arm with a CDN_BASE — the resolve-env shape changed; update this reader", target)
	}
	return string(m[1])
}

// TestChannelManifestURLs_MatchReleaseWorkflowCDN pins both update-channel
// manifest URLs to the CDN hosts release.yml actually publishes to, so the
// two cannot drift again. prereleaseManifestURL pointed at
// stage-downloads.kameas.ai — NXDOMAIN — from #168 (2026-06-11) to
// 2026-10-07; a DNS failure is not errManifestNotFound, so the stable
// fallback never ran and every Prerelease-channel update check failed.
func TestChannelManifestURLs_MatchReleaseWorkflowCDN(t *testing.T) {
	const path = "/kenaz-harness/manifest.json"
	if want := releaseWorkflowCDNBase(t, "stage") + path; prereleaseManifestURL != want {
		t.Errorf("prereleaseManifestURL = %q, want %q (release.yml stage CDN_BASE)", prereleaseManifestURL, want)
	}
	if want := releaseWorkflowCDNBase(t, "prod") + path; stableManifestURL != want {
		t.Errorf("stableManifestURL = %q, want %q (release.yml prod CDN_BASE)", stableManifestURL, want)
	}
	if got := channelManifestURL("prerelease"); got != prereleaseManifestURL {
		t.Errorf("channelManifestURL(prerelease) = %q", got)
	}
}
