package export_test

// Adversarial redaction coverage for the session export
// (fix/export-redact, 2026-08-16).
//
// WHAT THESE TESTS ARE FOR. Before this change the export's credential
// scanner walked exactly three strings per message — `Content`, each
// `ToolCall.Result`, and each top-level string in `ToolCall.Arguments` —
// and nothing at all in the session row. Reproduced against 146d9e54
// with a throwaway probe, these reached both files verbatim:
//
//   - a key pasted into the session TITLE (markdown H1, JSON
//     `session.name`, AND the filename offered to the save dialog);
//   - a credential in the SYSTEM PROMPT (JSON `session.system_prompt`);
//   - an attachment's `original_name` (JSON) and `uri` (markdown);
//   - the tool NAME;
//   - `{"aws_secret_access_key": "wJalr…"}` inside a tool RESULT — the
//     old key-name matcher required `name<colon>value` with nothing
//     between, so the closing quote of a JSON key defeated it. Every
//     structured tool result in this app is JSON.
//
// ASSERTIONS ARE ON THE EXPORTED BYTES. A redacted intermediate struct
// proves nothing about a file; the renderers read fields the scanner did
// not walk, which is how four of the five leaks above happened.
//
// FIXTURES DRIVE REAL SQLITE. `session.NewMemoryStore()` skips SQL
// encode/decode, and `ToolCall.Arguments` is a `map[string]any` that only
// becomes its real shape (nested `map[string]any` / `[]any` / `float64`,
// never the Go types a test literal produces) after a JSON round trip
// through the `tool_calls` column.
//
// MUTATION EVIDENCE — each RUN against a deliberately broken tree,
// `go test ./core/sessions/export/ -count=1`, results as observed:
//
//   - redact.go: `allMatchers` = `builtinMatchers` only, dropping the
//     seven key-name matchers → TestExport_SecretNamedKeyRedactsValue-
//     ThatMatchesNoPattern and TestExport_AdversarialFixture_Nothing-
//     ReachesEitherFile both FAIL.
//   - export.go: drop `sess = redactRecord(sess)` →
//     TestExport_SessionRowIsScanned and
//     TestExport_AdversarialFixture_NothingReachesEitherFile FAIL.
//   - export.go: drop `RedactValue` from `DefaultFilename` →
//     TestExport_FilenameDoesNotCarryTheSecret FAILS.
//   - redact.go: disable the ContentBlocks scan in `redactMessages` →
//     TestExport_AttachmentMetadataIsScanned,
//     TestExport_AdversarialFixture_NothingReachesEitherFile and
//     TestRedactMessages_DoesNotMutateTheCallersMessages FAIL.
//
// A NEGATIVE RESULT worth recording, because it changes what these tests
// can honestly claim. Reverting `redactMessages` to the old shallow
// argument loop — no key scan, no walk — leaves EVERY test in this file
// green. TestExport_ArgumentKeysAreScanned survives because argument
// keys are scanned a SECOND time by `argsSummaryFromValues` (moves.go),
// which is the only code that prints them. Removing that scan alone also
// leaves this file green. Removing BOTH fails
// TestExport_ArgumentKeysAreScanned, TestExport_RedactsArgumentNames and
// TestExport_AdversarialFixture_NothingReachesEitherFile.
//
// So the key scan is genuinely redundant on the byte path, and the
// argument-VALUE walk has no byte path at all (v0.63.0 stopped printing
// values). Both are pinned one level down, in redact_internal_test.go,
// which states the same thing rather than pretending a bytes assertion
// covers it.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/sessions/export"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// The adversarial payload. Every constant is a distinct needle so a
// failure names exactly which shape escaped.
const (
	secTitle       = "sk-ant-api03-INTITLE0123456789abcdefghijkl"
	secSysPrompt   = "sk-ant-api03-INSYSPROMPT0123456789abcdefg"
	secContent     = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	secDeep3       = "sk-ant-api03-DEEP3LEVELS0123456789abcdefg"
	secInArray     = "sk-ant-api03-INSIDEARRAY0123456789abcdef"
	secArrInObj    = "sk-ant-api03-ARRAYINOBJECT0123456789abcd"
	secAsKey       = "sk-ant-api03-USEDASMAPKEY0123456789abcde"
	secResultKey   = "sk-ant-api03-INTOOLRESULT0123456789abcde"
	secAttachName  = "sk-ant-api03-ATTACHNAME0123456789abcdefg"
	secAttachURI   = "sk-ant-api03-ATTACHURI0123456789abcdefgh"
	secToolName    = "sk-ant-api03-TOOLNAME0123456789abcdefghi"
	secOpaqueValue = "opaque-session-value-not-key-shaped"
	secAWSInJSON   = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

// allNeedles is what must not appear in either exported file.
func allNeedles() map[string]string {
	return map[string]string{
		"session title":                  secTitle,
		"system prompt":                  secSysPrompt,
		"message content":                secContent,
		"argument nested three deep":     secDeep3,
		"argument inside an array":       secInArray,
		"array inside an object":         secArrInObj,
		"argument used as a map key":     secAsKey,
		"tool result":                    secResultKey,
		"attachment original_name":       secAttachName,
		"attachment uri":                 secAttachURI,
		"tool name":                      secToolName,
		"value under a secret-named key": secOpaqueValue,
		"aws secret in JSON result":      secAWSInJSON,
	}
}

// newLeakFixture persists the adversarial session through real sqlite and
// returns the reloaded record and rows.
func newLeakFixture(t *testing.T) (session.Record, []session.Message) {
	t.Helper()
	ctx := context.Background()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(ctx) })

	mgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))

	rec, err := mgr.Create(ctx, "Deploy notes "+secTitle)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mgr.SetSystemPrompt(ctx, rec.ID,
		"You are a deploy bot. Use "+secSysPrompt+" for the API.",
		session.ContextKindSystem); err != nil {
		t.Fatalf("SetSystemPrompt: %v", err)
	}

	if _, err := mgr.AppendMessage(ctx, rec.ID, session.Message{
		Role:    session.RoleUser,
		Content: "Push it with " + secContent + " please.",
		ContentBlocks: []llm.ContentBlock{{
			Type: "document",
			Source: &llm.MediaSource{
				Kind:         "base64",
				MediaType:    "application/pdf",
				Data:         "JVBERi0xLjQK",
				OriginalName: secAttachName + ".pdf",
			},
		}, {
			Type: "image",
			Source: &llm.MediaSource{
				Kind:      "uri",
				MediaType: "image/png",
				URI:       "https://cdn.example.com/x.png?sig=" + secAttachURI,
			},
		}},
	}); err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}

	// The tool result is JSON, which is what a real HTTP tool returns and
	// what defeated the pre-fix key-name matcher.
	result := `{"status":200,` +
		`"headers":{"authorization":"` + secOpaqueValue + `",` +
		`"x-api-key":"` + secResultKey + `"},` +
		`"creds":{"aws_secret_access_key":"` + secAWSInJSON + `"},` +
		`"note":"deploy finished"}`

	if _, err := mgr.AppendMessage(ctx, rec.ID, session.Message{
		Role:    session.RoleAssistant,
		Content: "Calling the deploy API.",
		ToolCalls: []session.ToolCall{{
			ID: "tc-1",
			// The tool name is chosen by an MCP server, and it reaches
			// both documents verbatim (`<code>%s</code>` / `name`).
			// NOTE the shape: `secToolName` starts the string rather than
			// following an underscore, because `\b` finds no boundary
			// between `_` and `s` — a secret glued to a prefix with an
			// underscore is a known, unfixed hole, recorded in
			// docs/unwired-ledger.md rather than papered over here.
			Name: secToolName + "-deploy",
			Arguments: map[string]any{
				"url":    "https://api.example.com/deploy",
				"a":      map[string]any{"b": map[string]any{"c": secDeep3}},
				"body":   []any{"ok", secInArray, 42},
				"outer":  map[string]any{"list": []any{map[string]any{"k": secArrInObj}}},
				secAsKey: "value-sitting-under-a-secret-key",
				"headers": map[string]any{
					"authorization": secOpaqueValue,
				},
			},
			Result: result,
		}},
	}); err != nil {
		t.Fatalf("AppendMessage(assistant): %v", err)
	}

	reloaded, err := mgr.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	msgs, err := mgr.ListMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	return reloaded, msgs
}

