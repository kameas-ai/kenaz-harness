//go:build !darwin && !linux && !windows

package db

// osCheckMount is a fallback for unsupported platforms. The path is
// reported as MountKindUnknown and ErrUnsupportedOS is returned so the
// caller can decide whether to refuse or proceed.
func osCheckMount(path string) (MountReport, error) {
	return MountReport{Path: path, Kind: MountKindUnknown}, ErrUnsupportedOS
}
