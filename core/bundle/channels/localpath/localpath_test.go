package localpath_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	bundle "github.com/kameas-ai/kenaz-harness/core/bundle"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels/localpath"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

func mkBundle(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "artifacts", "providers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "artifacts", "providers", "claude.yaml"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func openCh(t *testing.T, root string) channels.Channel {
	t.Helper()
	c, err := localpath.Factory(channels.ChannelSpec{Kind: localpath.Kind, Path: root}, secrets.NoopResolver{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestReachable(t *testing.T) {
	root := mkBundle(t)
	c := openCh(t, root)
	if err := c.Reachable(context.Background()); err != nil {
		t.Errorf("Reachable: %v", err)
	}
}

func TestReachableMissing(t *testing.T) {
	c, err := localpath.Factory(channels.ChannelSpec{Kind: localpath.Kind, Path: "/nonexistent/path/here"}, secrets.NoopResolver{})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Reachable(context.Background())
	if !errors.Is(err, bundle.ErrChannelUnreachable) {
		t.Errorf("err=%v, want ErrChannelUnreachable", err)
	}
}

func TestFetchOK(t *testing.T) {
	root := mkBundle(t)
	c := openCh(t, root)
	var buf bytes.Buffer
	res, err := c.Fetch(context.Background(), channels.ArtifactCoord{Path: "artifacts/providers/claude.yaml"}, &buf)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if buf.String() != "hello" {
		t.Errorf("body=%q want hello", buf.String())
	}
	if res.Bytes != 5 {
		t.Errorf("bytes=%d want 5", res.Bytes)
	}
}

func TestFetchPathTraversal(t *testing.T) {
	root := mkBundle(t)
	c := openCh(t, root)
	cases := []string{
		"../escape.yaml",
		"a/../../escape.yaml",
		"/etc/passwd",
		"./..",
		"..",
	}
	for _, p := range cases {
		var buf bytes.Buffer
		_, err := c.Fetch(context.Background(), channels.ArtifactCoord{Path: p}, &buf)
		if !errors.Is(err, bundle.ErrPathTraversal) {
			t.Errorf("path=%q err=%v, want ErrPathTraversal", p, err)
		}
	}
}

func TestLookupSignaturesNone(t *testing.T) {
	root := mkBundle(t)
	c := openCh(t, root)
	got, err := c.LookupSignatures(context.Background(), channels.ArtifactCoord{Path: "artifacts/providers/claude.yaml"})
	if err != nil {
		t.Fatalf("LookupSignatures: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list, got %v", got)
	}
}

func TestLookupSignaturesPresent(t *testing.T) {
	root := mkBundle(t)
	if err := os.WriteFile(filepath.Join(root, "artifacts", "providers", "claude.yaml.sig"), []byte("sig"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := openCh(t, root)
	got, err := c.LookupSignatures(context.Background(), channels.ArtifactCoord{Path: "artifacts/providers/claude.yaml"})
	if err != nil {
		t.Fatalf("LookupSignatures: %v", err)
	}
	if len(got) != 1 || got[0].Kind != "ed25519_detached" {
		t.Errorf("sigs=%v", got)
	}
}

func TestRegistryDuplicateRegister(t *testing.T) {
	r := channels.NewRegistry()
	if err := r.Register(localpath.Kind, localpath.Factory); err != nil {
		t.Fatalf("Register 1: %v", err)
	}
	if err := r.Register(localpath.Kind, localpath.Factory); err == nil {
		t.Errorf("expected duplicate register to fail")
	}
}

func TestRegistryUnknownKind(t *testing.T) {
	r := channels.NewRegistry()
	_, err := r.Open(channels.ChannelSpec{Kind: "no-such-kind"}, secrets.NoopResolver{})
	if !errors.Is(err, bundle.ErrChannelUnknown) {
		t.Errorf("err=%v, want ErrChannelUnknown", err)
	}
}

func TestFactoryThroughRegistry(t *testing.T) {
	r := channels.NewRegistry()
	if err := r.Register(localpath.Kind, localpath.Factory); err != nil {
		t.Fatal(err)
	}
	root := mkBundle(t)
	ch, err := r.Open(channels.ChannelSpec{Kind: localpath.Kind, Path: root}, secrets.NoopResolver{})
	if err != nil {
		t.Fatal(err)
	}
	if ch.Kind() != localpath.Kind {
		t.Errorf("kind=%s", ch.Kind())
	}
	if err := ch.Reachable(context.Background()); err != nil {
		t.Errorf("Reachable: %v", err)
	}
}
