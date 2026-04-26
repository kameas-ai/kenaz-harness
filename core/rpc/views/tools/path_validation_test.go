package tools

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateAllowedDir_AcceptsExistingTmpDir(t *testing.T) {
	dir := t.TempDir()
	if err := ValidateAllowedDir(dir); err != nil {
		t.Fatalf("ValidateAllowedDir(%q) = %v, want nil", dir, err)
	}
}

func TestValidateAllowedDir_RejectsRelativePath(t *testing.T) {
	err := ValidateAllowedDir("relative/path")
	if !errors.Is(err, ErrPathNotAbsolute) {
		t.Errorf("got %v, want ErrPathNotAbsolute", err)
	}
}

func TestValidateAllowedDir_RejectsEmptyPath(t *testing.T) {
	err := ValidateAllowedDir("")
	if !errors.Is(err, ErrPathNotAbsolute) {
		t.Errorf("got %v, want ErrPathNotAbsolute", err)
	}
}

func TestValidateAllowedDir_RejectsTildePath(t *testing.T) {
	// "~/foo" is technically not absolute (no leading /), so the
	// validator rejects it for the caller to expand.
	err := ValidateAllowedDir("~/Documents")
	if !errors.Is(err, ErrPathNotAbsolute) {
		t.Errorf("got %v, want ErrPathNotAbsolute", err)
	}
}

func TestValidateAllowedDir_RejectsNonExistent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist", "nested")
	err := ValidateAllowedDir(dir)
	if !errors.Is(err, ErrPathNotFound) {
		t.Errorf("got %v, want ErrPathNotFound", err)
	}
}

func TestValidateAllowedDir_RejectsRoot(t *testing.T) {
	err := ValidateAllowedDir("/")
	if !errors.Is(err, ErrPathInDenyList) {
		t.Errorf("got %v, want ErrPathInDenyList", err)
	}
}

func TestValidateAllowedDir_RejectsDenyRootsExact(t *testing.T) {
	cases := []string{
		"/etc",
		"/System",
		"/Library",
		"/private",
		"/Applications",
		"/usr/bin",
		"/usr/sbin",
		"/sbin",
		"/bin",
		"/dev",
	}
	for _, p := range cases {
		// Skip cases where the path doesn't exist on this host.
		if _, err := os.Stat(p); err != nil {
			continue
		}
		err := ValidateAllowedDir(p)
		if !errors.Is(err, ErrPathInDenyList) {
			t.Errorf("ValidateAllowedDir(%q) = %v, want ErrPathInDenyList", p, err)
		}
	}
}

func TestValidateAllowedDir_RejectsDenyRootChildren(t *testing.T) {
	// /etc/passwd is below /etc, which is in the deny-list. The
	// validator must reject children of denied roots, not just the
	// root itself.
	candidate := "/etc/hosts"
	if _, err := os.Stat(candidate); err != nil {
		t.Skipf("%s not present, skipping", candidate)
	}
	err := ValidateAllowedDir(candidate)
	if !errors.Is(err, ErrPathInDenyList) {
		t.Errorf("got %v, want ErrPathInDenyList", err)
	}
}

func TestValidateAllowedDir_TraversalIsCanonicalized(t *testing.T) {
	// "/etc/../tmp" canonicalizes to "/tmp", which is NOT in the
	// deny-list — the validator should accept it (it's a real
	// allowed temp area). The point of this test is to confirm
	// canonicalization runs BEFORE the deny-list check.
	if _, err := os.Stat("/tmp"); err != nil {
		t.Skip("/tmp not present, skipping")
	}
	if _, err := os.Stat("/etc"); err != nil {
		t.Skip("/etc not present, skipping")
	}
	err := ValidateAllowedDir("/etc/../tmp")
	if err != nil {
		t.Errorf("ValidateAllowedDir(/etc/../tmp) = %v, want nil (canonicalizes to /tmp)", err)
	}
}

func TestValidateAllowedDir_TraversalLandingInDenyListRejected(t *testing.T) {
	// "/tmp/../etc" canonicalizes to "/etc" which IS denied.
	if _, err := os.Stat("/etc"); err != nil {
		t.Skip("/etc not present")
	}
	if _, err := os.Stat("/tmp"); err != nil {
		t.Skip("/tmp not present")
	}
	err := ValidateAllowedDir("/tmp/../etc")
	if !errors.Is(err, ErrPathInDenyList) {
		t.Errorf("got %v, want ErrPathInDenyList", err)
	}
}

func TestValidateAllowedDir_RejectsUserHomeItself(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no resolvable user home dir")
	}
	if _, err := os.Stat(home); err != nil {
		t.Skip("user home dir does not exist on this host")
	}
	gotErr := ValidateAllowedDir(home)
	if !errors.Is(gotErr, ErrPathInDenyList) {
		t.Errorf("ValidateAllowedDir(home=%q) = %v, want ErrPathInDenyList", home, gotErr)
	}
}

func TestValidateAllowedDir_AllowsHomeChild(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no resolvable user home dir")
	}
	// Create a subdir under the user's home, then clean it up. We
	// can't rely on a particular user-owned subdir existing; this
	// test asserts the policy (children allowed) using a fresh dir.
	tmp, err := os.MkdirTemp(home, "kaneaz-test-*")
	if err != nil {
		t.Skipf("cannot mkdir under home (%v), skipping", err)
	}
	defer os.RemoveAll(tmp)
	if err := ValidateAllowedDir(tmp); err != nil {
		t.Errorf("ValidateAllowedDir(home child %q) = %v, want nil", tmp, err)
	}
}

func TestEnsureWorkspace_CreatesDirAndMarker(t *testing.T) {
	dataDir := t.TempDir()
	got, err := EnsureWorkspace(dataDir)
	if err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}
	want := filepath.Join(dataDir, "agent-workspace")
	if got != want {
		t.Errorf("EnsureWorkspace returned %q, want %q", got, want)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("workspace not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("workspace is not a directory")
	}
	marker := filepath.Join(got, ".kaneaz-workspace")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("marker file missing: %v", err)
	}
}

func TestEnsureWorkspace_Idempotent(t *testing.T) {
	dataDir := t.TempDir()
	first, err := EnsureWorkspace(dataDir)
	if err != nil {
		t.Fatalf("first EnsureWorkspace: %v", err)
	}
	// Touch the marker with sentinel content; a re-run must NOT
	// overwrite it.
	marker := filepath.Join(first, ".kaneaz-workspace")
	const sentinel = "preserved"
	if err := os.WriteFile(marker, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := EnsureWorkspace(dataDir)
	if err != nil {
		t.Fatalf("second EnsureWorkspace: %v", err)
	}
	if second != first {
		t.Errorf("second call returned %q, want same as first %q", second, first)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sentinel {
		t.Errorf("marker overwritten: got %q, want %q", got, sentinel)
	}
}

func TestEnsureWorkspace_EmptyDataDirError(t *testing.T) {
	if _, err := EnsureWorkspace(""); err == nil {
		t.Errorf("EnsureWorkspace(\"\") = nil, want error")
	}
}

func TestEnsureWorkspaceProducesValidatableDir(t *testing.T) {
	// The whole point of EnsureWorkspace is to produce a directory
	// that ValidateAllowedDir accepts; otherwise the recipe-install
	// flow can't use it.
	dataDir := t.TempDir()
	workspace, err := EnsureWorkspace(dataDir)
	if err != nil {
		t.Fatalf("EnsureWorkspace: %v", err)
	}
	if err := ValidateAllowedDir(workspace); err != nil {
		t.Errorf("ValidateAllowedDir(%q) = %v, want nil", workspace, err)
	}
}
