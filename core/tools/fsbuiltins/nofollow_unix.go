//go:build !windows

package fsbuiltins

import (
	"errors"
	"os"
	"syscall"
)

// openNoFollow opens p for reading without following a leaf symlink
// (O_NOFOLLOW): a link at p is refused with ErrEditThroughLink instead of
// opening whatever it points to.
func openNoFollow(p string) (*os.File, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, ErrEditThroughLink
		}
		return nil, err
	}
	return f, nil
}
