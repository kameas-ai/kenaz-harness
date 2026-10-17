package personal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// legacyProvidersJSON is a providers.json exactly as a pre-WP04 build wrote
// it (writeLocked's MarshalIndent output), with no source / credential_id.
const legacyProvidersJSON = `{
  "version": 1,
  "providers": [
    {
      "id": "anthropic-default",
      "kind": "anthropic",
      "model": "claude-sonnet",
      "cred": {
        "kind": "keychain",
        "locator": "kenaz-harness/anthropic-default"
      }
    },
    {
      "id": "bedrock-work",
      "kind": "bedrock",
      "model": "anthropic.claude",
      "cred": {
        "kind": "aws_profile",
        "locator": "work"
      },
      "region": "us-east-2"
    }
  ]
}`

// TestLegacyProvidersJSON_RoundTripsUnchanged reads a real old-format file,
// checks every profile comes back as a local key, rewrites the store through
// Update and asserts the bytes on disk are identical to the original: the
// new optional fields neither appear nor change anything for existing users.
func TestLegacyProvidersJSON_RoundTripsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kenaz-harness", "providers.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(legacyProvidersJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d profiles", len(list))
	}
	for _, p := range list {
		if p.Cred.Source != "" || p.Cred.CredentialID != "" {
			t.Errorf("%s: legacy profile decoded with source=%q id=%q", p.ID, p.Cred.Source, p.Cred.CredentialID)
		}
		if p.Cred.Source.Effective() != llm.CredSourceLocal {
			t.Errorf("%s: effective source %q, want local", p.ID, p.Cred.Source.Effective())
		}
	}
	// Rewrite in the original order so the comparison is byte-for-byte.
	if err := s.Update(list[0]); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != legacyProvidersJSON {
		t.Fatalf("rewritten providers.json differs from the legacy file:\n%s", got)
	}
	if strings.Contains(string(got), "source") || strings.Contains(string(got), "credential_id") {
		t.Fatal("new fields leaked into a local-only file")
	}
}

func TestFleetSourcedProfile_PersistsAndValidates(t *testing.T) {
	s := newTempStore(t)
	p := llm.ProviderProfile{
		ID: "org-openai", Kind: "openai", Model: "gpt-4o",
		Cred: llm.CredentialReference{Kind: "keychain", Source: llm.CredSourceOrg, CredentialID: "0b6f2c1e-5d1a-4c55-9a0e-2f1f6d7f9b10"},
	}
	if err := s.Add(p); err != nil {
		t.Fatalf("Add fleet profile: %v", err)
	}
	got, err := s.Get("org-openai")
	if err != nil {
		t.Fatal(err)
	}
	if got.Cred != p.Cred {
		t.Fatalf("cred round-trip: got %+v want %+v", got.Cred, p.Cred)
	}

	bad := []llm.CredentialReference{
		{Kind: "keychain", Source: llm.CredSourceOrg},                              // no id
		{Kind: "env", Source: llm.CredSourceTeam, CredentialID: "c1"},              // not keychain
		{Kind: "keychain", Locator: "kenaz-harness/x", CredentialID: "c1"},         // id on a local key
		{Kind: "keychain", Source: "partner", CredentialID: "c1"},                  // unknown source
		{Kind: "keychain", Source: llm.CredSourceUserSync, CredentialID: "a/../b"}, // unsafe id
	}
	for i, c := range bad {
		q := p
		q.ID = "bad-" + string(rune('a'+i))
		q.Cred = c
		if err := s.Add(q); err == nil {
			t.Errorf("case %d (%+v): Add accepted an invalid reference", i, c)
		}
	}
}
