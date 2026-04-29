package stdio

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	fakeOnce sync.Once
	fakePath string
	fakeErr  error
)

// buildFakeServer compiles testdata/fake-mcp-server/main.go into a
// binary stored under the test cache and returns the absolute
// path. The compile happens once per `go test` run; subsequent
// callers return the cached path.
func buildFakeServer(t *testing.T) string {
	t.Helper()
	fakeOnce.Do(func() {
		fakePath, fakeErr = doBuildFakeServer()
	})
	if fakeErr != nil {
		t.Fatalf("buildFakeServer: %v", fakeErr)
	}
	return fakePath
}

func doBuildFakeServer() (string, error) {
	// Locate the source by anchoring on this file's location —
	// works regardless of the test's working directory.
	_, here, _, _ := runtime.Caller(0)
	srcDir := filepath.Join(filepath.Dir(here), "testdata", "fake-mcp-server")
	tmpDir, err := os.MkdirTemp("", "kaneaz-fake-mcp-")
	if err != nil {
		return "", err
	}
	exe := filepath.Join(tmpDir, "fake-mcp-server")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, ".")
	cmd.Dir = srcDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return exe, nil
}
