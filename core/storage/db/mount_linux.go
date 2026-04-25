//go:build linux

package db

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// osCheckMount implements MountChecker on Linux by parsing
// /proc/self/mountinfo, finding the longest mount-point that is an
// ancestor of the absolute path, and classifying its filesystem.
func osCheckMount(path string) (MountReport, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return MountReport{Path: path, Kind: MountKindUnknown}, err
	}
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return MountReport{Path: abs, Kind: MountKindUnknown},
			fmt.Errorf("open mountinfo: %w", err)
	}
	defer f.Close()

	type entry struct {
		mountPoint string
		fstype     string
	}
	var best entry
	bestLen := -1
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		// Format:
		//   36 35 98:0 /mnt1 /mnt parent_opts - <fstype> <source> <super opts>
		// We want fields 5 (mount point) and the field after "-" (fstype).
		fields := strings.Fields(line)
		dashIdx := -1
		for i, f := range fields {
			if f == "-" {
				dashIdx = i
				break
			}
		}
		if dashIdx < 0 || dashIdx+1 >= len(fields) || len(fields) < 5 {
			continue
		}
		mp := fields[4]
		fstype := fields[dashIdx+1]
		if isAncestorPath(mp, abs) && len(mp) > bestLen {
			best = entry{mountPoint: mp, fstype: fstype}
			bestLen = len(mp)
		}
	}
	if err := scanner.Err(); err != nil {
		return MountReport{Path: abs, Kind: MountKindUnknown}, fmt.Errorf("scan mountinfo: %w", err)
	}
	rep := MountReport{Path: abs, Detail: "fstype=" + best.fstype}
	switch {
	case best.fstype == "":
		rep.Kind = MountKindUnknown
	case strings.HasPrefix(best.fstype, "nfs"):
		rep.Kind = MountKindNFS
	case best.fstype == "cifs":
		rep.Kind = MountKindCIFS
	case strings.HasPrefix(best.fstype, "smb"):
		rep.Kind = MountKindSMB
	case best.fstype == "fuse.sshfs":
		rep.Kind = MountKindSSHFS
	default:
		rep.Kind = MountKindLocal
	}
	return rep, nil
}

func isAncestorPath(parent, child string) bool {
	if parent == child {
		return true
	}
	if !strings.HasSuffix(parent, "/") {
		parent = parent + "/"
	}
	return strings.HasPrefix(child, parent) || child+"/" == parent
}

func init() {
	cloudSyncDenyListExtra = append(cloudSyncDenyListExtra, CloudSyncMatcher{
		Provider: "icloud",
		MatchPath: func(path string) (bool, string) {
			if strings.Contains(path, "/iCloudDrive/") {
				return true, "matched /iCloudDrive/"
			}
			return false, ""
		},
	})
}
