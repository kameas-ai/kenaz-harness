//go:build windows

package fsbuiltins

import (
	"testing"

	"golang.org/x/sys/windows"
)

// Every name-surrogate reparse point in a checked path is a link — mount
// points included, whatever their target — and other reparse points and
// plain entries are not.
func TestIsLinkReparse(t *testing.T) {
	const (
		tagCloud = 0x9000001A // IO_REPARSE_TAG_CLOUD (OneDrive placeholder)
		tagDedup = 0x80000013 // IO_REPARSE_TAG_DEDUP
	)
	rp := uint32(windows.FILE_ATTRIBUTE_REPARSE_POINT)
	cases := []struct {
		name       string
		attrs, tag uint32
		want       bool
	}{
		{"mount point (junction or volume)", rp, windows.IO_REPARSE_TAG_MOUNT_POINT, true},
		{"symlink", rp, windows.IO_REPARSE_TAG_SYMLINK, true},
		{"onedrive placeholder", rp, tagCloud, false},
		{"dedup", rp, tagDedup, false},
		{"plain directory", windows.FILE_ATTRIBUTE_DIRECTORY, 0, false},
		{"mount-point tag without the reparse attribute", windows.FILE_ATTRIBUTE_DIRECTORY, windows.IO_REPARSE_TAG_MOUNT_POINT, false},
	}
	for _, c := range cases {
		if got := isLinkReparse(c.attrs, c.tag); got != c.want {
			t.Errorf("%s: isLinkReparse = %v, want %v", c.name, got, c.want)
		}
	}
}
