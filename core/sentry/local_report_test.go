package sentry

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateLocalReport_SecretRedaction is the P0 gate for the local-report
// path: any planted secret must NOT appear in the output file bytes.
//
// (sentry-error-monitoring-01KX5R8G WP06)
func TestGenerateLocalReport_SecretRedaction(t *testing.T) {
	t.Parallel()

	// Secret classes to plant (subset matching integration_test.go patterns).
	secrets := []struct {
		name  string
		value string
	}{
		{"sk-ant key", "sk-ant-api01-secret-planted-test-key"},
		{"sk-proj key", "sk-proj-planted-test-secret-value"},
		{"generic sk- key", "sk-TESTPLANTEDKEY1234567890abcdef"},
		{"bearer token", "Bearer eyTestPlantedToken_1234abcd"},
		{"bare JWT", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0In0.plantedSignature"},
		{"AWS key ID", "AKIAIOSFODNN7EXAMPLETEST"},
		{"email", "test-planted@example.com"},
	}

	// Plant secrets into breadcrumbs (the global ring buffer).
	for _, s := range secrets {
		AddBreadcrumb(Breadcrumb{
			Level:   "info",
			Message: "test breadcrumb with planted secret: " + s.value,
		})
	}

	tmpDir := t.TempDir()

	path, n, err := GenerateLocalReport(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("GenerateLocalReport returned error: %v", err)
	}
	if n == 0 {
		t.Fatalf("GenerateLocalReport returned zero byte count")
	}

	// Read the generated report.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error: %v", path, err)
	}

	// Verify the file is valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("report is not valid JSON: %v", err)
	}

	// Assert no secret appears verbatim in the raw bytes.
	for _, s := range secrets {
		if bytes.Contains(data, []byte(s.value)) {
			t.Errorf("secret %q (%s) found verbatim in local crash report at %q", s.value, s.name, path)
		}
	}
}

// TestGenerateLocalReport_Structure verifies required top-level fields.
func TestGenerateLocalReport_Structure(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path, _, err := GenerateLocalReport(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("GenerateLocalReport error: %v", err)
	}

	data, _ := os.ReadFile(path)
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}

	required := []string{"generated_at", "harness_version", "os", "go_version", "arch", "breadcrumbs", "last_five"}
	for _, k := range required {
		if _, ok := parsed[k]; !ok {
			t.Errorf("report missing required field %q", k)
		}
	}
}

// TestGenerateLocalReport_WritesIntoSubDir verifies the file lands in
// <dataDir>/crash-reports/.
func TestGenerateLocalReport_WritesIntoSubDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path, _, err := GenerateLocalReport(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("GenerateLocalReport error: %v", err)
	}

	expectedPrefix := filepath.Join(tmpDir, reportSubDir)
	if !strings.HasPrefix(path, expectedPrefix) {
		t.Errorf("report path %q does not start with %q", path, expectedPrefix)
	}

	// File must end with .json.
	if !strings.HasSuffix(path, ".json") {
		t.Errorf("report path %q does not end with .json", path)
	}
}

// TestGenerateLocalReport_FilePermissions verifies the file is created with
// mode 0600 (owner-read/write only) so sensitive data is not world-readable.
func TestGenerateLocalReport_FilePermissions(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path, _, err := GenerateLocalReport(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("GenerateLocalReport error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}

	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Errorf("report file mode = %04o, want 0600", mode)
	}
}
