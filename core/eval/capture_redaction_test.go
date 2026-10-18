package eval_test

// Coverage for docs/escalation-register-2026-08-19.md G-3: eval capture
// used to run its own two-pattern (sk-, Bearer) credential scanner instead
// of consuming the shared core/sessions/export catalog, and its package
// doc claimed parity with the event log's redaction that did not exist.
//
// Every test here asserts on BYTES WRITTEN TO DISK — read the capture
// file back and check the plaintext secret is absent — never merely that
// a redaction function was invoked. The premise is specifically "a
// credential at rest on the user's disk," so anything less proves
// nothing.
//
// All secret fixtures are synthetic (sk-FAKE..., AKIAFAKE..., etc.):
// shaped correctly to trip the regex the test targets, never a real
// credential.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/eval"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// catalogFixture pairs a core/sessions/export matcher's profile id with a
// secret-shaped fixture engineered to match it, and the exact plaintext
// substring that must never survive to a capture file.
type catalogFixture struct {
	profileID string
	secret    string // the plaintext that MUST NOT reach disk
	haystack  string // the text handed to the capture pipeline (System field)
}

// catalogFixtures covers every provider-shape matcher in
// core/sessions/export/redact.go's builtinMatchers (the "widened export
// catalog" G-3 names as the single source). Two matchers that used to be
// the ENTIRE two-pattern local scanner — anthropic-key and bearer-token —
// are included so this table also re-proves the cases that already
// worked before the fix, not just the newly-covered ones.
func catalogFixtures() []catalogFixture {
	return []catalogFixture{
		{
			profileID: "aws-access-key-id",
			secret:    "AKIA" + strings.Repeat("F", 16),
			haystack:  "aws key: " + "AKIA" + strings.Repeat("F", 16) + " do not share",
		},
		{
			profileID: "aws-secret-key",
			secret:    strings.Repeat("A", 40),
			haystack:  "aws_secret_access_key=" + strings.Repeat("A", 40),
		},
		{
			profileID: "jwt",
			// The textbook jwt.io example token — a public, non-functioning
			// demo credential, shaped exactly like a real JWT.
			secret:   "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			haystack: "token: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		},
		{
			profileID: "bearer-token",
			secret:    strings.Repeat("x", 30),
			haystack:  "Authorization: Bearer " + strings.Repeat("x", 30),
		},
		{
			profileID: "basic-auth",
			secret:    strings.Repeat("Y", 20),
			haystack:  "Authorization: Basic " + strings.Repeat("Y", 20),
		},
		{
			profileID: "pem-block",
			secret:    "FAKEPEMBODYFAKEPEMBODYFAKE",
			haystack:  "-----BEGIN RSA PRIVATE KEY-----\nFAKEPEMBODYFAKEPEMBODYFAKE\n-----END RSA PRIVATE KEY-----",
		},
		{
			profileID: "github-token",
			secret:    "ghp_" + strings.Repeat("a", 36),
			haystack:  "token=ghp_" + strings.Repeat("a", 36),
		},
		{
			profileID: "anthropic-key",
			secret:    "sk-ant-" + strings.Repeat("F", 25),
			haystack:  "key is sk-ant-" + strings.Repeat("F", 25) + " keep secret",
		},
		{
			profileID: "openai-key-bare",
			secret:    "sk-" + strings.Repeat("q", 25),
			haystack:  "key is sk-" + strings.Repeat("q", 25) + " keep secret",
		},
		{
			profileID: "openai-key-proj",
			secret:    "sk-proj-" + strings.Repeat("m", 25),
			haystack:  "key is sk-proj-" + strings.Repeat("m", 25) + " keep secret",
		},
		{
			profileID: "google-api-key",
			secret:    "AIza" + strings.Repeat("G", 35),
			haystack:  "google key: AIza" + strings.Repeat("G", 35),
		},
		{
			profileID: "slack-token",
			secret:    "xoxb-" + strings.Repeat("1", 15),
			haystack:  "slack token xoxb-" + strings.Repeat("1", 15),
		},
		{
			profileID: "stripe-key",
			secret:    "sk_live_" + strings.Repeat("Z", 20),
			haystack:  "stripe key sk_live_" + strings.Repeat("Z", 20),
		},
		{
			profileID: "connection-string-password",
			secret:    "hunter2pass",
			haystack:  "postgres://dbuser:hunter2pass@dbhost:5432/app",
		},
	}
}

// TestCaptureRedaction_CatalogPatterns is the table test over the shared
// catalog's provider-shape matchers. Before this fix, every case except
// "anthropic-key"/"openai-key-bare"/"bearer-token" would have failed —
// the local redactString handled exactly two prefixes (sk-, Bearer) and
// nothing else. See TestCaptureRedaction_ProvesRedTest for the actual red
// run against the pre-fix scanner.
func TestCaptureRedaction_CatalogPatterns(t *testing.T) {
	for _, f := range catalogFixtures() {
		f := f
		t.Run(f.profileID, func(t *testing.T) {
			dir := t.TempDir()
			captureDir := filepath.Join(dir, "eval-captures")
			rec := eval.NewRecorder(captureDir, "test-build")
			ctx := context.Background()
			sid := "sess-" + f.profileID

			if err := rec.StartCapture(ctx, sid); err != nil {
				t.Fatalf("StartCapture: %v", err)
			}
			req := corellm.GenerationRequest{
				ProfileID: "p",
				System:    f.haystack,
				Messages: []corellm.Message{
					{Role: corellm.RoleUser, Content: []corellm.ContentBlock{{Type: "text", Text: "hi"}}},
				},
			}
			rec.AppendLLMRequest(sid, req)
			if err := rec.StopCapture(ctx, sid); err != nil {
				t.Fatalf("StopCapture: %v", err)
			}

			path := filepath.Join(captureDir, sid+".jsonl")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read capture file: %v", err)
			}
			if bytes.Contains(raw, []byte(f.secret)) {
				t.Errorf("capture file for matcher %q contains plaintext secret %q\nfile contents:\n%s",
					f.profileID, f.secret, raw)
			}
			// Sanity: the file isn't just empty/truncated — benign
			// surrounding text should still be present.
			if !bytes.Contains(raw, []byte("hi")) {
				t.Errorf("capture file missing benign content %q — over-redaction or write failure, not just a pass",
					"hi")
			}
		})
	}
}

