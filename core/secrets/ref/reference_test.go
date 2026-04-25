package ref

import (
	"errors"
	"strings"
	"testing"
)

func TestRefKind_String(t *testing.T) {
	cases := []struct {
		k    RefKind
		want string
	}{
		{RefEnv, "env"},
		{RefKeychain, "keychain"},
		{RefFile, "file"},
		{RefAWSProfile, "aws_profile"},
		{RefAWSKMS, "aws_kms"},
		{RefYubikeyPIV, "yubikey_piv"},
		{RefPKCS11, "pkcs11"},
		{RefUnknown, "unknown"},
		{RefKind(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.k.String(); got != c.want {
			t.Errorf("RefKind(%d).String()=%q want %q", c.k, got, c.want)
		}
	}
}

func TestAllKinds_NoUnknown(t *testing.T) {
	for _, k := range AllKinds() {
		if k == RefUnknown {
			t.Fatal("AllKinds must not include RefUnknown")
		}
	}
}

func TestFromMap_Positive(t *testing.T) {
	cases := []struct {
		name    string
		m       Map
		wantK   RefKind
		wantLoc string
	}{
		{"env", Map{"env": "ANTHROPIC_API_KEY"}, RefEnv, "ANTHROPIC_API_KEY"},
		{"keychain", Map{"keychain": "anthropic-api-key"}, RefKeychain, "anthropic-api-key"},
		{"file", Map{"file": "/run/secrets/db_key"}, RefFile, "/run/secrets/db_key"},
		{"aws_profile", Map{"aws_profile": "default"}, RefAWSProfile, "default"},
		{"aws_kms_arn", Map{"aws_kms": "arn:aws:kms:us-east-1:111122223333:key/abcd"}, RefAWSKMS, "arn:aws:kms:us-east-1:111122223333:key/abcd"},
		{"aws_kms_alias", Map{"aws_kms": "alias/my-key"}, RefAWSKMS, "alias/my-key"},
		{"yubikey_piv", Map{"yubikey_piv": "9a"}, RefYubikeyPIV, "9a"},
		{"pkcs11", Map{"pkcs11": "pkcs11:token=Foo;object=Bar"}, RefPKCS11, "pkcs11:token=Foo;object=Bar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := FromMap(c.m)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.Kind != c.wantK {
				t.Errorf("Kind=%v want %v", r.Kind, c.wantK)
			}
			if r.Locator != c.wantLoc {
				t.Errorf("Locator=%q want %q", r.Locator, c.wantLoc)
			}
			if r.ID() == "" {
				t.Error("ID should not be empty")
			}
			if strings.Contains(r.String(), c.wantLoc) {
				t.Errorf("String() must redact locator, got %q", r.String())
			}
		})
	}
}

func TestFromMap_ConsumerAndOptional(t *testing.T) {
	r, err := FromMap(Map{
		"keychain":    "x",
		"consumer_id": "llm:anthropic",
		"optional":    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.ConsumerID != "llm:anthropic" {
		t.Errorf("ConsumerID=%q", r.ConsumerID)
	}
	if !r.Optional {
		t.Error("Optional should be true")
	}
}

func TestFromMap_Negative(t *testing.T) {
	cases := []struct {
		name    string
		m       Map
		wantErr error
	}{
		{"empty", Map{}, ErrEmptyLocator},
		{"empty_locator", Map{"keychain": ""}, ErrEmptyLocator},
		{"whitespace_locator", Map{"keychain": "   "}, ErrEmptyLocator},
		{"unknown_kind", Map{"vault": "secret/data/x"}, ErrUnknownKind},
		{"ambiguous", Map{"env": "X", "keychain": "y"}, ErrAmbiguousReference},
		{"nonstring_locator", Map{"env": 123}, ErrLocatorFormat},
		{"env_invalid_name", Map{"env": "not a var"}, ErrLocatorFormat},
		{"aws_kms_bad_format", Map{"aws_kms": "just-a-name"}, ErrLocatorFormat},
		{"file_with_nul", Map{"file": "/etc/x\x00"}, ErrLocatorFormat},
		{"inline_plaintext_sk", Map{"keychain": "sk-AbCdEfGhIjKlMnOpQrStUvWx"}, ErrInlinePlaintext},
		{"inline_plaintext_bearer", Map{"env": "bearer xyzAbcDef1234567890qrst"}, ErrLocatorFormat}, // env name regex catches first
		{"inline_plaintext_keychain_bearer", Map{"keychain": "bearer xyzAbcDef1234567890qrst"}, ErrInlinePlaintext},
		{"inline_plaintext_long_b64", Map{"keychain": "AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIII"}, ErrInlinePlaintext},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := FromMap(c.m)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, c.wantErr) {
				t.Errorf("err=%v, want errors.Is %v", err, c.wantErr)
			}
		})
	}
}

