package localruntime

import (
	"context"
	"os/exec"
	"sync"
	"time"
)

// DetectAll probes all supported local runtimes and returns a RuntimeInfo
// for each one. The slice always has exactly len(AllKinds) entries (one per
// known kind) so callers can iterate predictably.
//
// When Enabled() is false (HARNESS_LOCAL_RUNTIMES=0), DetectAll returns an
// empty slice immediately without any network or filesystem access.
//
// Results are safe to call from multiple goroutines — internal detection is
// parallelised. Context cancellation is respected; a cancelled ctx causes
// all in-flight port checks to return false.
func DetectAll(ctx context.Context) []RuntimeInfo {
	if !Enabled() {
		return []RuntimeInfo{}
	}

	var wg sync.WaitGroup
	results := make([]RuntimeInfo, len(AllKinds))

	for i, kind := range AllKinds {
		wg.Add(1)
		go func(idx int, k RuntimeKind) {
			defer wg.Done()
			results[idx] = detectOne(ctx, k)
		}(i, kind)
	}
	wg.Wait()
	return results
}

// detectOne runs the per-runtime detection for a single kind.
func detectOne(ctx context.Context, kind RuntimeKind) RuntimeInfo {
	now := time.Now()
	info := RuntimeInfo{
		Kind:           kind,
		Name:           defaultNames[kind],
		DefaultBaseURL: defaultBaseURLs[kind],
		Port:           defaultPorts[kind],
		DetectedAt:     now,
	}

	// Binary detection
	binName := defaultBinaries[kind]
	if p, err := exec.LookPath(binName); err == nil {
		info.BinaryPath = p
		info.Installed = true
	}

	// Port listening check
	info.Running = portListening(ctx, info.Port)

	// Set detection method
	switch {
	case info.Installed && info.Running:
		info.Method = DetectionBoth
	case info.Installed:
		info.Method = DetectionBinary
	case info.Running:
		info.Method = DetectionPort
	}

	return info
}
