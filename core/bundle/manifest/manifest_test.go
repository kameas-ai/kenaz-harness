package manifest_test

import (
	"errors"
	"strings"
	"testing"

	bundle "github.com/kameas-ai/kenaz-harness/core/bundle"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
)

const validYAML = `
schema_version: 1
name: team-baseline
version: 0.4.2
license: Apache-2.0
metadata:
  homepage: https://example.com/team-baseline
dependencies:
  - name: org-providers
    version: ">=1.2.0,<2.0.0"
    source:
      kind: oci
      ref: ghcr.io/example/org-providers
artifacts:
  - name: claude-anthropic
    kind: provider_profile
    path: artifacts/providers/claude-anthropic.yaml
    content_hash: sha256:abc1230000000000000000000000000000000000000000000000000000000000
  - name: github-mcp
    kind: mcp_server
    path: artifacts/mcp/github.yaml
    content_hash: sha256:def4560000000000000000000000000000000000000000000000000000000000
signatures:
  - kind: sigstore_referrer
    locator: ghcr.io/example/team-baseline@sha256:abcdef
`

func TestParseValidates(t *testing.T) {
	m, err := manifest.Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Name != "team-baseline" {
		t.Errorf("name=%q, want team-baseline", m.Name)
	}
	if len(m.Artifacts) != 2 {
		t.Errorf("artifacts=%d, want 2", len(m.Artifacts))
	}
	if err := m.Validate(manifest.ValidateOpts{}); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestSchemaVersionTooNew(t *testing.T) {
	bad := strings.Replace(validYAML, "schema_version: 1", "schema_version: 999", 1)
	_, err := manifest.Parse([]byte(bad))
	if !errors.Is(err, bundle.ErrSchemaUnsupported) {
		t.Fatalf("err=%v, want ErrSchemaUnsupported", err)
	}
}

func TestSchemaVersionMissing(t *testing.T) {
	bad := strings.Replace(validYAML, "schema_version: 1\n", "", 1)
	_, err := manifest.Parse([]byte(bad))
	if !errors.Is(err, bundle.ErrManifestInvalid) {
		t.Fatalf("err=%v, want ErrManifestInvalid", err)
	}
}

func TestDuplicateArtifact(t *testing.T) {
	bad := strings.Replace(validYAML,
		"  - name: github-mcp\n    kind: mcp_server",
		"  - name: claude-anthropic\n    kind: provider_profile", 1)
	m, err := manifest.Parse([]byte(bad))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = m.Validate(manifest.ValidateOpts{})
	if !errors.Is(err, bundle.ErrDuplicateArtifact) {
		t.Fatalf("err=%v, want ErrDuplicateArtifact", err)
	}
}

func TestPathTraversal(t *testing.T) {
	cases := []string{
		"../escape.yaml",
		"a/../../escape.yaml",
		"/etc/passwd",
		"C:/Windows/System32/cmd.exe",
		"./a/../../b.yaml",
	}
	for _, badPath := range cases {
		bad := strings.Replace(validYAML,
			"path: artifacts/providers/claude-anthropic.yaml",
			"path: "+badPath, 1)
		m, err := manifest.Parse([]byte(bad))
		if err != nil {
			// some inputs may fail at YAML parse (e.g., colon syntax). Accept
			// either ErrPathTraversal at validate time or ErrManifestInvalid
			// at parse time — both are correct rejections.
			if !errors.Is(err, bundle.ErrManifestInvalid) {
				t.Errorf("path=%q: unexpected parse err %v", badPath, err)
			}
			continue
		}
		err = m.Validate(manifest.ValidateOpts{})
		if !errors.Is(err, bundle.ErrPathTraversal) {
			t.Errorf("path=%q: err=%v, want ErrPathTraversal", badPath, err)
		}
	}
}

func TestNorwayProblem(t *testing.T) {
	// The Norway problem in YAML 1.1 silently casts `NO`, `no`, `yes`, `on`,
	// `off` to booleans. We mitigate by mandating that `name` and other
	// short identifiers match a string-only regex; the JSON Schema's pattern
	// `^[a-z0-9][a-z0-9._-]*$` rejects raw bool tokens like `true`/`false`,
	// and quoted strings round-trip correctly. Confirm round-trip safety.
	const yamlWithQuotedNO = `
schema_version: 1
name: "norway-test"
version: 0.1.0
license: Apache-2.0
metadata:
  country: "NO"
artifacts:
  - name: a
    kind: provider_profile
    path: a.yaml
    content_hash: sha256:0000000000000000000000000000000000000000000000000000000000000000
`
	m, err := manifest.Parse([]byte(yamlWithQuotedNO))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := m.Metadata["country"]; got != "NO" {
		t.Errorf("country=%q, want \"NO\"", got)
	}
}

func TestContentHashStable(t *testing.T) {
	m1, err := manifest.Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("Parse 1: %v", err)
	}
	m2, err := manifest.Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("Parse 2: %v", err)
	}
	if m1.ContentHash() != m2.ContentHash() {
		t.Errorf("hashes diverge:\n  %s\n  %s", m1.ContentHash(), m2.ContentHash())
	}
	// Cross-permutation: shuffle artifact order in source and re-parse.
	shuffled := strings.Replace(validYAML,
		"artifacts:\n  - name: claude-anthropic\n    kind: provider_profile\n    path: artifacts/providers/claude-anthropic.yaml\n    content_hash: sha256:abc1230000000000000000000000000000000000000000000000000000000000\n  - name: github-mcp\n    kind: mcp_server\n    path: artifacts/mcp/github.yaml\n    content_hash: sha256:def4560000000000000000000000000000000000000000000000000000000000",
		"artifacts:\n  - name: github-mcp\n    kind: mcp_server\n    path: artifacts/mcp/github.yaml\n    content_hash: sha256:def4560000000000000000000000000000000000000000000000000000000000\n  - name: claude-anthropic\n    kind: provider_profile\n    path: artifacts/providers/claude-anthropic.yaml\n    content_hash: sha256:abc1230000000000000000000000000000000000000000000000000000000000",
		1)
	m3, err := manifest.Parse([]byte(shuffled))
	if err != nil {
		t.Fatalf("Parse 3: %v", err)
	}
	if m1.ContentHash() != m3.ContentHash() {
		t.Errorf("shuffled hash differs (canonical sort broken):\n  %s\n  %s",
			m1.ContentHash(), m3.ContentHash())
	}
}

