package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_RefusesProdAndRequiresOnedir(t *testing.T) {
	if err := run([]string{"--env", "prod", "--onedir", t.TempDir()}, os.Stdout, os.Stderr); err == nil || !strings.Contains(err.Error(), "prod") {
		t.Fatalf("expected the prod refusal, got %v", err)
	}
	if err := run([]string{"--root", t.TempDir()}, os.Stdout, os.Stderr); err == nil {
		t.Fatal("expected an error without --onedir")
	}
}

func TestRun_SeedsThenReportsStatus(t *testing.T) {
	src := filepath.Join(t.TempDir(), "kameas-ml")
	if err := os.MkdirAll(filepath.Join(src, "_internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"kameas-ml": "#!/bin/sh\n", "VERSION": "9.9.9\n", "_internal/x": "x"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--root", root, "--onedir", src, "--source", "test"}, out, os.Stderr); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := run([]string{"--root", root, "--status"}, out, os.Stderr); err != nil {
		t.Fatalf("status: %v", err)
	}
	b, _ := os.ReadFile(out.Name())
	if !strings.Contains(string(b), "seeded engine 9.9.9") || !strings.Contains(string(b), `"verifies": true`) {
		t.Fatalf("unexpected output:\n%s", b)
	}
}
