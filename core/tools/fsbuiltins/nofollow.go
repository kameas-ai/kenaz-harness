package fsbuiltins

// No-follow I/O on the location the gate checked.
//
// The gate (corefs.Gate.EvaluatePath) resolves every link in the requested
// path at check time and judges the resolved location; the tools then do
// their I/O on that resolved path. Between the two, an agent with a shell
// can swap an intermediate directory of the resolved path for a symlink
// (replace proj with a link to <DataDir>/policy). A path-based open or
// rename would follow it and land in the target.
//
// So every builtin opens the checked path without following a link in ANY
// component (openDirNoFollow, platform files), then works relative to the
// opened directory handle: reads open the leaf in it, writes create the
// temp file in it and rename inside it. The checked path has no links in
// it (it is the resolved form), so a link met on the way is a swap made
// after the check: the I/O is refused with ErrPathChanged, never followed.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// pathChangedError is ErrPathChanged's type; a type rather than
// errors.New so the user-facing sentence can keep its capital and period.
type pathChangedError struct{}

func (pathChangedError) Error() string {
	return "The path changed while it was being checked; try again."
}

// ErrPathChanged is the refusal when a component of the checked path turns
// out to be a link at I/O time: it was swapped after the gate's check.
var ErrPathChanged error = pathChangedError{}

// errLeafLink is the platform primitives' signal that the final component
// opened with openInDir is itself a link. Callers map it: edit_file to
// ErrEditThroughLink (a leaf link is a legitimate, refused target for a
// write), the read tools to ErrPathChanged (their checked path had every
// link resolved, so a leaf link appeared after the check).
var errLeafLink = errors.New("leaf is a link")

// openNoFollow opens p for reading without following a link in any
// component. A leaf link is refused with ErrEditThroughLink; a link in an
// intermediate component with ErrPathChanged. p may be a directory.
func openNoFollow(p string) (*os.File, error) {
	p = filepath.Clean(p)
	parent := filepath.Dir(p)
	if parent == p {
		return openDirNoFollow(p, false)
	}
	dir, err := openDirNoFollow(parent, false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	f, err := openInDir(dir, filepath.Base(p), os.O_RDONLY, 0)
	if errors.Is(err, errLeafLink) {
		return nil, ErrEditThroughLink
	}
	return f, err
}

// openCheckedForRead is openNoFollow for the read tools: their checked
// path is fully resolved, so a link anywhere, the leaf included, is a swap.
func openCheckedForRead(p string) (*os.File, error) {
	f, err := openNoFollow(p)
	if errors.Is(err, ErrEditThroughLink) {
		return nil, ErrPathChanged
	}
	return f, err
}

// readDirSorted reads every entry of an opened directory, sorted by name
// (filepath.WalkDir's and os.ReadDir's order, which the tools relied on).
func readDirSorted(dir *os.File) ([]fs.DirEntry, error) {
	ents, err := dir.ReadDir(-1)
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	return ents, err
}

// walkNoFollow walks the tree below the opened directory root (rootPath is
// its path, for reporting), calling fn for every descendant — not for the
// root itself — in lexical order. Subdirectories are opened relative to
// their parent's handle without following links, so a directory swapped
// for a link mid-walk is refused with ErrPathChanged rather than entered;
// in-tree links are reported to fn as links and never descended.
// filepath.SkipDir and filepath.SkipAll behave as in filepath.WalkDir. A
// directory that cannot be read or opened is skipped, as the tools' old
// WalkDir callbacks did. parent is the handle of the entry's directory;
// fn may use it with openInDir / lsizeInDir until it returns.
func walkNoFollow(root *os.File, rootPath string, fn func(path string, d fs.DirEntry, parent *os.File) error) error {
	err := walkDirHandle(root, rootPath, fn)
	if err == filepath.SkipAll || err == filepath.SkipDir {
		return nil
	}
	return err
}

func walkDirHandle(dir *os.File, dirPath string, fn func(string, fs.DirEntry, *os.File) error) error {
	ents, err := readDirSorted(dir)
	if err != nil && len(ents) == 0 {
		return nil // unreadable directory: skipped
	}
	for _, d := range ents {
		p := filepath.Join(dirPath, d.Name())
		if err := fn(p, d, dir); err != nil {
			if err == filepath.SkipDir {
				if d.IsDir() {
					continue
				}
				return nil // SkipDir on a file skips the rest of its directory
			}
			return err
		}
		if !d.IsDir() {
			continue
		}
		sub, err := openSubdir(dir, d.Name())
		if err != nil {
			if errors.Is(err, ErrPathChanged) {
				return err
			}
			continue // unreadable subdirectory: skipped
		}
		err = walkDirHandle(sub, p, fn)
		_ = sub.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// atomicWrite writes data to path using a tmp-file-then-rename pattern,
// creating missing parent directories. The parent directory is opened
// without following a link in any component, the temp file is created in
// that handle (O_EXCL, no-follow), synced, and renamed inside the same
// handle, so both land in the directory that was opened — a parent swapped
// for a link after the gate's check is refused (ErrPathChanged), never
// followed. A link at the leaf is replaced by the rename, never written
// through. The rename is atomic on POSIX (same directory); Windows has
// weaker rename semantics but is still better than truncate+write.
func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	path = filepath.Clean(path)
	dir, err := openDirNoFollow(filepath.Dir(path), true)
	if err != nil {
		if errors.Is(err, ErrPathChanged) {
			return err
		}
		return fmt.Errorf("mkdir: %w", err)
	}
	defer dir.Close()
	base := filepath.Base(path)

	var tmp *os.File
	var tmpName string
	for i := 0; i < 16; i++ {
		tmpName = ".fsbuiltins-write-" + randSuffix()
		tmp, err = openInDir(dir, tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if !errors.Is(err, fs.ErrExist) {
			break
		}
	}
	if err != nil {
		if errors.Is(err, ErrPathChanged) {
			return err
		}
		return fmt.Errorf("create tmp: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = removeInDir(dir, tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := renameInDir(dir, tmpName, base); err != nil {
		if errors.Is(err, ErrPathChanged) {
			return err
		}
		return fmt.Errorf("rename: %w", err)
	}
	committed = true
	return nil
}

func randSuffix() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
