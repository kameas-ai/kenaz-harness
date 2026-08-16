package export_test

// Performance guard for the redaction pass.
//
// A deep walk plus a wider catalog is only acceptable if exporting a large
// session stays fast. These render a 400-message session carrying ~3.8 MB
// of content and tool output — larger than any real transcript this app
// produces, since `capToolOutput` caps each tool result at 4000 runes.
//
// Measured on darwin/arm64 (Apple M5 Pro), `-benchtime=20x`, markdown:
//
//	146d9e54 (10 matchers, shallow walk, no anchors)  556 ms/op
//	this change, transcript with no anchor words      485 ms/op
//	this change, transcript containing ALL 7 anchors  973 ms/op
//
// The typical case is FASTER than the code it replaces despite scanning
// for eleven more credential shapes, because of two changes in
// RedactValue: the `anchor` prefilter (see redact.go) skips a matcher
// whose literal is absent, and a `FindStringIndex` guard stops
// `ReplaceAllStringFunc` from copying the whole string once per matcher
// when nothing matched.
//
// The all-anchors case — a session ABOUT authentication, which is a real
// shape — is 1.75x the old cost. That is the honest number and it is the
// price of catching `{"authorization": "<opaque>"}`, which the old
// catalog could not see at all. Cost is linear in bytes scanned and is
// dominated by the string matchers; the structural walk over argument
// maps does not appear in the profile.

import (
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/sessions/export"
)

func buildLargeSession() (session.Record, []session.Message) {
	sess := session.Record{
		ID:           "sess-bench",
		Name:         "Large benchmark session",
		SystemPrompt: strings.Repeat("You are a helpful assistant. ", 40),
		ContextKind:  "system",
		CreatedAt:    time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC),
	}
	body := strings.Repeat(
		"The deploy pipeline reads config from core/config/load.go and writes "+
			"artifacts to s3://kameas-ai-prod-releases-use2. See the runbook. ", 60)
	toolOut := strings.Repeat(
		`{"path":"core/rpc/api.go","lines":420,"status":"ok","note":"no findings"} `, 50)

	msgs := make([]session.Message, 0, 400)
	for i := 0; i < 400; i++ {
		m := session.Message{
			ID:        "m",
			SessionID: sess.ID,
			Sequence:  int64(i),
			Role:      session.RoleUser,
			Content:   body,
			CreatedAt: sess.CreatedAt,
		}
		if i%2 == 1 {
			m.Role = session.RoleAssistant
			m.ToolCalls = []session.ToolCall{{
				ID:   "tc",
				Name: "read_file",
				Arguments: map[string]any{
					"path":    "core/rpc/api.go",
					"opts":    map[string]any{"limit": 200, "offset": 0, "flags": []any{"a", "b", "c"}},
					"headers": map[string]any{"accept": "application/json"},
				},
				Result: toolOut,
			}}
		}
		msgs = append(msgs, m)
	}
	return sess, msgs
}

func BenchmarkRender_LargeSession(b *testing.B) {
	sess, msgs := buildLargeSession()
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := export.Render(export.FormatMarkdown, sess, msgs, now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRender_LargeSessionJSON(b *testing.B) {
	sess, msgs := buildLargeSession()
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := export.Render(export.FormatJSON, sess, msgs, now); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRender_LargeSessionAllAnchors is the honest worst case for the
// anchor prefilter: a transcript that mentions every key-name anchor, so
// not one of the seven key-name matchers can be skipped. A session about
// authentication is exactly this shape, which is why it is measured rather
// than assumed.
func BenchmarkRender_LargeSessionAllAnchors(b *testing.B) {
	sess, msgs := buildLargeSession()
	anchors := " We reviewed the authorization header, the cookie jar, the " +
		"api_key rotation, the token budget, the secret store, the password " +
		"policy and the passphrase prompt. "
	for i := range msgs {
		msgs[i].Content += anchors
	}
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := export.Render(export.FormatMarkdown, sess, msgs, now); err != nil {
			b.Fatal(err)
		}
	}
}
