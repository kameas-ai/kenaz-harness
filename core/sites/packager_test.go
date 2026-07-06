package sites_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/sites"
)

// buildTree creates a directory tree under dir for testing.
// Layout:
//
//	dir/
//	  a.txt        (content "alpha")
//	  b/
//	    c.txt      (content "charlie")
//	  secret.pem   (should be ignored)
//	  .env.local   (should be ignored)
//	  node_modules/ (should be ignored)
//	    pkg.js
func buildTree(t *testing.T, dir string) {
	t.Helper()
	mustWrite := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(dir, "a.txt"), "alpha")
	mustWrite(filepath.Join(dir, "b", "c.txt"), "charlie")
	mustWrite(filepath.Join(dir, "secret.pem"), "private key material")
	mustWrite(filepath.Join(dir, ".env.local"), "SECRET=hunter2")
	mustWrite(filepath.Join(dir, "node_modules", "pkg.js"), "npm")
}

// packDir is a helper that packs dir into a bytes.Buffer.
func packDir(t *testing.T, dir string, m sites.Manifest, opts sites.PackOptions) (sites.PackResult, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	res, err := sites.Pack(context.Background(), dir, m, &buf, opts)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	return res, &buf
}

// TestPackDeterminism verifies FR-101 / NFR-001: packing the same tree
// twice produces the same sha256.
func TestPackDeterminism(t *testing.T) {
	dir := t.TempDir()
	buildTree(t, dir)

	m := sites.Manifest{Version: 1, Name: "det-test", Type: sites.KindStatic}

	res1, _ := packDir(t, dir, m, sites.PackOptions{})
	res2, _ := packDir(t, dir, m, sites.PackOptions{})

	if res1.SHA256Hex == "" {
		t.Fatal("SHA256Hex is empty")
	}
	if res1.SHA256Hex != res2.SHA256Hex {
		t.Errorf("non-deterministic: run1=%q run2=%q", res1.SHA256Hex, res2.SHA256Hex)
	}
}

// TestPackDeterminismMtimeIndependent verifies that changing a file's mtime
// does NOT change the sha256 (the packager zeroes all mtimes).
func TestPackDeterminismMtimeIndependent(t *testing.T) {
	dir := t.TempDir()
	buildTree(t, dir)

	m := sites.Manifest{Version: 1, Name: "mtime-test", Type: sites.KindStatic}

	res1, _ := packDir(t, dir, m, sites.PackOptions{})

	// Touch a.txt with a different mtime.
	futureTime := time.Now().Add(72 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "a.txt"), futureTime, futureTime); err != nil {
		t.Fatal(err)
	}

	res2, _ := packDir(t, dir, m, sites.PackOptions{})

	if res1.SHA256Hex != res2.SHA256Hex {
		t.Errorf("sha256 changed after mtime bump: run1=%q run2=%q", res1.SHA256Hex, res2.SHA256Hex)
	}
}

// TestPackDefaultIgnores verifies FR-102: default ignore set always applied.
func TestPackDefaultIgnores(t *testing.T) {
	dir := t.TempDir()
	buildTree(t, dir)

	var warned []string
	opts := sites.PackOptions{
		WarnFn: func(p string) { warned = append(warned, p) },
	}
	m := sites.Manifest{Version: 1, Name: "ign-test", Type: sites.KindStatic}

	res, buf := packDir(t, dir, m, opts)

	// Check exclusion list.
	if !slices.Contains(res.Excluded, "secret.pem") {
		t.Errorf("secret.pem not excluded; excluded=%v", res.Excluded)
	}
	if !slices.Contains(res.Excluded, ".env.local") {
		t.Errorf(".env.local not excluded; excluded=%v", res.Excluded)
	}
	// node_modules/ itself or its contents should be excluded.
	nodeModulesExcluded := false
	for _, e := range res.Excluded {
		if e == "node_modules" || e == "node_modules/pkg.js" {
			nodeModulesExcluded = true
			break
		}
	}
	if !nodeModulesExcluded {
		t.Errorf("node_modules not excluded; excluded=%v", res.Excluded)
	}

	// WarnFn must be called for each excluded entry.
	if len(warned) == 0 {
		t.Error("WarnFn was never called")
	}
	if !slices.Contains(warned, "secret.pem") {
		t.Errorf("secret.pem warning not emitted; warned=%v", warned)
	}

	// Verify the tarball does NOT contain excluded files.
	entries := tarEntryNames(t, buf.Bytes())
	for _, bad := range []string{"secret.pem", ".env.local", "node_modules/pkg.js"} {
		if slices.Contains(entries, bad) {
			t.Errorf("excluded file %q found in tarball entries", bad)
		}
	}
}