func TestSchemaEmbedded(t *testing.T) {
	if len(manifest.SchemaJSON) < 100 {
		t.Errorf("schema not embedded (size=%d)", len(manifest.SchemaJSON))
	}
	if !strings.Contains(string(manifest.SchemaJSON), "kenaz.yaml") {
		t.Errorf("schema does not look like kenaz schema")
	}
}

// TestSigningPayloadExcludesSignatures is UNIT-1/UNIT-2's D-3 pin: the
// signed payload is CanonicalBytes() with Signatures treated as empty,
// so a signature's own locator (which the signer doesn't know yet at
// signing time) can never be part of what was signed.
func TestSigningPayloadExcludesSignatures(t *testing.T) {
	m, err := manifest.Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Signatures) == 0 {
		t.Fatal("fixture must carry at least one signature for this test to mean anything")
	}

	// 1. SigningPayload() must equal CanonicalBytes() computed on a
	// clone with Signatures cleared.
	clone := *m
	clone.Signatures = nil
	want := clone.CanonicalBytes()
	got := m.SigningPayload()
	if string(got) != string(want) {
		t.Fatalf("SigningPayload() != CanonicalBytes() with Signatures cleared:\n got=%q\nwant=%q", got, want)
	}

	// 2. SigningPayload() must differ from CanonicalBytes() on the
	// unmodified manifest (proving Signatures really does change
	// CanonicalBytes() — otherwise check 1 would pass vacuously).
	if string(m.SigningPayload()) == string(m.CanonicalBytes()) {
		t.Fatalf("SigningPayload() == CanonicalBytes(): fixture's Signatures entry has no effect on the canonical form")
	}

	// 3. Changing (or adding) a signature's locator must NOT change
	// SigningPayload() — this is the whole point (research/signed-payload.md):
	// the signer can choose the locator after signing without the
	// payload having depended on it.
	before := m.SigningPayload()
	m.Signatures = append(m.Signatures, manifest.SignatureRef{
		Kind:      "ed25519_detached",
		Locator:   "kenaz.yaml.sig",
		Algorithm: "ed25519",
		KeyID:     "deadbeef",
	})
	after := m.SigningPayload()
	if string(before) != string(after) {
		t.Fatalf("SigningPayload() changed after appending a signature ref:\n before=%q\n after=%q", before, after)
	}
	// Sanity: CanonicalBytes() DOES change (Signatures is part of it),
	// confirming the append above was a real, observable mutation.
	if string(m.CanonicalBytes()) == string(before) {
		t.Fatalf("appending a signature had no effect on CanonicalBytes(); test fixture is broken")
	}
}