func TestFromString_Positive(t *testing.T) {
	r, err := FromString("keychain:anthropic-api-key")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != RefKeychain || r.Locator != "anthropic-api-key" {
		t.Errorf("got %+v", r)
	}
}

func TestFromString_Negative(t *testing.T) {
	cases := []struct {
		in      string
		wantErr error
	}{
		{"", ErrEmptyLocator},
		{"   ", ErrLocatorFormat},
		{"no-colon", ErrLocatorFormat},
		{"bogus:value", ErrUnknownKind},
		{"keychain:", ErrEmptyLocator},
		{":locator", ErrLocatorFormat},
		{"keychain:sk-AbCdEfGhIjKlMnOpQrStUvWx", ErrInlinePlaintext},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			_, err := FromString(c.in)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, c.wantErr) {
				t.Errorf("err=%v, want errors.Is %v", err, c.wantErr)
			}
		})
	}
}

func TestID_StableAndRedacting(t *testing.T) {
	r1 := CredentialReference{Kind: RefKeychain, Locator: "anthropic-api-key"}
	r2 := CredentialReference{Kind: RefKeychain, Locator: "anthropic-api-key"}
	r3 := CredentialReference{Kind: RefKeychain, Locator: "openai-api-key"}
	r4 := CredentialReference{Kind: RefEnv, Locator: "anthropic-api-key"}
	if r1.ID() != r2.ID() {
		t.Error("identical references must produce identical IDs")
	}
	if r1.ID() == r3.ID() {
		t.Error("different locators must produce different IDs")
	}
	if r1.ID() == r4.ID() {
		t.Error("different kinds must produce different IDs")
	}
	if strings.Contains(r1.ID(), r1.Locator) {
		t.Error("ID must not contain locator")
	}
	if len(r1.ID()) != 16 {
		t.Errorf("ID length=%d, want 16", len(r1.ID()))
	}
}

func TestString_RedactsLocator(t *testing.T) {
	r := CredentialReference{Kind: RefKeychain, Locator: "anthropic-api-key", ConsumerID: "llm:anthropic"}
	s := r.String()
	if strings.Contains(s, "anthropic-api-key") {
		t.Errorf("String must not include locator, got %q", s)
	}
	if !strings.Contains(s, "llm:anthropic") {
		t.Errorf("String should include consumer_id, got %q", s)
	}
	if !strings.Contains(s, r.ID()) {
		t.Errorf("String should include redacted ID, got %q", s)
	}
}

func TestLooksLikeInlineSecret(t *testing.T) {
	hits := []string{
		"sk-AbCdEfGhIjKlMnOpQrStUvWx",
		"xoxb-1234567890-AbCdEfGh",
		"AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHH",
		"Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.AbCdEf",
	}
	for _, h := range hits {
		if !looksLikeInlineSecret(h) {
			t.Errorf("expected hit: %q", h)
		}
	}
	misses := []string{
		"",
		"short",
		"db-encryption-key", // legitimate keychain entry name
		"anthropic-api-key",
		"my_app_key",
	}
	for _, m := range misses {
		if looksLikeInlineSecret(m) {
			t.Errorf("expected no hit: %q", m)
		}
	}
}
