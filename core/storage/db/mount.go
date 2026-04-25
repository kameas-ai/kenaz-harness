package db

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// MountKind classifies a filesystem path. Local mounts are safe for
// SQLite WAL; everything else is refused unless the operator explicitly
// overrides via Config.AllowNonLocalMount.
type MountKind string

const (
	MountKindLocal     MountKind = "local"
	MountKindNFS       MountKind = "nfs"
	MountKindCIFS      MountKind = "cifs"
	MountKindSMB       MountKind = "smb"
	MountKindAFP       MountKind = "afp"
	MountKindSSHFS     MountKind = "fuse.sshfs"
	MountKindCloudSync MountKind = "cloud-sync"
	MountKindUnknown   MountKind = "unknown"
)

// MountReport is the outcome of a Check call.
type MountReport struct {
	Path     string
	Kind     MountKind
	Provider string // for cloud-sync: "icloud" | "dropbox" | "onedrive" | "google-drive" | "insync"
	Detail   string
}

// IsLocal reports whether the report is a known-local mount.
func (r MountReport) IsLocal() bool { return r.Kind == MountKindLocal }

// MountChecker is the OS-specific detector. Build-tag-gated
// implementations live in mount_<goos>.go.
type MountChecker interface {
	Check(path string) (MountReport, error)
}

// ErrUnsupportedOS is returned by checkers on platforms without a real
// implementation. Tests run on the target OSes; the fallback exists only
// to keep `go build` working everywhere.
var ErrUnsupportedOS = errors.New("storage/db: mount detection not implemented for this OS")

// CheckMount routes to the OS-specific detector. After the OS-level
// classification, it overlays the cloud-sync deny-list — a path can be
// on a local FS yet inside a cloud-sync provider's root, in which case
// the WAL-on-cloud-sync corruption hazard still applies.
func CheckMount(path string) (MountReport, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return MountReport{Path: path}, fmt.Errorf("mount: abs %q: %w", path, err)
	}
	rep := MountReport{Path: abs, Kind: MountKindLocal}

	// OS-specific detection (defined in mount_<goos>.go).
	osRep, err := osCheckMount(abs)
	if err == nil {
		rep = osRep
	} else if !errors.Is(err, ErrUnsupportedOS) {
		return rep, err
	}

	// If the OS classification is local, layer the cloud-sync heuristic.
	if rep.Kind == MountKindLocal {
		if hit, provider, detail := matchCloudSyncRoot(abs); hit {
			rep.Kind = MountKindCloudSync
			rep.Provider = provider
			rep.Detail = detail
		}
	}

	return rep, nil
}

// CloudSyncMatcher describes a deny-list entry. Each entry specifies a
// substring or path-prefix that, if matched, classifies the path as
// living inside a sync provider's root.
type CloudSyncMatcher struct {
	Provider string
	// MatchPath returns true if path lives inside the provider's
	// sync root.
	MatchPath func(path string) (bool, string)
}

// CloudSyncDenyList is the cross-platform set of path heuristics. Each
// OS-specific file (mount_darwin.go, mount_linux.go, mount_windows.go)
// supplies additional entries via cloudSyncDenyListExtra at init time.
//
// To add a new sync provider, append an entry here (or in the OS-specific
// extra slice) with a clear MatchPath predicate. Default policy: deny
// everything matched.
var CloudSyncDenyList = []CloudSyncMatcher{
	{
		Provider: "dropbox",
		MatchPath: func(path string) (bool, string) {
			candidates := []string{
				"/Dropbox/",
				"/Dropbox (Personal)/",
				"/Dropbox (Business)/",
			}
			for _, c := range candidates {
				if strings.Contains(path, c) {
					return true, "matched substring " + c
				}
			}
			return false, ""
		},
	},
	{
		Provider: "google-drive",
		MatchPath: func(path string) (bool, string) {
			candidates := []string{
				"/Google Drive/",
				"/GoogleDrive/",
				"/My Drive/",
			}
			for _, c := range candidates {
				if strings.Contains(path, c) {
					return true, "matched substring " + c
				}
			}
			return false, ""
		},
	},
	{
		Provider: "onedrive",
		MatchPath: func(path string) (bool, string) {
			candidates := []string{
				"/OneDrive/",
				"/OneDrive - ",
			}
			for _, c := range candidates {
				if strings.Contains(path, c) {
					return true, "matched substring " + c
				}
			}
			return false, ""
		},
	},
	{
		Provider: "insync",
		MatchPath: func(path string) (bool, string) {
			if strings.Contains(path, "/Insync/") {
				return true, "matched substring /Insync/"
			}
			return false, ""
		},
	},
}

// cloudSyncDenyListExtra is appended to by OS-specific files (e.g. macOS
// adds iCloud's Mobile Documents path). Initialized via init() in those
// files.
var cloudSyncDenyListExtra []CloudSyncMatcher

// matchCloudSyncRoot walks both deny-lists and returns the first hit.
func matchCloudSyncRoot(path string) (bool, string, string) {
	for _, m := range CloudSyncDenyList {
		if hit, detail := m.MatchPath(path); hit {
			return true, m.Provider, detail
		}
	}
	for _, m := range cloudSyncDenyListExtra {
		if hit, detail := m.MatchPath(path); hit {
			return true, m.Provider, detail
		}
	}
	return false, "", ""
}
