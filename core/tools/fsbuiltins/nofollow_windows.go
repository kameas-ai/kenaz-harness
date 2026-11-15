//go:build windows

package fsbuiltins

// Windows has no openat and no O_NOFOLLOW, so the no-follow I/O is done by
// path and verified against the handle: after a directory or file is
// opened, every component of its path is Lstat'ed and must not be a link
// (a symlink, junction or other name-surrogate reparse point), and the
// opened handle must be the same file (os.SameFile, which compares
// GetFileInformationByHandle's volume serial and file index) as an Lstat
// of the path. A redirect through a swapped component makes the opened
// handle a different file from the checked path, or leaves a link in a
// component, and is refused with ErrPathChanged.
//
// Residual risk (documented in the PR): the check follows the open, so a
// swap made and reverted again between the open and the check is not
// seen, and a write's temp-file create and rename are by path after the
// directory handle was verified, so a swap inside that window can still
// land them in the target. Each needs the agent to win a race against
// syscalls issued back to back; the unix builds have no such window.

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// openDirNoFollow opens the directory at p and verifies that no component
// of p is a link and that the handle is the directory at p.
func openDirNoFollow(p string, mkdir bool) (*os.File, error) {
	p = filepath.Clean(p)
	if mkdir {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	if err := verifyHandle(p, f, true); err != nil {
		_ = f.Close()
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || !fi.IsDir() {
		_ = f.Close()
		if err == nil {
			err = &os.PathError{Op: "open", Path: p, Err: windows.ERROR_DIRECTORY}
		}
		return nil, err
	}
	return f, nil
}

// verifyHandle checks every component of p (from the volume root) is not
// a link, and that f is the file at p. withLeaf includes p itself in the
// component check.
func verifyHandle(p string, f *os.File, withLeaf bool) error {
	cur := p
	if !withLeaf {
		cur = filepath.Dir(p)
	}
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			break // the volume root
		}
		li, err := os.Lstat(cur)
		if err != nil {
			return err
		}
		if isLinkInfo(cur, li) {
			return ErrPathChanged
		}
		cur = parent
	}
	li, err := os.Lstat(p)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(li, fi) {
		return ErrPathChanged
	}
	return nil
}

// isLinkInfo reports whether p (Lstat'ed as li) is a link: a symlink, or
// a reparse point whose tag is a name surrogate (junctions, mount points).
// Other reparse points (OneDrive placeholders, dedup) are the file itself.
func isLinkInfo(p string, li os.FileInfo) bool {
	if li.Mode()&os.ModeSymlink != 0 {
		return true
	}
	if li.Mode()&os.ModeIrregular == 0 {
		return false
	}
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return true
	}
	var fd windows.Win32finddata
	h, err := windows.FindFirstFile(name, &fd)
	if err != nil {
		return true // cannot tell: fail closed
	}
	_ = windows.FindClose(h)
	if fd.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return false
	}
	const nameSurrogate = 0x20000000 // IsReparseTagNameSurrogate
	return fd.Reserved0&nameSurrogate != 0
}

// openInDir opens name in the verified directory dir. A link at name is
// errLeafLink; a redirect of dir is ErrPathChanged.
func openInDir(dir *os.File, name string, flag int, perm os.FileMode) (*os.File, error) {
	full := filepath.Join(dir.Name(), name)
	creating := flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0
	if !creating {
		li, err := os.Lstat(full)
		if err != nil {
			return nil, err
		}
		if isLinkInfo(full, li) {
			return nil, errLeafLink
		}
	}
	f, err := os.OpenFile(full, flag, perm)
	if err != nil {
		return nil, err
	}
	if err := verifyHandle(full, f, true); err != nil {
		_ = f.Close()
		if creating {
			_ = os.Remove(full)
		}
		if errors.Is(err, ErrPathChanged) {
			return nil, ErrPathChanged
		}
		return nil, err
	}
	if err := verifyHandle(dir.Name(), dir, true); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// openSubdir opens the subdirectory name of dir for listing; a link there
// is ErrPathChanged.
func openSubdir(dir *os.File, name string) (*os.File, error) {
	full := filepath.Join(dir.Name(), name)
	li, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	if isLinkInfo(full, li) {
		return nil, ErrPathChanged
	}
	return openDirNoFollow(full, false)
}

// renameInDir renames from to to inside dir, after re-verifying dir.
func renameInDir(dir *os.File, from, to string) error {
	if err := verifyHandle(dir.Name(), dir, true); err != nil {
		return err
	}
	return os.Rename(filepath.Join(dir.Name(), from), filepath.Join(dir.Name(), to))
}

// removeInDir removes the file name inside dir.
func removeInDir(dir *os.File, name string) error {
	return os.Remove(filepath.Join(dir.Name(), name))
}

// lsizeInDir is the size of name in dir, not following a link at name.
func lsizeInDir(dir *os.File, name string) (int64, bool) {
	li, err := os.Lstat(filepath.Join(dir.Name(), name))
	if err != nil {
		return 0, false
	}
	return li.Size(), true
}