// TestCaptureRedaction_KeyNameScanning is the class the two-pattern
// scanner could never see at all: a secret sitting under a field NAMED
// like a credential ("password") whose VALUE matches no known provider
// shape. Only core/sessions/export.RedactStructured's key-name-forced
// redaction (secretNamingKeyRe) catches this — RedactValue's flat text
// scan cannot, because there is no recognisable pattern in the value
// itself.
func TestCaptureRedaction_KeyNameScanning(t *testing.T) {
	dir := t.TempDir()
	captureDir := filepath.Join(dir, "eval-captures")
	rec := eval.NewRecorder(captureDir, "test-build")
	ctx := context.Background()
	const sid = "sess-keyname"

	if err := rec.StartCapture(ctx, sid); err != nil {
		t.Fatalf("StartCapture: %v", err)
	}

	const secretValue = "not-a-known-secret-shape-482"
	args, err := json.Marshal(map[string]string{
		"password": secretValue,
		"note":     "benign field survives",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.AppendToolCall(sid, eval.ToolCallEntry{
		ID:   "call-1",
		Name: "some_tool",
		Args: args,
	})
	if err := rec.StopCapture(ctx, sid); err != nil {
		t.Fatalf("StopCapture: %v", err)
	}

	path := filepath.Join(captureDir, sid+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secretValue)) {
		t.Errorf("capture file contains the plaintext value of a field NAMED %q — key-name scanning did not fire\nfile contents:\n%s",
			"password", raw)
	}
	if !bytes.Contains(raw, []byte("benign field survives")) {
		t.Error("capture file missing unrelated benign field — over-redaction, not a real pass")
	}
}

// TestCaptureRedaction_ToolCallArgsAndResult proves ToolCallEntry.Args and
// .Result — previously written to disk with ZERO redaction, since
// AppendToolCall never called redactString at all — are now scrubbed.
func TestCaptureRedaction_ToolCallArgsAndResult(t *testing.T) {
	dir := t.TempDir()
	captureDir := filepath.Join(dir, "eval-captures")
	rec := eval.NewRecorder(captureDir, "test-build")
	ctx := context.Background()
	const sid = "sess-toolcall"

	if err := rec.StartCapture(ctx, sid); err != nil {
		t.Fatalf("StartCapture: %v", err)
	}

	secretKey := "sk-ant-" + strings.Repeat("Q", 25)
	args, _ := json.Marshal(map[string]string{"cmd": "curl -H 'x-api-key: " + secretKey + "'"})
	result, _ := json.Marshal(map[string]string{"stdout": "used key " + secretKey})
	rec.AppendToolCall(sid, eval.ToolCallEntry{
		ID:     "call-2",
		Name:   "bash",
		Args:   args,
		Result: result,
	})
	if err := rec.StopCapture(ctx, sid); err != nil {
		t.Fatalf("StopCapture: %v", err)
	}

	path := filepath.Join(captureDir, sid+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secretKey)) {
		t.Errorf("tool call args/result written with plaintext credential (previously had ZERO redaction)\nfile contents:\n%s", raw)
	}
}

// TestCaptureRedaction_LLMResponseContent proves LLMResponseEntry.Content
// — previously written to disk with ZERO redaction, since
// AppendLLMResponse marshalled resp.Content directly — is now scrubbed,
// including a tool_use block's Input (the key-name-scanning class again,
// this time on the response side).
func TestCaptureRedaction_LLMResponseContent(t *testing.T) {
	dir := t.TempDir()
	captureDir := filepath.Join(dir, "eval-captures")
	rec := eval.NewRecorder(captureDir, "test-build")
	ctx := context.Background()
	const sid = "sess-response"

	if err := rec.StartCapture(ctx, sid); err != nil {
		t.Fatalf("StartCapture: %v", err)
	}

	secretKey := "sk-" + strings.Repeat("R", 25)
	const toolInputSecret = "not-a-known-secret-shape-999"
	rec.AppendLLMResponse(sid, "fp-1", corellm.Response{
		Content: []corellm.ContentBlock{
			{Type: "text", Text: "here is the key: " + secretKey},
			{
				Type: "tool_use",
				ToolUse: &corellm.ToolUse{
					ID:    "tu-1",
					Name:  "run",
					Input: json.RawMessage(`{"token":"` + toolInputSecret + `"}`),
				},
			},
		},
		FinishReason: "end_turn",
	})
	if err := rec.StopCapture(ctx, sid); err != nil {
		t.Fatalf("StopCapture: %v", err)
	}

	path := filepath.Join(captureDir, sid+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secretKey)) {
		t.Errorf("LLM response text written with plaintext credential (previously had ZERO redaction)\nfile contents:\n%s", raw)
	}
	if bytes.Contains(raw, []byte(toolInputSecret)) {
		t.Errorf("LLM response tool_use.input written with plaintext key-named secret\nfile contents:\n%s", raw)
	}
}
