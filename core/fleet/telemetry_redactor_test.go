package fleet

import (
	"testing"
)

// TestRedactString_PatternMatrix tests every pattern rule against a representative
// input that should match and an input that should NOT match.
func TestRedactString_PatternMatrix(t *testing.T) {
	t.Parallel()
	r := NewTelemetryRedactor()

	tests := []struct {
		name       string
		input      string
		wantMatch  bool // true = expect redaction; false = expect pass-through
		wantOutput string
	}{
		{
			name:       "anthropic key",
			input:      "sk-ant-api03-verylongsecretkeyabcdefghijklmnopqrstuvwxyz",
			wantMatch:  true,
			wantOutput: "[REDACTED:anthropic-key]",
		},
		{
			name:       "openai project key",
			input:      "sk-proj-abcdefghijklmnopqrstuvwxyz1234",
			wantMatch:  true,
			wantOutput: "[REDACTED:openai-key]",
		},
		{
			name:       "generic sk- key",
			input:      "sk-abcdefghijklmnopqrstuvwxyz",
			wantMatch:  true,
			wantOutput: "[REDACTED:openai-key]",
		},
		{
			name:       "bearer token",
			input:      "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc",
			wantMatch:  true,
			wantOutput: "Authorization: [REDACTED:bearer-token]",
		},
		{
			name:       "jwt token",
			input:      "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123",
			wantMatch:  true,
			wantOutput: "[REDACTED:jwt-token]",
		},
		{
			name:       "aws access key id AKIA",
			input:      "key=AKIAIOSFODNN7EXAMPLE",
			wantMatch:  true,
			wantOutput: "key=[REDACTED:aws-key-id]",
		},
		{
			name:       "secret ref locator",
			input:      "@secret:anthropic/key/prod",
			wantMatch:  true,
			wantOutput: "[REDACTED:secret-ref]",
		},
		{
			name:       "sentry DSN",
			input:      "https://0123456789abcdef0123456789abcdef@o1234567.ingest.sentry.io/1234567",
			wantMatch:  true,
			wantOutput: "[REDACTED:sentry-dsn]",
		},
		{
			name:       "oauth client_secret",
			input:      "client_secret=my-very-long-client-secret-value",
			wantMatch:  true,
			wantOutput: "[REDACTED:oauth-secret]",
		},
		{
			name:       "password= pattern",
			input:      "password=hunter2hunter2",
			wantMatch:  true,
			wantOutput: "[REDACTED:password]",
		},
		{
			name:       "gemini api key",
			// AIzaSy + exactly 33 alphanumeric chars
			input:      "AIzaSy" + "abcdefghijklmnopqrstuvwxyz0123456",
			wantMatch:  true,
			wantOutput: "[REDACTED:apikey]",
		},
		// Pass-through cases
		{
			name:       "plain span name no secret",
			input:      "http.client.request",
			wantMatch:  false,
			wantOutput: "http.client.request",
		},
		{
			name:       "short sk prefix not matched",
			input:      "sk-short", // fewer than 20 chars after sk-
			wantMatch:  false,
			wantOutput: "sk-short",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := r.RedactString(tc.input)
			if tc.wantMatch {
				if got == tc.input {
					t.Errorf("RedactString(%q) = %q (unchanged), want redaction", tc.input, got)
				}
				if tc.wantOutput != "" && got != tc.wantOutput {
					t.Errorf("RedactString(%q) = %q, want %q", tc.input, got, tc.wantOutput)
				}
			} else {
				if got != tc.wantOutput {
					t.Errorf("RedactString(%q) = %q, want %q (unchanged)", tc.input, got, tc.wantOutput)
				}
			}
		})
	}
}

// TestIsSensitiveKey verifies the key-name deny list.
func TestIsSensitiveKey(t *testing.T) {
	t.Parallel()
	r := NewTelemetryRedactor()

	hits := []string{
		"password", "Password", "PASSWORD",
		"secret", "token", "api_key", "apikey",
		"access_key", "secret_key", "private_key",
		"auth", "authorization", "credential", "credentials",
		"passphrase", "pin",
		"private.my_field",   // prefix match
		"secret.my_field",    // prefix match
		"auth.header",        // prefix match
	}
	for _, k := range hits {
		if !r.IsSensitiveKey(k) {
			t.Errorf("IsSensitiveKey(%q) = false, want true", k)
		}
	}

	misses := []string{
		"span.name", "http.method", "db.statement", "user.id",
		"duration_ms", "error.class",
	}
	for _, k := range misses {
		if r.IsSensitiveKey(k) {
			t.Errorf("IsSensitiveKey(%q) = true, want false", k)
		}
	}
}

// TestRedactAttributes verifies that sensitive keys are blanked and
// non-sensitive string values are pattern-scrubbed.
func TestRedactAttributes(t *testing.T) {
	t.Parallel()
	r := NewTelemetryRedactor()

	attrs := map[string]any{
		"http.method":  "GET",
		"password":     "hunter2",
		"token":        "sk-ant-abc123456789012345678901",
		"span.name":    "chat.run",
		"user.message": "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123",
	}

	out := r.RedactAttributes(attrs)

	if out["http.method"] != "GET" {
		t.Errorf("http.method should be unchanged, got %v", out["http.method"])
	}
	if out["password"] != "[REDACTED]" {
		t.Errorf("password should be [REDACTED], got %v", out["password"])
	}
	if out["token"] != "[REDACTED]" {
		t.Errorf("token should be [REDACTED], got %v", out["token"])
	}
	if out["span.name"] != "chat.run" {
		t.Errorf("span.name should be unchanged, got %v", out["span.name"])
	}
	// The user.message has a JWT that should be scrubbed by the pattern.
	userMsg, _ := out["user.message"].(string)
	if userMsg == attrs["user.message"] {
		t.Errorf("user.message JWT not redacted: %q", userMsg)
	}

	// Original map must not be modified.
	if attrs["password"] != "hunter2" {
		t.Error("original attrs map was mutated")
	}
}

// TestDefaultRedactor confirms the package-level singleton is usable.
func TestDefaultRedactor(t *testing.T) {
	t.Parallel()
	if DefaultRedactor == nil {
		t.Fatal("DefaultRedactor is nil")
	}
	got := DefaultRedactor.RedactString("sk-ant-api03-xxxxxxxxxxxxxxxxxxxxxxxxxxx")
	if got == "sk-ant-api03-xxxxxxxxxxxxxxxxxxxxxxxxxxx" {
		t.Error("DefaultRedactor did not redact anthropic key")
	}
}
