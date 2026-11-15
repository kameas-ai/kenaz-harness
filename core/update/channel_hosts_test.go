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
	// The arm is matched up to its own `;;`, so a missing or reshaped
	// stage CDN_BASE cannot borrow the next arm's (prod's) value.
	arm := regexp.MustCompile(`(?ms)^\s*` + regexp.QuoteMeta(target) + `\)\s*$(.*?)^\s*;;`).FindSubmatch(raw)
	if arm == nil {
		t.Fatalf("release.yml has no %s) case arm — the resolve-env shape changed; update this reader", target)
	}
	m := regexp.MustCompile(`CDN_BASE="([^"]+)"`).FindSubmatch(arm[1])
	if m == nil {
		t.Fatalf("release.yml's %s) arm sets no CDN_BASE=\"…\"", target)
	}
	return string(m[1])
}

// TestChannelManifestURLs_MatchReleaseWorkflowCDN pins both update-channel
// manifest URLs to the CDN hosts release.yml actually publishes to, so the
// two cannot drift again. prereleaseManifestURL pointed at
// stage-downloads.kameas.ai — NXDOMAIN — from #168 (2026-06-11) to
// 2026-10-07; a DNS failure is not errManifestNotFound, so the stable
// fallback never ran and every Prerelease-channel update check failed.
//
// Since the L09 release gate both channels live on the prod CDN: a prod
// build moves kenaz-harness/prerelease/manifest.json (release.yml's
// publish-s3 guard step) and promote.yml moves kenaz-harness/manifest.json.
func TestChannelManifestURLs_MatchReleaseWorkflowCDN(t *testing.T) {
	const path = "/kenaz-harness/manifest.json"
	const prereleasePath = "/kenaz-harness/prerelease/manifest.json"
	if want := releaseWorkflowCDNBase(t, "prod") + prereleasePath; prereleaseManifestURL != want {
		t.Errorf("prereleaseManifestURL = %q, want %q (release.yml prod CDN_BASE + prerelease pointer)", prereleaseManifestURL, want)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	if !regexp.MustCompile(`POINTER_KEY="kenaz-harness/prerelease/manifest\.json"`).Match(raw) {
		t.Errorf("release.yml no longer publishes prod builds to kenaz-harness/prerelease/manifest.json; the prerelease channel would read nothing")
	}
	if want := releaseWorkflowCDNBase(t, "prod") + path; stableManifestURL != want {
		t.Errorf("stableManifestURL = %q, want %q (release.yml prod CDN_BASE)", stableManifestURL, want)
	}
	if got := channelManifestURL("prerelease"); got != prereleaseManifestURL {
		t.Errorf("channelManifestURL(prerelease) = %q", got)
	}
}
