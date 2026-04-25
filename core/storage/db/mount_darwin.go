//go:build darwin

package db

import (
	"fmt"
	"strings"
	"syscall"
)

// osCheckMount implements MountChecker on macOS via statfs(2). The
// f_flags field carries MNT_LOCAL when the mount lives on directly
// attached storage (APFS, HFS+). Anything without MNT_LOCAL — AFP, NFS,
// SMB — is non-local and refused.
//
// The f_fstypename byte slice gives us a more precise classification
// for the audit event.
func osCheckMount(path string) (MountReport, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return MountReport{Path: path, Kind: MountKindUnknown},
			fmt.Errorf("statfs %q: %w", path, err)
	}
	fstype := cstr(st.Fstypename[:])
	rep := MountReport{Path: path, Detail: "fstype=" + fstype}

	// MNT_LOCAL is 0x00001000 on Darwin per <sys/mount.h>.
	const mntLocal = 0x00001000
	if st.Flags&mntLocal == 0 {
		// non-local mount — classify by fstype.
		switch strings.ToLower(fstype) {
		case "nfs":
			rep.Kind = MountKindNFS
		case "cifs":
			rep.Kind = MountKindCIFS
		case "smbfs", "smb":
			rep.Kind = MountKindSMB
		case "afpfs", "afp":
			rep.Kind = MountKindAFP
		default:
			rep.Kind = MountKindUnknown
		}
		return rep, nil
	}
	rep.Kind = MountKindLocal
	return rep, nil
}

func init() {
	cloudSyncDenyListExtra = append(cloudSyncDenyListExtra, CloudSyncMatcher{
		Provider: "icloud",
		MatchPath: func(path string) (bool, string) {
			// macOS iCloud Drive surfaces as
			// ~/Library/Mobile Documents/com~apple~CloudDocs/
			if strings.Contains(path, "/Library/Mobile Documents/") {
				return true, "matched /Library/Mobile Documents/"
			}
			if strings.Contains(path, "/Library/CloudStorage/") {
				return true, "matched /Library/CloudStorage/"
			}
			return false, ""
		},
	})
}

// cstr converts a NUL-terminated byte slice into a Go string.
func cstr(b []int8) string {
	bs := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		bs = append(bs, byte(c))
	}
	return string(bs)
}
