//go:build linux

package fsbuiltins

import (
	"errors"
	"os"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// searchOnlyFlag opens a directory the process may search but not list.
const searchOnlyFlag = unix.O_PATH

// openat2Off is set once openat2 turns out to be unavailable (ENOSYS on
// kernels before 5.6, or a seccomp filter that refuses it), so later
// calls go straight to the component walk. Tests also set it to exercise
// the walk on a kernel that has openat2.
var openat2Off atomic.Bool

// noFollowResolve is the openat2 resolve flag; a variable only so the
// mutation test can prove the tests notice it being dropped.
var noFollowResolve uint64 = unix.RESOLVE_NO_SYMLINKS

// openDirFast opens p with openat2(RESOLVE_NO_SYMLINKS) from "/": the
// kernel refuses a link in any component (ELOOP). ok is false when the
// caller should use the component walk instead: openat2 is unavailable,
// or it failed for a reason the walk reports better or can fix (a missing
// component it may create, a --x component it may open search-only).
func openDirFast(p string) (f *os.File, ok bool, err error) {
	if openat2Off.Load() {
		return nil, false, nil
	}
	fd, err := unix.Openat2(unix.AT_FDCWD, p, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: noFollowResolve,
	})
	switch {
	case err == nil:
		return os.NewFile(uintptr(fd), p), true, nil
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EPERM):
		openat2Off.Store(true)
		return nil, false, nil
	case errors.Is(err, unix.ELOOP):
		return nil, true, ErrPathChanged
	default:
		return nil, false, nil
	}
}
