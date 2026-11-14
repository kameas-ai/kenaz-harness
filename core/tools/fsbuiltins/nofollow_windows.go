//go:build windows

package fsbuiltins

import "os"

// openNoFollow opens p for reading and refuses a leaf link (a symlink or
// other reparse point): Windows has no O_NOFOLLOW, so the path is checked
// with Lstat and the opened handle must be that same file.
func openNoFollow(p string) (*os.File, error) {
	li, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if li.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return nil, ErrEditThroughLink
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !os.SameFile(li, fi) {
		_ = f.Close()
		return nil, ErrEditThroughLink
	}
	return f, nil
}
