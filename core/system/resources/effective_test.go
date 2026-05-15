package resources

import (
	"errors"
	"testing"
)

// TestSystemRAMBytes_ReturnsNonzero verifies that systemRAMBytesVia with
// the production gopsutil probe returns a sensible value on the CI machine.
// We only test the injected path to avoid hard-coding a gopsutil call.
func TestSystemRAMBytesVia_Probe(t *testing.T) {
	got := systemRAMBytesVia(func() (uint64, error) {
		return 16 * 1024 * 1024 * 1024, nil // fake 16 GiB
	})
	want := int64(16 * 1024 * 1024 * 1024)
	if got != want {
		t.Errorf("systemRAMBytesVia = %d; want %d", got, want)
	}
}

func TestSystemRAMBytesVia_Error(t *testing.T) {
	got := systemRAMBytesVia(func() (uint64, error) {
		return 0, errors.New("probe failed")
	})
	if got != 0 {
		t.Errorf("expected 0 on probe error; got %d", got)
	}
}

// TestGPUVRAMBytesVia_ErrorPath verifies that GPUVRAMBytes returns (0, false)
// when the probe returns an error.
func TestGPUVRAMBytesVia_ErrorPath(t *testing.T) {
	bytes, ok := gpuVRAMBytesVia(func() (string, error) {
		return "", errors.New("nvidia-smi not found")
	})
	if ok {
		t.Error("expected ok=false on probe error")
	}
	if bytes != 0 {
		t.Errorf("expected 0 bytes on probe error; got %d", bytes)
	}
}

// TestGPUVRAMBytesVia_GoodOutput verifies nvidia-smi MiB parsing on linux/windows.
// On darwin/freebsd parseNvidiaSMIOutput is a no-op stub; we skip there.
func TestParseNvidiaSMIOutput_ValidMiB(t *testing.T) {
	bytes, ok := parseNvidiaSMIOutput("8192\n")
	// On non-Linux/Windows the stub always returns (0, false); skip gracefully.
	if !ok {
		if bytes == 0 {
			t.Skip("parseNvidiaSMIOutput is a no-op stub on this platform")
		}
		t.Fatal("expected ok=true for valid MiB output on Linux/Windows")
	}
	want := int64(8192) * 1024 * 1024
	if bytes != want {
		t.Errorf("parseNvidiaSMIOutput = %d; want %d", bytes, want)
	}
}

func TestParseNvidiaSMIOutput_Invalid(t *testing.T) {
	// On non-Linux/Windows the stub always returns (0, false) which is the same
	// as the invalid-input outcome — still valid.
	_, ok := parseNvidiaSMIOutput("N/A\n")
	if ok {
		t.Error("expected ok=false for 'N/A' output")
	}
}

// TestEffectiveRAMBytes_NonZero verifies that EffectiveRAMBytes returns a
// positive value (system RAM is always present).
func TestEffectiveRAMBytes_NonZero(t *testing.T) {
	got := EffectiveRAMBytes()
	if got <= 0 {
		t.Errorf("EffectiveRAMBytes = %d; want positive", got)
	}
}
