package fleet

// useragent.go — legal (kameas-ai-11, 2026-10-16, via kenaz-fleet #289):
// every request this package sends to Fleet (and to its identity provider
// for token exchange / refresh) carries
//
//	User-Agent: kenaz-harness/<semver>
//
// instead of Go's default "Go-http-client/1.1". Fleet logs client_name and
// client_version from the first UA token next to each ML notice ack. The
// version is the build version main.go reports (main.Version, injected by
// the release ldflags), with any leading "v" dropped; an unset dev build
// is "dev" (kenaz-harness/dev).

import (
	"net/http"
	"strings"
	"sync/atomic"
)

// UserAgentProduct is the UA product token Fleet reads as client_name.
const UserAgentProduct = "kenaz-harness"

var clientVersion atomic.Value // string

// SetClientVersion records the app's build version for the User-Agent.
// main.go calls it once at boot with main.Version.
func SetClientVersion(v string) {
	clientVersion.Store(normalizeClientVersion(v))
}

// normalizeClientVersion trims space and a leading "v"/"V"; "" → "dev".
func normalizeClientVersion(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 1 && (v[0] == 'v' || v[0] == 'V') && v[1] >= '0' && v[1] <= '9' {
		v = v[1:]
	}
	if v == "" {
		return "dev"
	}
	return v
}

// UserAgent is "kenaz-harness/<semver>" ("kenaz-harness/dev" until
// SetClientVersion is called, or for a dev build).
func UserAgent() string {
	v, _ := clientVersion.Load().(string)
	if v == "" {
		v = "dev"
	}
	return UserAgentProduct + "/" + v
}

// setUserAgent stamps req with UserAgent(). Every Fleet request site in
// this package calls it (or goes through tokenRoundTripper, which does).
func setUserAgent(req *http.Request) {
	if req != nil {
		req.Header.Set("User-Agent", UserAgent())
	}
}
