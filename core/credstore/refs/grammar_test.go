package refs

import (
	"errors"
	"testing"
)

func TestParse_valid(t *testing.T) {
	cases := []struct {
		in      string
		locator string
		field   string
	}{
		{"@secret:user:foo", "user:foo", ""},
		{"@secret:user:example-api-token", "user:example-api-token", ""},
		{"@secret:provider:bedrock-prod", "provider:bedrock-prod", ""},
		{"@secret:provider:bedrock-prod#aws_session_token", "provider:bedrock-prod", "aws_session_token"},
		{"@secret:mcp:github:env:GITHUB_TOKEN", "mcp:github:env:GITHUB_TOKEN", ""},
		{"@secret:provider:my-anthropic:api_key#token", "provider:my-anthropic:api_key", "token"},
		{"@secret:ABC", "ABC", ""},
		{"@secret:a1#b2", "a1", "b2"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			ref, err := Parse(c.in)
			if err != nil {
				t.Fatalf("Parse(%q) = _, %v; want nil", c.in, err)
			}
			if ref.Locator != c.locator {
				t.Errorf("Locator = %q; want %q", ref.Locator, c.locator)
			}
			if ref.Field != c.field {
				t.Errorf("Field = %q; want %q", ref.Field, c.field)
			}
		})
	}
}

func TestParse_invalid(t *testing.T) {
	cases := []struct {
		in      string
		wantErr error
	}{
		// Wrong prefix
		{"secret:user:foo", ErrInvalidReference},
		{"@Secret:user:foo", ErrInvalidReference},
		{" @secret:user:foo", ErrInvalidReference},
		{"@SECRET:user:foo", ErrInvalidReference},
		// Empty locator
		{"@secret:", ErrEmptyLocator},
		// Control characters / NUL
		{"@secret:user\x00foo", ErrInvalidReference},
		{"@secret:user\x01foo", ErrInvalidReference},
		{"@secret:\x00", ErrInvalidReference},
		// Spaces in locator
		{"@secret:user foo", ErrInvalidReference},
		// Blank field after #
		{"@secret:user:foo#", ErrInvalidReference},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			_, err := Parse(c.in)
			if err == nil {
				t.Fatalf("Parse(%q) = _, nil; want error", c.in)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Errorf("error = %v; want wrapping %v", err, c.wantErr)
			}
		})
	}
}

func TestIsReferenceToken(t *testing.T) {
	if !IsReferenceToken("@secret:user:foo") {
		t.Error("expected valid token to return true")
	}
	if IsReferenceToken("secret:user:foo") {
		t.Error("expected invalid token to return false")
	}
	if IsReferenceToken("@secret:") {
		t.Error("expected empty-locator to return false")
	}
}

func TestFindAll(t *testing.T) {
	t.Run("no_matches", func(t *testing.T) {
		m := FindAll("no references here")
		if len(m) != 0 {
			t.Errorf("expected 0 matches, got %d", len(m))
		}
	})

	t.Run("single_match", func(t *testing.T) {
		src := "Bearer @secret:user:example-api-token"
		m := FindAll(src)
		if len(m) != 1 {
			t.Fatalf("expected 1 match, got %d", len(m))
		}
		if m[0].Reference.Locator != "user:example-api-token" {
			t.Errorf("Locator = %q; want user:example-api-token", m[0].Reference.Locator)
		}
		if m[0].Start != 7 {
			t.Errorf("Start = %d; want 7", m[0].Start)
		}
		if m[0].End != len(src) {
			t.Errorf("End = %d; want %d", m[0].End, len(src))
		}
		if m[0].Raw != "@secret:user:example-api-token" {
			t.Errorf("Raw = %q", m[0].Raw)
		}
	})

	t.Run("multi_match", func(t *testing.T) {
		src := "Authorization: Bearer @secret:user:foo\nX-API: @secret:provider:bar#key"
		m := FindAll(src)
		if len(m) != 2 {
			t.Fatalf("expected 2 matches, got %d", len(m))
		}
		if m[0].Reference.Locator != "user:foo" {
			t.Errorf("[0].Locator = %q", m[0].Reference.Locator)
		}
		if m[1].Reference.Locator != "provider:bar" {
			t.Errorf("[1].Locator = %q", m[1].Reference.Locator)
		}
		if m[1].Reference.Field != "key" {
			t.Errorf("[1].Field = %q", m[1].Reference.Field)
		}
	})

	t.Run("byte_offsets_correct", func(t *testing.T) {
		src := "a @secret:x:y b"
		m := FindAll(src)
		if len(m) != 1 {
			t.Fatalf("expected 1, got %d", len(m))
		}
		if src[m[0].Start:m[0].End] != m[0].Raw {
			t.Errorf("splice check failed: src[%d:%d]=%q, raw=%q",
				m[0].Start, m[0].End, src[m[0].Start:m[0].End], m[0].Raw)
		}
	})

	t.Run("non_overlapping", func(t *testing.T) {
		src := "@secret:a:b@secret:c:d"
		m := FindAll(src)
		// The two tokens are adjacent; both should match
		if len(m) != 2 {
			t.Fatalf("expected 2, got %d", len(m))
		}
	})
}

func TestReference_Token(t *testing.T) {
	r := Reference{Locator: "user:foo"}
	if r.Token() != "@secret:user:foo" {
		t.Errorf("Token() = %q", r.Token())
	}
	r2 := Reference{Locator: "provider:bar", Field: "key"}
	if r2.Token() != "@secret:provider:bar#key" {
		t.Errorf("Token() = %q", r2.Token())
	}
}
