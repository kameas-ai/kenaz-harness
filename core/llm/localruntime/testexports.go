// testexports.go — exported helpers for white-box testing across packages.
// These functions are intentionally not prefixed with "test" so they can be
// called from both *_test.go files in other packages and from integration
// tests without build-tag guards. They expose the mutable internals needed
// to override per-runtime port/URL in tests.
package localruntime

// DefaultPorts returns a copy of the default port map (KindX → port).
func DefaultPorts() map[RuntimeKind]int {
	m := make(map[RuntimeKind]int, len(defaultPorts))
	for k, v := range defaultPorts {
		m[k] = v
	}
	return m
}

// SetDefaultPort overrides the default port for a runtime kind.
// Use in tests to point detectors at an httptest.Server.
func SetDefaultPort(kind RuntimeKind, port int) {
	defaultPorts[kind] = port
}

// DefaultBaseURLs returns a copy of the default base-URL map.
func DefaultBaseURLs() map[RuntimeKind]string {
	m := make(map[RuntimeKind]string, len(defaultBaseURLs))
	for k, v := range defaultBaseURLs {
		m[k] = v
	}
	return m
}

// SetDefaultBaseURL overrides the default base URL for a runtime kind.
func SetDefaultBaseURL(kind RuntimeKind, url string) {
	defaultBaseURLs[kind] = url
}
