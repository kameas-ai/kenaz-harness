package tools

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestCheckPrereqs_EmptyCommand returns nil for an empty command.
func TestCheckPrereqs_EmptyCommand(t *testing.T) {
	t.Parallel()
	if got := CheckPrereqs(nil); got != nil {
		t.Errorf("CheckPrereqs(nil) = %v, want nil", got)
	}
	if got := CheckPrereqs([]string{}); got != nil {
		t.Errorf("CheckPrereqs([]) = %v, want nil", got)
	}
}

// TestCheckPrereqs_UnknownCommand returns nil for non-catalog commands.
func TestCheckPrereqs_UnknownCommand(t *testing.T) {
	t.Parallel()
	unknown := []string{"/usr/bin/env", "python3", "-m", "somemod"}
	if got := CheckPrereqs(unknown); got != nil {
		t.Errorf("CheckPrereqs(unknown) = %v, want nil (non-catalog commands skipped)", got)
	}
}

// TestCheckPrereqs_PresentBinary verifies that a binary that IS in $PATH
// produces no missing prereqs.
// NOTE: Not parallel — mutates the package-level knownPrereqs map.
func TestCheckPrereqs_PresentBinary(t *testing.T) {
	// Temporarily register a fake binary name so we don't depend on npx/uv
	// being installed on the CI runner.
	fakeBin := resolvableTestBinary(t)
	if fakeBin == "" {
		t.Skip("cannot resolve a guaranteed-in-PATH binary for this test")
	}

	old := knownPrereqs
	defer func() { knownPrereqs = old }()
	knownPrereqs = map[string]RuntimePrereq{
		fakeBin: {
			Name:        "test-runtime",
			Cmds:        []string{fakeBin},
			InstallHint: "n/a",
		},
	}
	got := CheckPrereqs([]string{fakeBin, "arg1"})
	if got != nil {
		t.Errorf("CheckPrereqs([%q, ...]) = %v, want nil (binary is in PATH)", fakeBin, got)
	}
}

// TestCheckPrereqs_MissingBinary verifies that a binary that is NOT in $PATH
// produces a MissingPrereq entry.
// NOTE: Not parallel — mutates the package-level knownPrereqs map.
func TestCheckPrereqs_MissingBinary(t *testing.T) {
	const ghostBin = "__kenaz_nonexistent_binary_12345__"

	old := knownPrereqs
	defer func() { knownPrereqs = old }()
	knownPrereqs = map[string]RuntimePrereq{
		ghostBin: {
			Name:        "ghost-runtime",
			Cmds:        []string{ghostBin},
			InstallHint: "install ghost",
		},
	}
	got := CheckPrereqs([]string{ghostBin, "arg1"})
	if len(got) != 1 {
		t.Fatalf("CheckPrereqs([%q, ...]) = %v, want 1 MissingPrereq", ghostBin, got)
	}
	if got[0].Name != "ghost-runtime" {
		t.Errorf("MissingPrereq.Name = %q, want %q", got[0].Name, "ghost-runtime")
	}
	if !strings.Contains(got[0].InstallHint, "install ghost") {
		t.Errorf("MissingPrereq.InstallHint = %q, want hint containing 'install ghost'", got[0].InstallHint)
	}
}

// TestCheckPrereqs_PathPrefix verifies that a command like
// "/usr/local/bin/npx" is handled the same as "npx".
// NOTE: Not parallel — mutates the package-level knownPrereqs map.
func TestCheckPrereqs_PathPrefix(t *testing.T) {
	// Register "npx" as a ghost; the full-path variant should still hit it.
	const ghost = "__kenaz_npx_ghost__"

	old := knownPrereqs
	defer func() { knownPrereqs = old }()
	knownPrereqs = map[string]RuntimePrereq{
		"npx": {
			Name:        "Node.js / npx",
			Cmds:        []string{ghost}, // ghost so it fails PATH check
			InstallHint: "install node",
		},
	}
	got := CheckPrereqs([]string{"/usr/local/bin/npx", "-y", "@scope/pkg"})
	if len(got) != 1 {
		t.Fatalf("CheckPrereqs([/usr/local/bin/npx]) = %v, want 1 MissingPrereq (stripped to 'npx')", got)
	}
}

// TestPrereqError_Nil confirms that PrereqError(nil) == nil.
func TestPrereqError_Nil(t *testing.T) {
	t.Parallel()
	if err := PrereqError(nil); err != nil {
		t.Errorf("PrereqError(nil) = %v, want nil", err)
	}
}

