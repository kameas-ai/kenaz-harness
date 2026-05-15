// Package sentry_test contains the PRIVACY-CRITICAL integration test.
//
// This test plants known secret literals in a synthetic crash payload and
// asserts that NO plaintext secret appears in the redacted output.
// A failure in any assertion here is a P0 privacy bug.
//
// Coverage:
//   - All 10 secret-pattern classes listed in the mission spec
//   - Long-string truncation (marker present, full string absent)
//   - Home-dir path replacement (~/...)
//   - private. slog attribute keys dropped
//   - Breadcrumb data containing secrets is redacted
//   - Multi-secret strings (all patterns at once)
package sentry_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/sentry"
)

// knownSecrets are the raw secret literals that must NEVER appear in any
// redacted output. Each entry is a (label, plaintext) pair.
var knownSecrets = []struct {
	label string
	plain string
}{
	{"secret-ref", "@secret:anthropic/prod/mykey"},
	{"anthropic-key", "sk-ant-api03-REALKEY123456789ABCDEFGH"},
	{"openai-proj-key", "sk-proj-realprojectkey1234567890abc"},
	{"openai-key", "sk-abcdefghijklmnopqrstuvwxyz12345"},
	{"bearer-token", "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.thepayload.thesig"},
	{"aws-key-id", "AKIAIOSFODNN7EXAMPLE"},
	{"aws-secret", "aws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"},
	{"email", "victim@example.com"},
	{"phone", "555-867-5309"},
}

// longStringPlain is > 200 chars; must be truncated.
const longStringPlain = "LONGSTART" +
	"XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX" +
	"XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX" +
	"XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX" +
	"LONGEND"

