package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlaceRecipeFile_CopiesFileToTarget verifies that PlaceRecipeFile copies
// the source file to the recipe's declared target path and sets mode 0600.
// NOTE: Not parallel — mutates the package-level recipeFilePrereqs variable.
func TestPlaceRecipeFile_CopiesFileToTarget(t *testing.T) {
	// Create a temp source file with known content.
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "client_secret.json")
	wantContent := []byte(`{"installed":{"client_id":"fake"}}`)
	if err := os.WriteFile(srcPath, wantContent, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Redirect the gmail file prereq target into a temp dir.
	dstDir := t.TempDir()
	dstPath := filepath.Join(dstDir, "gcp-oauth.keys.json")

	const recipeID = "__kenaz_place_test_recipe__"
	old := recipeFilePrereqs
	defer func() { recipeFilePrereqs = old }()
	recipeFilePrereqs = map[string]MissingPrereq{
		recipeID: {
			Name:        "Test credentials file",
			InstallHint: "test",
			Kind:        "file",
			FileSetupGuide: &FileSetupGuide{
				TargetPath: dstPath,
				Steps:      []string{"Step 1"},
			},
		},
	}

	api := &API{}
	if err := api.PlaceRecipeFile(context.Background(), recipeID, srcPath); err != nil {
		t.Fatalf("PlaceRecipeFile: %v", err)
	}

	// Verify content.
	gotContent, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(gotContent) != string(wantContent) {
		t.Errorf("dst content = %q, want %q", gotContent, wantContent)
	}

	// Verify mode 0600.
	info, err := os.Stat(dstPath)
	if err != nil {
		t.Fatalf("Stat dst: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("dst mode = %04o, want 0600", mode)
	}
}

// TestPlaceRecipeFile_CreatesTargetDirectory verifies that PlaceRecipeFile
// creates the target directory if it does not exist.
// NOTE: Not parallel — mutates the package-level recipeFilePrereqs variable.
func TestPlaceRecipeFile_CreatesTargetDirectory(t *testing.T) {
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "creds.json")
	if err := os.WriteFile(srcPath, []byte(`{}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Use a non-existent sub-directory as the target dir.
	baseDir := t.TempDir()
	newDir := filepath.Join(baseDir, "nested", "dir")
	dstPath := filepath.Join(newDir, "gcp-oauth.keys.json")

	const recipeID = "__kenaz_place_mkdir_test__"
	old := recipeFilePrereqs
	defer func() { recipeFilePrereqs = old }()
	recipeFilePrereqs = map[string]MissingPrereq{
		recipeID: {
			Name: "Test creds", InstallHint: "test", Kind: "file",
			FileSetupGuide: &FileSetupGuide{TargetPath: dstPath, Steps: []string{"Step 1"}},
		},
	}

	api := &API{}
	if err := api.PlaceRecipeFile(context.Background(), recipeID, srcPath); err != nil {
		t.Fatalf("PlaceRecipeFile: %v", err)
	}

	if _, err := os.Stat(dstPath); err != nil {
		t.Errorf("destination file not found after PlaceRecipeFile: %v", err)
	}

	// Verify the created directory has mode 0700.
	info, err := os.Stat(newDir)
	if err != nil {
		t.Fatalf("Stat new dir: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0700 {
		t.Errorf("target dir mode = %04o, want 0700", mode)
	}
}

// TestPlaceRecipeFile_RejectsUnknownRecipe verifies that an unknown recipeID
// is rejected with a clear error.
// NOTE: Not parallel — mutates the package-level recipeFilePrereqs variable.
func TestPlaceRecipeFile_RejectsUnknownRecipe(t *testing.T) {
	old := recipeFilePrereqs
	defer func() { recipeFilePrereqs = old }()
	recipeFilePrereqs = map[string]MissingPrereq{} // empty registry

	api := &API{}
	err := api.PlaceRecipeFile(context.Background(), "unknown-recipe", "/some/src.json")
	if err == nil {
		t.Fatal("PlaceRecipeFile(unknown recipe): want error, got nil")
	}
	if !strings.Contains(err.Error(), "no registered file prereq") {
		t.Errorf("error = %q, want message about no registered file prereq", err.Error())
	}
}

// TestPlaceRecipeFile_RejectsRecipeWithoutFilePrereq verifies that a recipe
// with no file prereq is rejected.
// NOTE: Not parallel — mutates the package-level recipeFilePrereqs variable.
func TestPlaceRecipeFile_RejectsRecipeWithoutFilePrereq(t *testing.T) {
	const recipeID = "__kenaz_no_file_prereq__"
	old := recipeFilePrereqs
	defer func() { recipeFilePrereqs = old }()
	// Register the recipe but with a nil FileSetupGuide.
	recipeFilePrereqs = map[string]MissingPrereq{
		recipeID: {
			Name:           "Test",
			Kind:           "file",
			FileSetupGuide: nil, // explicitly nil
		},
	}

	api := &API{}
	err := api.PlaceRecipeFile(context.Background(), recipeID, "/some/src.json")
	if err == nil {
		t.Fatal("PlaceRecipeFile(nil FileSetupGuide): want error, got nil")
	}
	if !strings.Contains(err.Error(), "no FileSetupGuide") {
		t.Errorf("error = %q, want message about no FileSetupGuide", err.Error())
	}
}

// TestPlaceRecipeFile_RejectsEmptySrcPath verifies that an empty srcPath is
// rejected immediately without touching the filesystem.
// NOTE: Not parallel — mutates the package-level recipeFilePrereqs variable.
func TestPlaceRecipeFile_RejectsEmptySrcPath(t *testing.T) {
	const recipeID = "__kenaz_place_empty_src__"
	old := recipeFilePrereqs
	defer func() { recipeFilePrereqs = old }()
	recipeFilePrereqs = map[string]MissingPrereq{
		recipeID: {
			Name: "Test", Kind: "file",
			FileSetupGuide: &FileSetupGuide{TargetPath: "/tmp/test.json", Steps: []string{"Step 1"}},
		},
	}

	api := &API{}
	err := api.PlaceRecipeFile(context.Background(), recipeID, "")
	if err == nil {
		t.Fatal("PlaceRecipeFile(empty srcPath): want error, got nil")
	}
	if !strings.Contains(err.Error(), "srcPath must not be empty") {
		t.Errorf("error = %q, want message about srcPath", err.Error())
	}
}

// TestPlaceRecipeFile_OverwritesExistingDestination verifies that
// PlaceRecipeFile replaces an already-existing file at the target path.
// NOTE: Not parallel — mutates the package-level recipeFilePrereqs variable.
func TestPlaceRecipeFile_OverwritesExistingDestination(t *testing.T) {
	dstDir := t.TempDir()
	dstPath := filepath.Join(dstDir, "gcp-oauth.keys.json")
	// Pre-create the destination with old content.
	if err := os.WriteFile(dstPath, []byte(`{"old":"data"}`), 0600); err != nil {
		t.Fatalf("WriteFile old dst: %v", err)
	}

	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "new_creds.json")
	newContent := []byte(`{"new":"data"}`)
	if err := os.WriteFile(srcPath, newContent, 0644); err != nil {
		t.Fatalf("WriteFile src: %v", err)
	}

	const recipeID = "__kenaz_place_overwrite_test__"
	old := recipeFilePrereqs
	defer func() { recipeFilePrereqs = old }()
	recipeFilePrereqs = map[string]MissingPrereq{
		recipeID: {
			Name: "Test creds", Kind: "file",
			FileSetupGuide: &FileSetupGuide{TargetPath: dstPath, Steps: []string{"Step 1"}},
		},
	}

	api := &API{}
	if err := api.PlaceRecipeFile(context.Background(), recipeID, srcPath); err != nil {
		t.Fatalf("PlaceRecipeFile: %v", err)
	}

	gotContent, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("ReadFile dst: %v", err)
	}
	if string(gotContent) != string(newContent) {
		t.Errorf("dst content = %q, want %q", gotContent, newContent)
	}
}