// renderBoth returns the exported bytes of both formats.
func renderBoth(t *testing.T, rec session.Record, msgs []session.Message) (md, js string) {
	t.Helper()
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	mdB, _, err := export.Render(export.FormatMarkdown, rec, msgs, now)
	if err != nil {
		t.Fatalf("Render markdown: %v", err)
	}
	jsB, _, err := export.Render(export.FormatJSON, rec, msgs, now)
	if err != nil {
		t.Fatalf("Render json: %v", err)
	}
	// The JSON export must also be parseable — a redaction that produced
	// invalid JSON would "pass" a substring search and break every reader.
	var probe map[string]any
	if uerr := json.Unmarshal(jsB, &probe); uerr != nil {
		t.Fatalf("JSON export is not valid JSON after redaction: %v", uerr)
	}
	return string(mdB), string(jsB)
}

// TestExport_AdversarialFixture_NothingReachesEitherFile is the headline
// assertion: every needle, every shape, both files.
func TestExport_AdversarialFixture_NothingReachesEitherFile(t *testing.T) {
	t.Parallel()
	rec, msgs := newLeakFixture(t)
	md, js := renderBoth(t, rec, msgs)

	for name, needle := range allNeedles() {
		if strings.Contains(md, needle) {
			t.Errorf("MARKDOWN export leaks %s (%q)", name, needle)
		}
		// JSON escapes `/` never and `<` as <, so a raw substring
		// search is the right check for the needles; none contain a
		// character encoding/json rewrites except `/` in the AWS key,
		// which Go emits verbatim.
		if strings.Contains(js, needle) {
			t.Errorf("JSON export leaks %s (%q)", name, needle)
		}
	}
	if !strings.Contains(md, "<REDACTED:") {
		t.Error("markdown export carries no redaction marker at all — did anything render?")
	}
}

