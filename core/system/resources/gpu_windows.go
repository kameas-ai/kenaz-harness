//go:build windows

package resources

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// GPUVRAMBytes returns the total VRAM reported by the first NVIDIA GPU
// (via nvidia-smi) in bytes on Windows. Returns (0, false) when no GPU
// is available or nvidia-smi is not present.
func GPUVRAMBytes() (int64, bool) {
	return gpuVRAMBytesVia(runNvidiaSMI)
}

func gpuVRAMBytesVia(probe func() (string, error)) (int64, bool) {
	out, err := probe()
	if err != nil {
		return 0, false
	}
	return parseNvidiaSMIOutput(out)
}

func runNvidiaSMI() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=memory.total", "--format=csv,noheader,nounits")
	out, err := cmd.Output()
	return string(out), err
}

func parseNvidiaSMIOutput(out string) (int64, bool) {
	line := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
	mib, err := strconv.ParseInt(line, 10, 64)
	if err != nil || mib <= 0 {
		return 0, false
	}
	return mib * 1024 * 1024, true
}
