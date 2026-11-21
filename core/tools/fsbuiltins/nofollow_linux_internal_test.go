//go:build linux

package fsbuiltins

import "testing"

type noFollowMode struct {
	name string
	set  func(t *testing.T)
}

// noFollowModes: openat2 (skipped where the kernel lacks it) and the
// component walk.
func noFollowModes() []noFollowMode {
	return []noFollowMode{
		{"openat2", func(t *testing.T) {
			if openat2Off.Load() {
				t.Skip("openat2 unavailable on this kernel")
			}
		}},
		{"walk", func(t *testing.T) {
			was := openat2Off.Load()
			openat2Off.Store(true)
			t.Cleanup(func() { openat2Off.Store(was) })
		}},
	}
}

// mutateFastPath drops RESOLVE_NO_SYMLINKS from the openat2 path.
func mutateFastPath() (restore func()) {
	saved := noFollowResolve
	noFollowResolve = 0
	return func() { noFollowResolve = saved }
}