// TestPrereqError_NonEmpty checks that PrereqError returns a non-nil error
// with the name and hint embedded.
func TestPrereqError_NonEmpty(t *testing.T) {
	t.Parallel()
	missing := []MissingPrereq{
		{Name: "uv / uvx", InstallHint: "brew install uv"},
	}
	err := PrereqError(missing)
	if err == nil {
		t.Fatal("PrereqError(missing) = nil, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "uv / uvx") {
		t.Errorf("error missing runtime name: %q", msg)
	}
	if !strings.Contains(msg, "brew install uv") {
		t.Errorf("error missing install hint: %q", msg)
	}
}

// TestCheckPrereqs_FilePrereqAbsent verifies that a recipe with a file
// prereq registered in recipeFilePrereqs returns a MissingPrereq of kind "file"
// when the file is not present on disk.
// NOTE: Not parallel — mutates the package-level recipeFilePrereqs and
// checkFilePrereqs variables.
func TestCheckPrereqs_FilePrereqAbsent(t *testing.T) {
	const ghostRecipe = "__kenaz_test_recipe_12345__"

	guide := &FileSetupGuide{
		TargetPath: "/nonexistent/path/to/creds.json",
		Steps:      []string{"Step 1", "Step 2"},
		DocsURL:    "https://example.com/docs",
	}
	oldPrereqs := recipeFilePrereqs
	defer func() { recipeFilePrereqs = oldPrereqs }()
	recipeFilePrereqs = map[string]MissingPrereq{
		ghostRecipe: {
			Name:           "Test credentials file",
			InstallHint:    "place the file at /nonexistent/path/to/creds.json",
			Kind:           "file",
			FileSetupGuide: guide,
		},
	}

	// Command is unknown (no runtime prereq); only the file prereq fires.
	got := CheckPrereqs([]string{"/usr/bin/env", "someserver"}, ghostRecipe)
	if len(got) != 1 {
		t.Fatalf("CheckPrereqs with missing file prereq = %v, want 1 MissingPrereq", got)
	}
	if got[0].Kind != "file" {
		t.Errorf("MissingPrereq.Kind = %q, want %q", got[0].Kind, "file")
	}
	if got[0].Name != "Test credentials file" {
		t.Errorf("MissingPrereq.Name = %q, want %q", got[0].Name, "Test credentials file")
	}
	if got[0].FileSetupGuide == nil {
		t.Fatal("MissingPrereq.FileSetupGuide is nil, want non-nil")
	}
	if got[0].FileSetupGuide.TargetPath != "/nonexistent/path/to/creds.json" {
		t.Errorf("FileSetupGuide.TargetPath = %q, want %q", got[0].FileSetupGuide.TargetPath, "/nonexistent/path/to/creds.json")
	}
	if len(got[0].FileSetupGuide.Steps) != 2 {
		t.Errorf("FileSetupGuide.Steps len = %d, want 2", len(got[0].FileSetupGuide.Steps))
	}
	if got[0].FileSetupGuide.DocsURL != "https://example.com/docs" {
		t.Errorf("FileSetupGuide.DocsURL = %q, want %q", got[0].FileSetupGuide.DocsURL, "https://example.com/docs")
	}
}

// TestCheckPrereqs_FilePrereqPresent verifies that a registered file prereq
// is satisfied (returns no MissingPrereq) when the file actually exists.
// NOTE: Not parallel — mutates the package-level recipeFilePrereqs.
func TestCheckPrereqs_FilePrereqPresent(t *testing.T) {
	// Create a temp file to act as the "present" credentials file.
	f, err := os.CreateTemp(t.TempDir(), "creds-*.json")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	f.Close()

	const ghostRecipe = "__kenaz_test_recipe_present__"
	oldPrereqs := recipeFilePrereqs
	defer func() { recipeFilePrereqs = oldPrereqs }()
	recipeFilePrereqs = map[string]MissingPrereq{
		ghostRecipe: {
			Name:        "Test credentials file",
			InstallHint: "place the file",
			Kind:        "file",
			FileSetupGuide: &FileSetupGuide{
				TargetPath: f.Name(),
				Steps:      []string{"Step 1"},
			},
		},
	}

	got := CheckPrereqs([]string{"/usr/bin/env", "someserver"}, ghostRecipe)
	if len(got) != 0 {
		t.Errorf("CheckPrereqs with present file = %v, want empty (prereq satisfied)", got)
	}
}

// TestCheckPrereqs_NoRecipeID verifies that file prereqs are NOT checked
// when no recipe ID is supplied (backwards-compatible one-arg call style).
// NOTE: Not parallel — mutates the package-level recipeFilePrereqs.
func TestCheckPrereqs_NoRecipeID(t *testing.T) {
	const ghostRecipe = "__kenaz_test_recipe_noid__"
	oldPrereqs := recipeFilePrereqs
	defer func() { recipeFilePrereqs = oldPrereqs }()
	recipeFilePrereqs = map[string]MissingPrereq{
		ghostRecipe: {
			Name:        "Some file",
			InstallHint: "place file",
			Kind:        "file",
			FileSetupGuide: &FileSetupGuide{
				TargetPath: "/nonexistent/some-creds.json",
				Steps:      []string{"Step 1"},
			},
		},
	}

	// Call without the recipe ID variadic arg — file prereq should NOT fire.
	got := CheckPrereqs([]string{"/usr/bin/env", "someserver"})
	if len(got) != 0 {
		t.Errorf("CheckPrereqs without recipe ID = %v, want nil (file prereq skipped)", got)
	}
}

// TestCheckPrereqs_GmailMissingCreds verifies that the gmail recipe returns
// a "file" kind MissingPrereq when ~/.gmail-mcp/gcp-oauth.keys.json is absent.
// The test stubs checkFilePrereqs so it never touches the real filesystem.
// NOTE: Not parallel — mutates the package-level checkFilePrereqs var.
func TestCheckPrereqs_GmailMissingCreds(t *testing.T) {
	// The gmail command uses npx; we stub knownPrereqs so the runtime check
	// is bypassed (we don't want this test to fail if npx is not installed).
	oldPrereqs := knownPrereqs
	defer func() { knownPrereqs = oldPrereqs }()
	knownPrereqs = map[string]RuntimePrereq{} // no runtime prereqs for this test

	// Stub checkFilePrereqs to return the gmail file prereq unconditionally.
	old := checkFilePrereqs
	defer func() { checkFilePrereqs = old }()
	checkFilePrereqs = func(recipeID string) *MissingPrereq {
		if recipeID != "gmail" {
			return nil
		}
		return &MissingPrereq{
			Name:        "Gmail OAuth credentials file",
			InstallHint: "create a Google Cloud OAuth client and save JSON to ~/.gmail-mcp/gcp-oauth.keys.json",
			Kind:        "file",
			FileSetupGuide: &FileSetupGuide{
				TargetPath: "/Users/testuser/.gmail-mcp/gcp-oauth.keys.json",
				Steps:      []string{"Open Google Cloud Console", "Enable Gmail API", "Create OAuth client", "Download JSON"},
				DocsURL:    "https://developers.google.com/gmail/api/quickstart/go",
			},
		}
	}

	got := CheckPrereqs([]string{"npx", "-y", "@gongrzhe/server-gmail-autoauth-mcp"}, "gmail")
	if len(got) != 1 {
		t.Fatalf("CheckPrereqs for gmail = %v, want 1 MissingPrereq (missing creds file)", got)
	}
	mp := got[0]
	if mp.Kind != "file" {
		t.Errorf("MissingPrereq.Kind = %q, want \"file\"", mp.Kind)
	}
	if mp.FileSetupGuide == nil {
		t.Fatal("MissingPrereq.FileSetupGuide is nil")
	}
	if mp.FileSetupGuide.DocsURL == "" {
		t.Error("FileSetupGuide.DocsURL is empty, want a non-empty docs URL")
	}
	if len(mp.FileSetupGuide.Steps) < 4 {
		t.Errorf("FileSetupGuide.Steps has %d steps, want ≥4", len(mp.FileSetupGuide.Steps))
	}
	if !strings.Contains(mp.FileSetupGuide.TargetPath, "gcp-oauth.keys.json") {
		t.Errorf("FileSetupGuide.TargetPath = %q, want path containing gcp-oauth.keys.json", mp.FileSetupGuide.TargetPath)
	}
}

// resolvableTestBinary returns a short binary name (no path separators)
// that exec.LookPath will find, so tests can exercise the "present" path
// without depending on the external uv/npx binaries.
func resolvableTestBinary(t *testing.T) string {
	t.Helper()
	candidates := []string{"sh", "cat", "echo", "ls", "true"}
	for _, c := range candidates {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	// Windows: try cmd / where.
	for _, c := range []string{"cmd", "where"} {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	// Absolute last resort: the test binary itself.
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if idx := strings.LastIndexByte(exe, '/'); idx >= 0 {
		return exe[idx+1:]
	}
	if idx := strings.LastIndexByte(exe, '\\'); idx >= 0 {
		return exe[idx+1:]
	}
	return exe
}
