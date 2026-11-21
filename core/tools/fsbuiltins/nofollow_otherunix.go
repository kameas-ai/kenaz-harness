//go:build unix && !linux && !darwin

package fsbuiltins

import "os"

// searchOnlyFlag: no portable search-only open; a --x component fails.
const searchOnlyFlag = 0

// openDirFast: other unixes always walk the components (walkOpenDir).
func openDirFast(string) (*os.File, bool, error) { return nil, false, nil }
