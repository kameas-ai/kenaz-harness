package lockfile_test

import (
	"errors"
	"strings"
	"testing"

	bundle "github.com/sigil-tech/kaneaz-harness/core/bundle"
	"github.com/sigil-tech/kaneaz-harness/core/bundle/lockfile"
)

func sample() *lockfile.Lockfile {
	return &lockfile.Lockfile{
		SchemaVersion: 1,
		Bundles: []lockfile.LockedBundle{
			{
				Name:        "team-baseline",
				Version:     "0.4.2",
				Source:      "oci://ghcr.io/example/team-baseline",
				ContentHash: "sha256:abc1230000000000000000000000000000000000000000000000000000000000",
				Dependencies: []lockfile.LockedDep{
					{Name: "org-providers", Version: "1.3.1", ContentHash: "sha256:def4560000000000000000000000000000000000000000000000000000000000"},
				},
				Artifacts: []lockfile.LockedArtifact{
					{Name: "claude-anthropic", Kind: "provider_profile", ContentHash: "sha256:1110000000000000000000000000000000000000000000000000000000000000"},
					{Name: "github-mcp", Kind: "mcp_server", ContentHash: "sha256:2220000000000000000000000000000000000000000000000000000000000000"},
				},
			},
			{
				Name:        "org-providers",
				Version:     "1.3.1",
				Source:      "oci://ghcr.io/example/org-providers",
				ContentHash: "sha256:def4560000000000000000000000000000000000000000000000000000000000",
			},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	lf := sample()
	b1, err := lockfile.Write(lf)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := lockfile.Read(b1)
	if err != nil {
		t.Fatalf("Read: %v\nbytes:\n%s", err, b1)
	}
	b2, err := lockfile.Write(got)
	if err != nil {
		t.Fatalf("Re-Write: %v", err)
	}
	if string(b1) != string(b2) {
		t.Errorf("round-trip not byte-identical:\n--first--\n%s\n--second--\n%s", b1, b2)
	}
}

func TestDeterminismAcrossInputOrder(t *testing.T) {
	lf := sample()
	b1, err := lockfile.Write(lf)
	if err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	// Reverse the bundles slice; canonical sort must produce identical bytes.
	lf2 := sample()
	lf2.Bundles[0], lf2.Bundles[1] = lf2.Bundles[1], lf2.Bundles[0]
	b2, err := lockfile.Write(lf2)
	if err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	if string(b1) != string(b2) {
		t.Errorf("ordering broke determinism:\n%s\n---\n%s", b1, b2)
	}
}

func TestSchemaVersionUnsupported(t *testing.T) {
	doc := "schema_version = 999\n[universal]\n"
	_, err := lockfile.Read([]byte(doc))
	if !errors.Is(err, bundle.ErrSchemaUnsupported) {
		t.Errorf("err=%v, want ErrSchemaUnsupported", err)
	}
}

func TestSchemaVersionMissing(t *testing.T) {
	doc := "[universal]\n"
	_, err := lockfile.Read([]byte(doc))
	if !errors.Is(err, bundle.ErrLockfileInvalid) {
		t.Errorf("err=%v, want ErrLockfileInvalid", err)
	}
}

func TestTrailingNewline(t *testing.T) {
	b, err := lockfile.Write(sample())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(b) == 0 || b[len(b)-1] != '\n' {
		t.Errorf("missing trailing newline")
	}
	if strings.Contains(string(b), "\r\n") {
		t.Errorf("CRLF found in output")
	}
}

func TestUniversalReserved(t *testing.T) {
	b, err := lockfile.Write(sample())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(string(b), "[universal]") {
		t.Errorf("[universal] table missing")
	}
}

func TestTieBreakOnContentHash(t *testing.T) {
	lf := &lockfile.Lockfile{
		SchemaVersion: 1,
		Bundles: []lockfile.LockedBundle{
			{Name: "x", Version: "1.0.0", Source: "s", ContentHash: "sha256:bbb"},
			{Name: "x", Version: "1.0.0", Source: "s", ContentHash: "sha256:aaa"},
		},
	}
	b, err := lockfile.Write(lf)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	// The aaa entry should appear before bbb.
	s := string(b)
	idxA := strings.Index(s, "sha256:aaa")
	idxB := strings.Index(s, "sha256:bbb")
	if idxA < 0 || idxB < 0 || idxA > idxB {
		t.Errorf("tie-break order wrong: aaa@%d bbb@%d", idxA, idxB)
	}
}

func TestResolveConflictsDeferred(t *testing.T) {
	err := lockfile.ResolveConflicts(sample())
	if !errors.Is(err, bundle.ErrNotImplementedV1x) {
		t.Errorf("err=%v, want ErrNotImplementedV1x", err)
	}
}

func TestContentHashStable(t *testing.T) {
	h1, err := sample().ContentHash()
	if err != nil {
		t.Fatalf("h1: %v", err)
	}
	h2, err := sample().ContentHash()
	if err != nil {
		t.Fatalf("h2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash unstable: %s vs %s", h1, h2)
	}
}
