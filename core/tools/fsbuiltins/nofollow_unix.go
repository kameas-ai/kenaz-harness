//go:build unix

package fsbuiltins

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// openDirNoFollow opens the directory at the absolute path p without
// following a link in any component. A link met on the way is refused with
// ErrPathChanged. With mkdir, missing components are created (0755, as
// os.MkdirAll did) relative to the already-opened parent.
//
// Linux tries openat2(RESOLVE_NO_SYMLINKS) first (openDirFast); everywhere
// else, and on Linux before 5.6, the path is walked one component at a time
// from "/" with openat(O_NOFOLLOW|O_DIRECTORY).
func openDirNoFollow(p string, mkdir bool) (*os.File, error) {
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		return nil, &os.PathError{Op: "open", Path: p, Err: errors.New("path is not absolute")}
	}
	if f, ok, err := openDirFast(p); ok {
		return f, err
	}
	return walkOpenDir(p, mkdir)
}

// walkOpenDir is openDirNoFollow's component walk.
func walkOpenDir(p string, mkdir bool) (*os.File, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: "/", Err: err}
	}
	cur := "/"
	for _, comp := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if comp == "" {
			continue
		}
		cur = filepath.Join(cur, comp)
		next, err := openDirComp(fd, comp)
		if errors.Is(err, unix.ENOENT) && mkdir {
			if merr := unix.Mkdirat(fd, comp, 0o755); merr != nil && !errors.Is(merr, unix.EEXIST) {
				_ = unix.Close(fd)
				return nil, &os.PathError{Op: "mkdir", Path: cur, Err: merr}
			}
			next, err = openDirComp(fd, comp)
		}
		if err != nil {
			changed := isLinkAt(fd, comp)
			_ = unix.Close(fd)
			if changed {
				return nil, ErrPathChanged
			}
			return nil, &os.PathError{Op: "open", Path: cur, Err: err}
		}
		_ = unix.Close(fd)
		fd = next
	}
	return os.NewFile(uintptr(fd), p), nil
}

// noFollowDir is the flag set every directory component is opened with.
// It is a variable only so the mutation test can prove the tests notice
// its O_NOFOLLOW being dropped.
var noFollowDir = unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

// openDirComp opens one directory component below dirfd without following
// it. A component the process may search but not list (mode --x) is
// retried with the platform's search-only flag, which is enough to open
// names below it.
func openDirComp(dirfd int, name string) (int, error) {
	fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|noFollowDir, 0)
	if errors.Is(err, unix.EACCES) && searchOnlyFlag != 0 {
		if sfd, serr := unix.Openat(dirfd, name, searchOnlyFlag|noFollowDir, 0); serr == nil {
			return sfd, nil
		}
	}
	return fd, err
}

// isLinkAt reports whether name in dirfd is a symlink, without following it.
func isLinkAt(dirfd int, name string) bool {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false
	}
	return st.Mode&unix.S_IFMT == unix.S_IFLNK
}

// openInDir opens name in the opened directory dir without following a
// link at name (errLeafLink if it is one).
func openInDir(dir *os.File, name string, flag int, perm os.FileMode) (*os.File, error) {
	dfd := int(dir.Fd())
	fd, err := unix.Openat(dfd, name, flag|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(perm.Perm()))
	if err != nil {
		if isLinkAt(dfd, name) {
			return nil, errLeafLink
		}
		return nil, &os.PathError{Op: "open", Path: filepath.Join(dir.Name(), name), Err: err}
	}
	return os.NewFile(uintptr(fd), filepath.Join(dir.Name(), name)), nil
}

// openSubdir opens the subdirectory name of dir for listing, without
// following it: a link there is ErrPathChanged.
func openSubdir(dir *os.File, name string) (*os.File, error) {
	dfd := int(dir.Fd())
	fd, err := unix.Openat(dfd, name, unix.O_RDONLY|noFollowDir, 0)
	if err != nil {
		if isLinkAt(dfd, name) {
			return nil, ErrPathChanged
		}
		return nil, &os.PathError{Op: "open", Path: filepath.Join(dir.Name(), name), Err: err}
	}
	return os.NewFile(uintptr(fd), filepath.Join(dir.Name(), name)), nil
}

// renameInDir renames from to to, both inside the opened directory dir.
func renameInDir(dir *os.File, from, to string) error {
	dfd := int(dir.Fd())
	if err := unix.Renameat(dfd, from, dfd, to); err != nil {
		return &os.LinkError{Op: "rename", Old: filepath.Join(dir.Name(), from), New: filepath.Join(dir.Name(), to), Err: err}
	}
	return nil
}

// removeInDir unlinks the file name inside the opened directory dir.
func removeInDir(dir *os.File, name string) error {
	return unix.Unlinkat(int(dir.Fd()), name, 0)
}

// lsizeInDir is the size of name in dir, not following a link at name.
func lsizeInDir(dir *os.File, name string) (int64, bool) {
	var st unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return 0, false
	}
	return st.Size, true
}
