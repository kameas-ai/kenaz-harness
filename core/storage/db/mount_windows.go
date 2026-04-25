//go:build windows

package db

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// osCheckMount implements MountChecker on Windows by calling
// GetDriveType on the volume root containing path. DRIVE_REMOTE is
// refused.
func osCheckMount(path string) (MountReport, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return MountReport{Path: path, Kind: MountKindUnknown}, err
	}
	vol := filepath.VolumeName(abs)
	if vol == "" {
		return MountReport{Path: abs, Kind: MountKindUnknown,
			Detail: "no volume name"}, nil
	}
	root := vol + `\`
	rootPtr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return MountReport{Path: abs, Kind: MountKindUnknown},
			fmt.Errorf("utf16: %w", err)
	}
	dtype := windows.GetDriveType(rootPtr)
	rep := MountReport{Path: abs, Detail: fmt.Sprintf("drive_type=%d", dtype)}
	switch dtype {
	case windows.DRIVE_REMOTE:
		rep.Kind = MountKindSMB // Windows network shares are SMB-style
	case windows.DRIVE_UNKNOWN, windows.DRIVE_NO_ROOT_DIR:
		rep.Kind = MountKindUnknown
	default:
		rep.Kind = MountKindLocal
	}
	return rep, nil
}

func init() {
	cloudSyncDenyListExtra = append(cloudSyncDenyListExtra, CloudSyncMatcher{
		Provider: "icloud",
		MatchPath: func(path string) (bool, string) {
			lower := strings.ToLower(path)
			if strings.Contains(lower, `\icloud drive\`) ||
				strings.Contains(lower, `\icloud\`) {
				return true, "matched icloud drive directory"
			}
			return false, ""
		},
	})
}
