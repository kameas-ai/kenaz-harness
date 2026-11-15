//go:build darwin

package fsbuiltins

import (
	"os"

	"golang.org/x/sys/unix"
)

// searchOnlyFlag opens a directory the process may search but not list.
const searchOnlyFlag = unix.O_SEARCH

// openDirFast: darwin always walks the components (walkOpenDir).
func openDirFast(string) (*os.File, bool, error) { return nil, false, nil }
