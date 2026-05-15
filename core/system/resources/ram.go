// Package resources provides system resource probing utilities for the
// local-model-runtimes subsystem.
//
// (local-model-runtimes-01KQ8VMZ WP02)
package resources

import "github.com/shirou/gopsutil/v3/mem"

// ramProbe is the injectable interface for RAM detection.
// The default production probe uses gopsutil; tests substitute a fake.
type ramProbe func() (uint64, error)

var defaultRAMProbe ramProbe = func() (uint64, error) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return 0, err
	}
	return v.Total, nil
}

// SystemRAMBytes returns the total installed RAM in bytes.
// Returns 0 on error (non-fatal; callers treat 0 as "unknown").
func SystemRAMBytes() int64 {
	return systemRAMBytesVia(defaultRAMProbe)
}

func systemRAMBytesVia(probe ramProbe) int64 {
	total, err := probe()
	if err != nil {
		return 0
	}
	return int64(total)
}
