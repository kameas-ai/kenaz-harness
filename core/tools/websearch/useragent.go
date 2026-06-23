package websearch

import (
	"runtime/debug"
	"sync"
)

const fallbackUserAgent = "kenaz-harness/dev"

var (
	uaOnce sync.Once
	uaVal  string
)

// userAgent returns the User-Agent string the package sends on every
// outbound HTTP request. It reads the build-info module version when
// available and falls back to a stable static value (so tests and
// `go run` don't ship a blank UA).
func userAgent() string {
	uaOnce.Do(func() {
		info, ok := debug.ReadBuildInfo()
		if !ok || info == nil || info.Main.Version == "" || info.Main.Version == "(devel)" {
			uaVal = fallbackUserAgent
			return
		}
		uaVal = "kenaz-harness/" + info.Main.Version
	})
	return uaVal
}