// TestExport_SecretNamedKeyRedactsValueThatMatchesNoPattern pins the rule
// that a key can NAME a secret. `secOpaqueValue` matches no credential
// pattern; the only reason it must not be exported is that it sits under
// `authorization`.
func TestExport_SecretNamedKeyRedactsValueThatMatchesNoPattern(t *testing.T) {
	t.Parallel()
	rec, msgs := newLeakFixture(t)
	md, js := renderBoth(t, rec, msgs)

	if strings.Contains(md, secOpaqueValue) {
		t.Errorf("markdown carries the value under `authorization` verbatim")
	}
	if strings.Contains(js, secOpaqueValue) {
		t.Errorf("JSON carries the value under `authorization` verbatim")
	}
	if !strings.Contains(js, "REDACTED:secret-named-key") {
		t.Errorf("expected a secret-named-key marker in the JSON export:\n%s", js)
	}
	// The surrounding structure must survive — this is a redaction, not a
	// deletion, and a reader still needs to see that a call was made.
	if !strings.Contains(js, "deploy finished") {
		t.Errorf("the non-secret part of the tool result was destroyed:\n%s", js)
	}
}

// TestExport_ArgumentKeysAreScanned pins the other half of "keys matter":
// a key that IS a credential. The names-and-types summary prints argument
// names, so an unscanned key is a printed key.
func TestExport_ArgumentKeysAreScanned(t *testing.T) {
	t.Parallel()
	rec, msgs := newLeakFixture(t)
	md, js := renderBoth(t, rec, msgs)

	if strings.Contains(md, secAsKey) {
		t.Errorf("markdown args summary carries the raw secret KEY")
	}
	if strings.Contains(js, secAsKey) {
		t.Errorf("JSON args_summary carries the raw secret KEY")
	}
	// The summary still lists the argument, redacted, with its type.
	if !strings.Contains(js, "REDACTED:anthropic-key") {
		t.Errorf("the redacted key is missing from the summary entirely:\n%s", js)
	}
}

// TestExport_SessionRowIsScanned pins the session record, which nothing
// scanned before 2026-08-16.
func TestExport_SessionRowIsScanned(t *testing.T) {
	t.Parallel()
	rec, msgs := newLeakFixture(t)
	md, js := renderBoth(t, rec, msgs)

	if strings.Contains(md, secTitle) {
		t.Error("markdown H1 carries the credential in the session title")
	}
	if strings.Contains(js, secTitle) {
		t.Error("JSON session.name carries the credential in the session title")
	}
	if strings.Contains(js, secSysPrompt) {
		t.Error("JSON session.system_prompt carries a credential")
	}
	// The readable half of the title survives.
	if !strings.Contains(md, "Deploy notes") {
		t.Errorf("the whole title was destroyed rather than the secret in it:\n%s", md)
	}
}

// TestExport_FilenameDoesNotCarryTheSecret pins DefaultFilename, whose
// only production caller hands it the raw session name.
func TestExport_FilenameDoesNotCarryTheSecret(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	name := export.DefaultFilename("Deploy notes "+secTitle, export.FormatMarkdown, now)
	// sanitiseTitle strips `-` never but drops `:`/`<`/`>`, so compare
	// against the alphanumeric core of the secret, which is what would
	// survive slugification.
	if strings.Contains(strings.ToLower(name), "intitle0123456789") {
		t.Errorf("suggested filename carries the credential: %q", name)
	}
	if !strings.HasPrefix(name, "deploy-notes") {
		t.Errorf("filename lost its readable prefix: %q", name)
	}
}

