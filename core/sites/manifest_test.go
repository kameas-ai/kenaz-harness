package sites_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/sites"
)

// writeManifest is a test helper that writes content to kameas-site.json
// in dir and returns the dir path.
func writeManifest(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kameas-site.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParseBytes_ValidStatic(t *testing.T) {
	data := `{
		"version": 1,
		"name": "my-site",
		"type": "static",
		"static": {"dir": "dist", "spa_fallback": true}
	}`
	m, err := sites.ParseBytes([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "my-site" {
		t.Errorf("Name = %q, want %q", m.Name, "my-site")
	}
	if m.Type != sites.KindStatic {
		t.Errorf("Type = %q, want %q", m.Type, sites.KindStatic)
	}
	if m.Static == nil || m.Static.Dir != "dist" {
		t.Errorf("Static.Dir = %q, want %q", func() string {
			if m.Static != nil {
				return m.Static.Dir
			}
			return "<nil>"
		}(), "dist")
	}
}

func TestParseBytes_ValidDynamic(t *testing.T) {
	data := `{
		"version": 1,
		"name": "dash-app",
		"type": "dynamic",
		"dynamic": {
			"runtime": "python3.12",
			"framework": "dash",
			"env": {"DB_URL": {"description": "warehouse DSN", "required": true}}
		}
	}`
	m, err := sites.ParseBytes([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Type != sites.KindDynamic {
		t.Errorf("Type = %q, want %q", m.Type, sites.KindDynamic)
	}
	if m.Dynamic == nil {
		t.Fatal("Dynamic is nil")
	}
	spec, ok := m.Dynamic.Env["DB_URL"]
	if !ok {
		t.Fatal("env[DB_URL] missing")
	}
	if !spec.Required {
		t.Errorf("env[DB_URL].Required = false, want true")
	}
}

// TestValidate_Matrix covers every FR-001 rejection case.
var validateMatrix = []struct {
	name    string
	json    string
	wantErr string // substring that must appear in the error
}{
	// ----- version -----
	{
		name:    "version 0 rejected",
		json:    `{"version":0,"name":"good","type":"static","static":{"dir":""}}`,
		wantErr: "version must be 1",
	},
	{
		name:    "version 2 rejected",
		json:    `{"version":2,"name":"good","type":"static","static":{"dir":""}}`,
		wantErr: "version must be 1",
	},
	// ----- name grammar -----
	{
		name:    "empty name rejected",
		json:    `{"version":1,"name":"","type":"static","static":{"dir":""}}`,
		wantErr: "name is required",
	},
	{
		name:    "name with uppercase rejected",
		json:    `{"version":1,"name":"MyApp","type":"static","static":{"dir":""}}`,
		wantErr: "does not match required pattern",
	},
	{
		name:    "name with leading hyphen rejected",
		json:    `{"version":1,"name":"-bad","type":"static","static":{"dir":""}}`,
		wantErr: "does not match required pattern",
	},
	{
		name:    "name with trailing hyphen rejected",
		json:    `{"version":1,"name":"bad-","type":"static","static":{"dir":""}}`,
		wantErr: "does not match required pattern",
	},
	{
		name:    "name with consecutive hyphens rejected (no --)",
		json:    `{"version":1,"name":"bad--name","type":"static","static":{"dir":""}}`,
		wantErr: "does not match required pattern",
	},
	{
		name:    "name exceeding 40 chars rejected",
		json:    `{"version":1,"name":"a123456789-b123456789-c123456789-d12345678","type":"static","static":{"dir":""}}`,
		wantErr: "maximum is 40",
	},
	{
		name: "name exactly 40 chars accepted",
		json: `{"version":1,"name":"a123456789-b123456789-c123456789-d123456","type":"static","static":{"dir":""}}`,
		// no error — "a123456789-b123456789-c123456789-d123456" is exactly 40 chars
	},
	// ----- type -----
	{
		name:    "unknown type rejected",
		json:    `{"version":1,"name":"good","type":"lambda","static":{"dir":""}}`,
		wantErr: "type must be",
	},
	// ----- both / neither sections -----
	{
		name:    "both static and dynamic present rejected",
		json:    `{"version":1,"name":"good","type":"static","static":{"dir":""},"dynamic":{"runtime":"python3.12"}}`,
		wantErr: "both",
	},
	{
		name:    "neither section present rejected",
		json:    `{"version":1,"name":"good","type":"static"}`,
		wantErr: "neither",
	},
	{
		name:    "type static but only dynamic section present",
		json:    `{"version":1,"name":"good","type":"static","dynamic":{"runtime":"python3.12"}}`,
		wantErr: "type is \"static\"",
	},
	{
		name:    "type dynamic but only static section present",
		json:    `{"version":1,"name":"good","type":"dynamic","static":{"dir":"dist"}}`,
		wantErr: "type is \"dynamic\"",
	},
	// ----- static.dir containment -----
	{
		name:    "static.dir absolute path rejected",
		json:    `{"version":1,"name":"good","type":"static","static":{"dir":"/etc/passwd"}}`,
		wantErr: "absolute path",
	},
	{
		name:    "static.dir with .. rejected",
		json:    `{"version":1,"name":"good","type":"static","static":{"dir":"../escape"}}`,
		wantErr: "\"..\"",
	},
	{
		name:    "static.dir with embedded .. rejected",
		json:    `{"version":1,"name":"good","type":"static","static":{"dir":"dist/../../../etc"}}`,
		wantErr: "\"..\"",
	},
	{
		// Windows-separator escape: "..\esc" must be rejected on all platforms
		// (contract §2 requires forward-slash separators only).
		name:    "static.dir with Windows backslash escape rejected",
		json:    "{\"version\":1,\"name\":\"good\",\"type\":\"static\",\"static\":{\"dir\":\"..\\\\esc\"}}",
		wantErr: "backslash",
	},
	{
		// Backslash in a subdirectory path must also be rejected.
		name:    "static.dir with backslash subdirectory rejected",
		json:    "{\"version\":1,\"name\":\"good\",\"type\":\"static\",\"static\":{\"dir\":\"dist\\\\sub\"}}",
		wantErr: "backslash",
	},
	// ----- dynamic.env value field -----
	{
		name:    "dynamic.env value field rejected",
		json:    `{"version":1,"name":"good","type":"dynamic","dynamic":{"runtime":"python3.12","env":{"SECRET":{"value":"hunter2","required":true}}}}`,
		wantErr: "\"value\" field",
	},
	// ----- custom framework entrypoint -----
	{
		name:    "custom framework without entrypoint rejected",
		json:    `{"version":1,"name":"good","type":"dynamic","dynamic":{"runtime":"python3.12","framework":"custom"}}`,
		wantErr: "entrypoint is empty",
	},
	// ----- valid cases -----
	{
		name: "streamlit framework accepted without entrypoint",
		json: `{"version":1,"name":"good","type":"dynamic","dynamic":{"runtime":"python3.12","framework":"streamlit"}}`,
	},
	{
		name: "unknown runtime accepted (server validates enum)",
		json: `{"version":1,"name":"good","type":"dynamic","dynamic":{"runtime":"fortran77"}}`,
	},
	{
		name: "static with empty dir accepted (site root served)",
		json: `{"version":1,"name":"good","type":"static","static":{"dir":""}}`,
	},
}

func TestValidate_Matrix(t *testing.T) {
	for _, tc := range validateMatrix {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := sites.ParseBytes([]byte(tc.json))
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q\nwant substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestParse_ReadsFile(t *testing.T) {
	dir := writeManifest(t, `{"version":1,"name":"file-site","type":"static","static":{"dir":"dist"}}`)
	m, err := sites.Parse(dir)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Name != "file-site" {
		t.Errorf("Name = %q, want %q", m.Name, "file-site")
	}
}

func TestParse_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := sites.Parse(dir)
	if err == nil {
		t.Fatal("expected error for missing kameas-site.json")
	}
	if !strings.Contains(err.Error(), "kameas-site.json") {
		t.Errorf("error %q does not mention file name", err.Error())
	}
}

func TestApplyFrameworkPresets_Streamlit(t *testing.T) {
	d := sites.DynamicSection{Framework: sites.FrameworkStreamlit}
	out := sites.ApplyFrameworkPresets(d)
	if out.Entrypoint == "" {
		t.Error("Entrypoint not filled by streamlit preset")
	}
	if !strings.Contains(out.Entrypoint, "streamlit run app.py") {
		t.Errorf("Entrypoint = %q, want streamlit run app.py ...", out.Entrypoint)
	}
	if out.HealthPath != "/_stcore/health" {
		t.Errorf("HealthPath = %q, want /_stcore/health", out.HealthPath)
	}
	if out.Size != "S" {
		t.Errorf("Size = %q, want S (default)", out.Size)
	}
}

func TestApplyFrameworkPresets_Dash(t *testing.T) {
	d := sites.DynamicSection{Framework: sites.FrameworkDash}
	out := sites.ApplyFrameworkPresets(d)
	if !strings.Contains(out.Entrypoint, "gunicorn") {
		t.Errorf("Entrypoint = %q, want gunicorn entrypoint", out.Entrypoint)
	}
	if out.HealthPath != "/" {
		t.Errorf("HealthPath = %q, want /", out.HealthPath)
	}
}

func TestApplyFrameworkPresets_CustomPreservesEntrypoint(t *testing.T) {
	d := sites.DynamicSection{Framework: sites.FrameworkCustom, Entrypoint: "python main.py"}
	out := sites.ApplyFrameworkPresets(d)
	if out.Entrypoint != "python main.py" {
		t.Errorf("Entrypoint = %q, want %q", out.Entrypoint, "python main.py")
	}
}

func TestApplyFrameworkPresets_DoesNotMutateInput(t *testing.T) {
	d := sites.DynamicSection{Framework: sites.FrameworkStreamlit}
	_ = sites.ApplyFrameworkPresets(d)
	if d.Entrypoint != "" {
		t.Error("ApplyFrameworkPresets mutated the input DynamicSection")
	}
}

func TestDynamicSizeDefault(t *testing.T) {
	data := `{"version":1,"name":"good","type":"dynamic","dynamic":{"runtime":"python3.12"}}`
	m, err := sites.ParseBytes([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Dynamic.Size != "S" {
		t.Errorf("Size = %q, want S (default)", m.Dynamic.Size)
	}
}

func TestIgnoreGlobsPreserved(t *testing.T) {
	data := `{"version":1,"name":"good","type":"static","static":{"dir":""},"ignore":["*.parquet","notebooks/"]}`
	m, err := sites.ParseBytes([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Ignore) != 2 {
		t.Errorf("Ignore = %v, want 2 entries", m.Ignore)
	}
}
