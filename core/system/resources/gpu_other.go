//go:build !linux && !windows

package resources

// GPUVRAMBytes returns (0, false) on platforms where GPU detection is not
// implemented (darwin, freebsd, etc.).
func GPUVRAMBytes() (int64, bool) {
	return 0, false
}

// gpuVRAMBytesVia is the injectable variant used in cross-platform tests.
func gpuVRAMBytesVia(probe func() (string, error)) (int64, bool) {
	_, _ = probe()
	return 0, false
}

// parseNvidiaSMIOutput is a stub on non-Linux/Windows platforms.
func parseNvidiaSMIOutput(_ string) (int64, bool) { return 0, false }