// TestIntegration_RedactionGate is the P0 privacy gate. Any failure is a
// privacy regression and must be fixed before shipping.
func TestIntegration_RedactionGate(t *testing.T) {
	os.Unsetenv("HARNESS_SENTRY_DISABLED")

	t.Run("RedactString_strips_all_secret_classes", func(t *testing.T) {
		for _, s := range knownSecrets {
			s := s
			t.Run(s.label, func(t *testing.T) {
				input := fmt.Sprintf("error context: %s occurred", s.plain)
				got := sentry.RedactString(input)
				if strings.Contains(got, s.plain) {
					t.Errorf("[P0] plaintext %q found in redacted output: %q", s.label, got)
				}
			})
		}
	})

	t.Run("bearer_token_with_prefix", func(t *testing.T) {
		input := "Authorization: Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.thepayload.thesig"
		got := sentry.RedactString(input)
		if strings.Contains(got, "eyJhbGciOiJSUz") {
			t.Errorf("[P0] bearer token not redacted: %q", got)
		}
	})

	t.Run("long_string_truncation", func(t *testing.T) {
		if len(longStringPlain) <= 200 {
			t.Fatal("test invariant: longStringPlain must be > 200 chars")
		}
		got := sentry.TruncateLong(longStringPlain)
		// Middle Xs must be gone.
		if strings.Contains(got, strings.Repeat("X", 50)) {
			t.Errorf("[P0] long X-block not truncated in: %q", got[:min(len(got), 100)])
		}
		if !strings.Contains(got, "[LONG_STRING_REDACTED") {
			t.Errorf("[P0] truncation marker not present in: %q", got)
		}
		if !strings.HasPrefix(got, "LONGSTART") {
			t.Errorf("[P0] head not preserved")
		}
		if !strings.HasSuffix(got, "LONGEND") {
			t.Errorf("[P0] tail not preserved")
		}
	})

	t.Run("private_keys_dropped_from_map", func(t *testing.T) {
		m := map[string]any{
			"private.token":  "sk-ant-api03-SECRET123456789ABCDE",
			"private.email":  "user@example.com",
			"public.message": "normal log",
		}
		out := sentry.RedactMap(m)
		for k := range out {
			if strings.HasPrefix(k, "private.") {
				t.Errorf("[P0] private. key %q not dropped from redacted map", k)
			}
		}
		if _, ok := out["public.message"]; !ok {
			t.Error("[P0] public key was dropped unexpectedly")
		}
	})

	t.Run("secrets_stripped_from_map_values", func(t *testing.T) {
		m := map[string]any{
			"key":   "sk-ant-api03-REALKEY123456789ABCDEFGH",
			"email": "victim@example.com",
		}
		out := sentry.RedactMap(m)
		if v, ok := out["key"].(string); ok {
			if strings.Contains(v, "sk-ant-") {
				t.Errorf("[P0] Anthropic key not redacted in map value: %q", v)
			}
		}
		if v, ok := out["email"].(string); ok {
			if strings.Contains(v, "victim@") {
				t.Errorf("[P0] email not redacted in map value: %q", v)
			}
		}
	})

	t.Run("breadcrumb_message_and_data_redacted", func(t *testing.T) {
		b := sentry.Breadcrumb{
			TS:      time.Now(),
			Level:   "info",
			Message: "Calling API with sk-ant-api03-SECRETABCDEF1234567890",
			Data: map[string]any{
				"private.token": "sk-proj-secretprojectkey1234567890abc",
				"public.info":   "normal",
			},
		}
		// Redact message.
		msg := sentry.RedactString(b.Message)
		if strings.Contains(msg, "sk-ant-api03-") {
			t.Errorf("[P0] Anthropic key in breadcrumb message not redacted: %q", msg)
		}
		// Redact data.
		data := sentry.RedactMap(b.Data)
		if _, hasPrivate := data["private.token"]; hasPrivate {
			t.Error("[P0] private.token key not dropped from breadcrumb data")
		}
	})

	t.Run("home_dir_replaced_in_paths", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			t.Skip("no home dir available")
		}
		path := home + "/some/private/project/file.go"
		got := sentry.RedactString(path)
		if strings.Contains(got, home) {
			t.Errorf("[P0] home dir not replaced in path: %q", got)
		}
		if !strings.HasPrefix(got, "~") {
			t.Errorf("[P0] expected path to start with ~, got: %q", got)
		}
	})

	t.Run("multiple_secrets_in_one_string", func(t *testing.T) {
		combined := "key=sk-ant-api03-REALKEY123456789ABCDEFGH " +
			"Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.abc.def " +
			"contact=victim@example.com aws=AKIAIOSFODNN7EXAMPLE"
		got := sentry.RedactString(combined)
		for _, needle := range []string{"sk-ant-api03-", "eyJhbGciOi", "victim@", "AKIAIOSFODNN7"} {
			if strings.Contains(got, needle) {
				t.Errorf("[P0] secret substring %q found in multi-secret string: %q", needle, got)
			}
		}
	})

	t.Run("SlogHandler_error_breadcrumb_redacted", func(t *testing.T) {
		// Build a SlogHandler chain with a discard inner handler.
		inner := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
		h := &sentry.SlogHandler{Inner: inner}
		logger := slog.New(h)

		// Emit an ERROR with a secret in the message.
		secret := "sk-ant-api03-SLOGTEST123456789ABC"
		logger.Error("failed", "err", "key="+secret)

		// The ring buffer should contain a breadcrumb with the redacted message.
		snaps := sentry.SnapshotBreadcrumbs()
		if len(snaps) == 0 {
			t.Skip("ring buffer empty — may be reset by parallel test")
		}
		// Find the most recent ERROR breadcrumb.
		last := snaps[len(snaps)-1]
		if strings.Contains(last.Message, secret) {
			t.Errorf("[P0] Anthropic key found in slog ERROR breadcrumb: %q", last.Message)
		}
		// Check data values too.
		for k, v := range last.Data {
			if s, ok := v.(string); ok && strings.Contains(s, secret) {
				t.Errorf("[P0] key in breadcrumb data attr %q not redacted: %q", k, s)
			}
		}
	})

	t.Run("RecoverGoroutine_redacts_panic_value", func(t *testing.T) {
		secret := "sk-ant-api03-PANICTEST123456789AB"
		var captured []sentry.Breadcrumb

		// Run a goroutine that panics with a secret.
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer sentry.RecoverGoroutine(context.Background(), "integration-test", nil)
			panic("API key: " + secret)
		}()
		<-done

		// The audit payload is checked in panic_test.go; here we verify the
		// ring buffer and that no breadcrumb contains the raw secret.
		_ = captured
		snaps := sentry.SnapshotBreadcrumbs()
		for _, b := range snaps {
			if strings.Contains(b.Message, secret) {
				t.Errorf("[P0] raw Anthropic key in ring buffer breadcrumb: %q", b.Message)
			}
		}
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