// TestPackManifestIgnoreGlobs verifies that manifest.Ignore globs are appended.
func TestPackManifestIgnoreGlobs(t *testing.T) {
	dir := t.TempDir()
	buildTree(t, dir)
	// Add an extra file that the manifest ignores.
	if err := os.WriteFile(filepath.Join(dir, "data.parquet"), []byte("parquet"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := sites.Manifest{
		Version: 1, Name: "custom-ignore", Type: sites.KindStatic,
		Ignore: []string{"*.parquet"},
	}
	res, buf := packDir(t, dir, m, sites.PackOptions{})

	if !slices.Contains(res.Excluded, "data.parquet") {
		t.Errorf("data.parquet not excluded; excluded=%v", res.Excluded)
	}
	entries := tarEntryNames(t, buf.Bytes())
	if slices.Contains(entries, "data.parquet") {
		t.Error("data.parquet should not appear in tarball")
	}
}

// TestPackContainsExpectedFiles verifies that the non-excluded files are present.
func TestPackContainsExpectedFiles(t *testing.T) {
	dir := t.TempDir()
	buildTree(t, dir)

	m := sites.Manifest{Version: 1, Name: "content-test", Type: sites.KindStatic}
	_, buf := packDir(t, dir, m, sites.PackOptions{})

	entries := tarEntryNames(t, buf.Bytes())

	for _, want := range []string{"a.txt", "b/c.txt"} {
		if !slices.Contains(entries, want) {
			t.Errorf("expected entry %q not found in tarball; entries=%v", want, entries)
		}
	}
}

// TestPackLexicographicOrder verifies that tar entries are in sorted order (FR-101).
func TestPackLexicographicOrder(t *testing.T) {
	dir := t.TempDir()
	// Create files in non-alphabetical creation order.
	for _, name := range []string{"z.txt", "a.txt", "m.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m := sites.Manifest{Version: 1, Name: "order-test", Type: sites.KindStatic}
	_, buf := packDir(t, dir, m, sites.PackOptions{})

	entries := tarEntryNames(t, buf.Bytes())
	// Filter out directory entries.
	var fileEntries []string
	for _, e := range entries {
		if e != "" && e[len(e)-1] != '/' {
			fileEntries = append(fileEntries, e)
		}
	}

	for i := 1; i < len(fileEntries); i++ {
		if fileEntries[i] < fileEntries[i-1] {
			t.Errorf("entries not sorted: %q before %q", fileEntries[i-1], fileEntries[i])
		}
	}
}

// TestPackSymlinkEscape verifies FR-103: symlinks that resolve outside the
// root are rejected with a typed error.
func TestPackSymlinkEscape(t *testing.T) {
	outer := t.TempDir()
	inner := filepath.Join(outer, "site")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "legit.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Symlink that escapes: inner/escape → outer/secret
	if err := os.WriteFile(filepath.Join(outer, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outer, "secret.txt"), filepath.Join(inner, "escape")); err != nil {
		t.Skip("symlinks not supported on this platform:", err)
	}

	m := sites.Manifest{Version: 1, Name: "symlink-test", Type: sites.KindStatic}
	var buf bytes.Buffer
	_, err := sites.Pack(context.Background(), inner, m, &buf, sites.PackOptions{})
	if err == nil {
		t.Fatal("expected error for symlink escape, got nil")
	}
	var escErr *sites.ErrSymlinkEscape
	if !errors.As(err, &escErr) {
		t.Errorf("expected *ErrSymlinkEscape, got %T: %v", err, err)
	}
	if escErr != nil && escErr.Link == "" {
		t.Error("ErrSymlinkEscape.Link is empty")
	}
}

// TestPackInternalSymlink verifies that symlinks pointing inside the root
// are followed and included (they do NOT escape).
func TestPackInternalSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.txt"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Symlink that stays inside: dir/link.txt → dir/real.txt
	if err := os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Skip("symlinks not supported on this platform:", err)
	}

	m := sites.Manifest{Version: 1, Name: "internal-symlink", Type: sites.KindStatic}
	var buf bytes.Buffer
	res, err := sites.Pack(context.Background(), dir, m, &buf, sites.PackOptions{})
	if err != nil {
		t.Fatalf("unexpected error for internal symlink: %v", err)
	}
	if res.SHA256Hex == "" {
		t.Error("SHA256Hex is empty")
	}
}

// TestPackModeNormalization verifies that file modes are normalised to 0644/0755.
func TestPackModeNormalization(t *testing.T) {
	dir := t.TempDir()
	// Write a file with executable bit set.
	execFile := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(execFile, []byte("#!/bin/sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := sites.Manifest{Version: 1, Name: "mode-test", Type: sites.KindStatic}
	var buf bytes.Buffer
	if _, err := sites.Pack(context.Background(), dir, m, &buf, sites.PackOptions{}); err != nil {
		t.Fatal(err)
	}

	gz, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal("gzip open:", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal("tar next:", err)
		}
		if hdr.Name == "script.sh" {
			// Should be normalised to 0644, not 0755.
			if hdr.Mode != 0o644 {
				t.Errorf("script.sh mode = %04o, want 0644", hdr.Mode)
			}
			break
		}
	}
}

// TestPackContextCancel verifies that cancelling the context aborts Pack.
func TestPackContextCancel(t *testing.T) {
	dir := t.TempDir()
	buildTree(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	m := sites.Manifest{Version: 1, Name: "cancel-test", Type: sites.KindStatic}
	var buf bytes.Buffer
	_, err := sites.Pack(ctx, dir, m, &buf, sites.PackOptions{})
	if err == nil {
		t.Fatal("expected error after context cancel, got nil")
	}
}

// tarEntryNames extracts the list of entry names from a gzipped tarball.
func tarEntryNames(t *testing.T, data []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal("gzip open:", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal("tar next:", err)
		}
		names = append(names, hdr.Name)
	}
	return names
}
