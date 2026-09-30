//go:build !darwin

package mlsidecar

// clearQuarantine is a no-op on every non-macOS platform: quarantine
// xattrs are a macOS/Gatekeeper-specific concept (design F3/§3.7 R3
// explicitly scopes this to macOS). install.go still calls this
// unconditionally after verification so the call site itself is
// platform-agnostic; only the implementation is guarded.
func clearQuarantine(root string) error {
	_ = root
	return nil
}