// TestExport_AttachmentMetadataIsScanned pins the attachment fields: the
// URI reaches markdown, the original name reaches JSON.
func TestExport_AttachmentMetadataIsScanned(t *testing.T) {
	t.Parallel()
	rec, msgs := newLeakFixture(t)
	md, js := renderBoth(t, rec, msgs)

	if strings.Contains(md, secAttachURI) {
		t.Error("markdown attachment link carries a presigned-URL credential")
	}
	if strings.Contains(js, secAttachName) {
		t.Error("JSON artifact name carries a credential")
	}
	// Redacting metadata must not drop the attachment.
	if !strings.Contains(js, "data_base64") {
		t.Errorf("attachment bytes disappeared from the JSON export:\n%s", js)
	}
}

// TestExport_OrdinaryContentSurvivesIntact is the over-redaction
// regression. Redacting everything passes every leak test above and is a
// terrible fix; this pins the ordinary case byte for byte.
//
// The strings here are the ones a widened scanner is most likely to eat:
// a `key: value` config line, a table of field names, a URL, prose about
// tokens, and a Go signature.
func TestExport_OrdinaryContentSurvivesIntact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(ctx) })
	mgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	rec, err := mgr.Create(ctx, "Refactor the token budget")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ordinary := []string{
		"The config key is `max_tokens: 4096` in settings.yaml.",
		"error: token expired, please re-authenticate",
		"See https://github.com/kameas-ai/kenaz-harness/blob/main/core/rpc/api.go",
		"func RedactValue(s string) (string, []RedactionMatch)",
		"| field | type |\n|---|---|\n| id | string |\n| token_count | number |",
		"Run `go test ./core/... -race -count=1 -short` before pushing.",
		"secret: none",
		"Connect with psql -h localhost -p 5432 -U postgres app_db",
		"The password field is empty; ask the user to set one.",
	}
	for _, line := range ordinary {
		if _, aerr := mgr.AppendMessage(ctx, rec.ID, session.Message{
			Role: session.RoleUser, Content: line,
		}); aerr != nil {
			t.Fatalf("AppendMessage: %v", aerr)
		}
	}
	msgs, err := mgr.ListMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	rec, err = mgr.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	md, js := renderBoth(t, rec, msgs)

	for _, line := range ordinary {
		if !strings.Contains(md, line) {
			t.Errorf("OVER-REDACTION: markdown mangled ordinary content %q\n---\n%s", line, md)
		}
	}
	if strings.Contains(md, "<REDACTED:") {
		t.Errorf("OVER-REDACTION: an export of entirely ordinary content carries a marker:\n%s", md)
	}
	if strings.Contains(js, "REDACTED:") {
		t.Errorf("OVER-REDACTION: JSON export of ordinary content carries a marker:\n%s", js)
	}
	if !strings.Contains(md, "Refactor the token budget") {
		t.Errorf("OVER-REDACTION: an ordinary session title was rewritten:\n%s", md)
	}
}

// TestExport_HostileStringsDoNotBreakTheRenderer covers the two inputs
// that break a scanner rather than defeat it: a very long string and
// invalid UTF-8. Built in memory rather than through sqlite deliberately
// — the TEXT column's own handling of invalid UTF-8 is the storage
// layer's contract, and what is under test here is that the scanner
// neither panics nor drops the credential sitting next to the bad bytes.
func TestExport_HostileStringsDoNotBreakTheRenderer(t *testing.T) {
	t.Parallel()
	const needle = "sk-ant-api03-NEXTTOBADBYTES0123456789abc"
	long := strings.Repeat("lorem ipsum dolor sit amet ", 40000) // ~1 MB
	bad := "prefix \xff\xfe\x00 " + needle + " \xc3\x28 suffix"

	sess := session.Record{
		ID: "s-hostile", Name: "hostile", ContextKind: "system",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	msgs := []session.Message{
		{ID: "m1", SessionID: "s-hostile", Sequence: 1, Role: session.RoleUser, Content: long},
		{ID: "m2", SessionID: "s-hostile", Sequence: 2, Role: session.RoleUser, Content: bad},
		{ID: "m3", SessionID: "s-hostile", Sequence: 3, Role: session.RoleAssistant,
			ToolCalls: []session.ToolCall{{ID: "t", Name: "x", Result: bad}}},
	}
	md, js := renderBoth(t, sess, msgs)
	if strings.Contains(md, needle) {
		t.Error("markdown leaked the credential adjacent to invalid UTF-8")
	}
	if strings.Contains(js, needle) {
		t.Error("JSON leaked the credential adjacent to invalid UTF-8")
	}
	if !strings.Contains(md, "lorem ipsum") {
		t.Error("the 1 MB ordinary string did not survive the scan")
	}
}
