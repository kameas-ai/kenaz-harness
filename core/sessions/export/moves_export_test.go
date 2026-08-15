package export_test

// Export fidelity for move-bearing sessions
// (model-moves-transcript-01PMCH01 WP05, FR-006).
//
// EVERY FIXTURE HERE IS PERSISTED THROUGH THE SQL STORE, not through
// session.NewMemoryStore(). WP03's review found that composition tests
// built on the memory store bypass SQL encode/decode entirely, and four
// separate SQL-path mutations survived the whole suite because of it.
// These assertions are about what an export of PERSISTED rows contains,
// so they have to travel the persisted path — and there is a second
// reason here: the move columns are unexported, so a move-bearing
// session.Message can only be produced by AppendTranscriptEntry writing
// to a store and reading back. A test that could construct one by hand
// would not be testing the export of a real row.
//
// MUTATION EVIDENCE — each recorded against a deliberately broken tree:
//
//   - markdown.go: revert the turn loop to `for idx, m := range msgs`
//     (row-per-heading) →
//     TestExport_Markdown_MoveBearingTurnIsOneTurn fails ("## Turn 6"
//     appears; the trajectory <details> disappears).
//   - json.go: put the trajectory rows into `messages` instead of
//     `moves` → TestExport_JSON_PreMoveConsumerSeesFinalsOnly fails
//     (6 messages, not 2).
//   - json.go: restore `Arguments: tc.Arguments` on jsonToolCall, or
//     markdown.go: restore `formatToolArgs(tc.Arguments)` →
//     TestExport_NeverCarriesRawToolArguments fails on the nested and
//     the array secret.
//   - moves.go: return `s` unchanged from capToolOutput →
//     TestExport_CapsToolOutput fails (both formats carry 40k runes).
//   - moves.go: key the answer off `kind == final` instead of the
//     positional rule → TestExport_InterruptedTurnKeepsItsTrajectory
//     fails (the trajectory is dropped from the document).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/sessions/export"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

var exportNow = time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)

// newSQLManager opens a real sqlite database so the move columns
// (migration 0333) are exercised rather than stubbed.
func newSQLManager(t *testing.T) (*session.Manager, session.Record) {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	mgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	rec, err := mgr.Create(context.Background(), "Export Moves")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return mgr, rec
}

// persistMoveTurn writes one human turn plus the trajectory answering
// it, through the single transcript seam, and returns the reloaded rows.
//
// The trajectory is the spec §1 shape: the model speaks, calls a tool,
// reads a result, speaks again, calls a failing tool, then answers.
func persistMoveTurn(t *testing.T, mgr *session.Manager, sid string,
	entries func(span string) []session.TranscriptEntry) []session.Message {
	t.Helper()
	ctx := context.Background()

	user, err := mgr.AppendMessage(ctx, sid, session.Message{
		Role:    session.RoleUser,
		Content: "Where is the config read?",
	})
	if err != nil {
		t.Fatalf("AppendMessage(user): %v", err)
	}
	for i, e := range entries(user.ID) {
		if _, err := mgr.AppendTranscriptEntry(ctx, sid, e); err != nil {
			t.Fatalf("AppendTranscriptEntry(%d): %v", i, err)
		}
	}
	msgs, err := mgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	return msgs
}

func fullTurn(span string) []session.TranscriptEntry {
	return []session.TranscriptEntry{
		{
			Role: session.RoleAssistant, Content: "Let me read the key files.",
			Kind: session.MoveKindAssistantMove, MoveIndex: 0, TurnSpanID: span,
		},
		{
			Role: session.RoleTool, Content: "path:string",
			Kind: session.MoveKindToolCall, MoveIndex: 1, TurnSpanID: span,
			ToolCalls: []session.ToolCall{{ID: "tc-1", Name: "read_file"}},
			// The MODEL layer's raw arguments. If the export ever grows a
			// reader for this column, the assertions below turn red.
			ModelToolArgs: map[string]string{
				"tc-1": `{"path":"/etc/shadow","token":"sk-ant-api01-abcdefghijklmnopqrstuvwxyz012345"}`,
			},
		},
		{
			Role: session.RoleTool, Content: "alpha\nbeta\ngamma",
			Kind: session.MoveKindToolResult, MoveIndex: 2, TurnSpanID: span,
			ToolCalls: []session.ToolCall{{ID: "tc-1", Name: "read_file"}},
		},
		{
			Role: session.RoleAssistant, Content: "Native tools are blocked; trying bash.",
			Kind: session.MoveKindAssistantMove, MoveIndex: 3, TurnSpanID: span,
		},
		{
			Role: session.RoleTool, Content: "cmd:string",
			Kind: session.MoveKindToolCall, MoveIndex: 4, TurnSpanID: span,
			ToolCalls: []session.ToolCall{{ID: "tc-2", Name: "bash"}},
		},
		{
			Role: session.RoleTool, Content: "permission denied",
			Kind: session.MoveKindToolResult, MoveIndex: 5, TurnSpanID: span,
			ToolCalls: []session.ToolCall{{ID: "tc-2", Name: "bash", IsError: true}},
		},
		{
			Role: session.RoleAssistant, Content: "The config is read in core/config/load.go.",
			Kind: session.MoveKindFinal, MoveIndex: 6, TurnSpanID: span,
		},
	}
}

// ---------------------------------------------------------------------
// FR-006 — the document is turns, and it carries the moves.
// ---------------------------------------------------------------------

func TestExport_Markdown_MoveBearingTurnIsOneTurn(t *testing.T) {
	t.Parallel()
	mgr, rec := newSQLManager(t)
	msgs := persistMoveTurn(t, mgr, rec.ID, fullTurn)
	if len(msgs) != 8 {
		t.Fatalf("persisted rows = %d, want 8 (1 user + 7 moves)", len(msgs))
	}

	b, _, err := export.Render(export.FormatMarkdown, rec, msgs, exportNow)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(b)

	// Two turn headings for one exchange: the question and the answer.
	// Before WP05 this document had eight, one per row.
	if got := strings.Count(s, "\n## Turn "); got != 2 {
		t.Errorf("turn headings = %d, want 2:\n%s", got, s)
	}
	if strings.Contains(s, "## Turn 3") {
		t.Errorf("move rows took turn headings of their own:\n%s", s)
	}
	if !strings.Contains(s, "## Turn 2 — Assistant") {
		t.Errorf("answer is not the second turn:\n%s", s)
	}
	if !strings.Contains(s, "The config is read in core/config/load.go.") {
		t.Errorf("answer missing from the document:\n%s", s)
	}

	// The trajectory is carried, folded.
	if !strings.Contains(s, "<summary>Trajectory — 6 moves</summary>") {
		t.Errorf("trajectory disclosure missing:\n%s", s)
	}
	for _, want := range []string{
		"Let me read the key files.",
		"Native tools are blocked; trying bash.",
		"**2. Tool call** `read_file` — args: `path:string`",
		"**6. Tool result** `bash` — error",
		"permission denied",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("trajectory missing %q:\n%s", want, s)
		}
	}
}

func TestExport_JSON_PreMoveConsumerSeesFinalsOnly(t *testing.T) {
	t.Parallel()
	mgr, rec := newSQLManager(t)
	msgs := persistMoveTurn(t, mgr, rec.ID, fullTurn)

	b, _, err := export.Render(export.FormatJSON, rec, msgs, exportNow)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// A reader written against export_format_version 1 knows `messages`
	// with role + content and nothing else. Decoding into exactly that
	// shape is what "degrades to the final-answer view" has to mean.
	var legacy struct {
		Meta struct {
			ExportFormatVersion int `json:"export_format_version"`
		} `json:"meta"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &legacy); err != nil {
		t.Fatalf("legacy unmarshal: %v", err)
	}
	if legacy.Meta.ExportFormatVersion != export.ExportFormatVersion {
		t.Errorf("schema version = %d", legacy.Meta.ExportFormatVersion)
	}
	if len(legacy.Messages) != 2 {
		t.Fatalf("a pre-move consumer sees %d messages, want 2 (question + answer)",
			len(legacy.Messages))
	}
	if legacy.Messages[0].Role != "user" ||
		legacy.Messages[0].Content != "Where is the config read?" {
		t.Errorf("messages[0] = %+v", legacy.Messages[0])
	}
	if legacy.Messages[1].Role != "assistant" ||
		legacy.Messages[1].Content != "The config is read in core/config/load.go." {
		t.Errorf("messages[1] = %+v", legacy.Messages[1])
	}

	// And a move-aware consumer sees the whole trajectory.
	var modern struct {
		Messages []struct {
			Kind  string `json:"kind"`
			Moves []struct {
				Kind        string `json:"kind"`
				MoveIndex   int    `json:"move_index"`
				Content     string `json:"content"`
				ToolName    string `json:"tool_name"`
				ArgsSummary string `json:"args_summary"`
				Output      string `json:"output"`
				IsError     bool   `json:"is_error"`
			} `json:"moves"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &modern); err != nil {
		t.Fatalf("modern unmarshal: %v", err)
	}
	if got := modern.Messages[0].Moves; len(got) != 0 {
		t.Errorf("user turn carries %d moves, want 0", len(got))
	}
	answer := modern.Messages[1]
	if answer.Kind != string(session.MoveKindFinal) {
		t.Errorf("answer kind = %q, want final", answer.Kind)
	}
	if len(answer.Moves) != 6 {
		t.Fatalf("answer carries %d moves, want 6", len(answer.Moves))
	}
	if answer.Moves[1].ToolName != "read_file" ||
		answer.Moves[1].ArgsSummary != "path:string" {
		t.Errorf("moves[1] = %+v", answer.Moves[1])
	}
	if answer.Moves[2].Output != "alpha\nbeta\ngamma" {
		t.Errorf("moves[2].output = %q", answer.Moves[2].Output)
	}
	if !answer.Moves[5].IsError {
		t.Errorf("the failing tool result lost its error flag: %+v", answer.Moves[5])
	}
}

// TestExport_ClassicSessionIsUnchangedByMoves pins the HALF of the
// backward-compatibility claim that is actually true, and its name is
// scoped to that half on purpose.
//
// A classic session with no tool calls exports byte-for-byte as it did
// before the mission — verified against the base commit, both formats.
// A classic session WITH tool calls does NOT, and cannot: dropping the
// raw `arguments` block is the security fix, not a side effect. That
// deliberate difference is pinned separately by
// TestExport_ClassicToolCallsLoseTheirRawArguments below, so the two
// facts cannot be confused for one another, and it is why
// ExportFormatVersion went to 2.
func TestExport_ClassicSessionIsUnchangedByMoves(t *testing.T) {
	t.Parallel()
	mgr, rec := newSQLManager(t)
	ctx := context.Background()
	for _, m := range []session.Message{
		{Role: session.RoleUser, Content: "hello"},
		{Role: session.RoleAssistant, Content: "hi there"},
		{Role: session.RoleUser, Content: "and again"},
		{Role: session.RoleAssistant, Content: "still here"},
	} {
		if _, err := mgr.AppendMessage(ctx, rec.ID, m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	msgs, err := mgr.ListMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	md, _, err := export.Render(export.FormatMarkdown, rec, msgs, exportNow)
	if err != nil {
		t.Fatalf("Render markdown: %v", err)
	}
	s := string(md)
	// One heading per row, exactly as before the mission: a classic
	// session has no move rows for the projection to fold.
	for i, want := range []string{
		"## Turn 1 — User", "## Turn 2 — Assistant",
		"## Turn 3 — User", "## Turn 4 — Assistant",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("classic heading %d (%q) missing:\n%s", i+1, want, s)
		}
	}
	if strings.Contains(s, "Trajectory") {
		t.Errorf("classic session grew a trajectory disclosure:\n%s", s)
	}

	jb, _, err := export.Render(export.FormatJSON, rec, msgs, exportNow)
	if err != nil {
		t.Fatalf("Render json: %v", err)
	}
	var raw struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(jb, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw.Messages) != 4 {
		t.Fatalf("classic messages = %d, want 4", len(raw.Messages))
	}
	// omitempty means the move fields are simply absent — the bytes a
	// pre-mission consumer parsed are the bytes it still parses.
	for i, m := range raw.Messages {
		for _, field := range []string{"kind", "turn_span_id", "moves", "trajectory_only"} {
			if _, present := m[field]; present {
				t.Errorf("classic messages[%d] grew a %q field", i, field)
			}
		}
	}
}

// TestExport_ClassicToolCallsLoseTheirRawArguments is the honest other
// half: WP05 is NOT purely additive for a session that predates it.
//
// A classic (move-free) session whose assistant rows carry tool calls
// exported `**Arguments:**` as a raw JSON dump in markdown and
// `tool_calls[].arguments` as a raw map in JSON. Both are gone. That is
// the whole point — `redactMessages` walks only top-level string values,
// so a secret one level down went into the file — but "the export is
// unchanged for classic sessions" was too strong a sentence to leave
// standing, and an untested deliberate break is indistinguishable from
// an accidental one on the next reading.
func TestExport_ClassicToolCallsLoseTheirRawArguments(t *testing.T) {
	t.Parallel()
	mgr, rec := newSQLManager(t)
	ctx := context.Background()
	if _, err := mgr.AppendMessage(ctx, rec.ID, session.Message{
		Role:    session.RoleAssistant,
		Content: "ok",
		ToolCalls: []session.ToolCall{{
			ID: "tc-1", Name: "read_file",
			Arguments: map[string]any{"path": "/etc/hosts", "limit": float64(10)},
			Result:    "127.0.0.1 localhost",
		}},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	msgs, err := mgr.ListMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	md, _, err := export.Render(export.FormatMarkdown, rec, msgs, exportNow)
	if err != nil {
		t.Fatalf("Render markdown: %v", err)
	}
	s := string(md)
	if strings.Contains(s, "/etc/hosts") {
		t.Errorf("markdown still prints argument VALUES:\n%s", s)
	}
	if !strings.Contains(s, "**Arguments (names and types):**") ||
		!strings.Contains(s, "limit:number path:string") {
		t.Errorf("markdown lost the names-and-types summary:\n%s", s)
	}
	// The result is still printed — it is what the tool said, not what
	// was passed in — and it is still credential-scanned and capped.
	if !strings.Contains(s, "127.0.0.1 localhost") {
		t.Errorf("markdown dropped the tool result:\n%s", s)
	}

	jb, _, err := export.Render(export.FormatJSON, rec, msgs, exportNow)
	if err != nil {
		t.Fatalf("Render json: %v", err)
	}
	var raw struct {
		Meta struct {
			ExportFormatVersion int `json:"export_format_version"`
		} `json:"meta"`
		Messages []struct {
			ToolCalls []map[string]any `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(jb, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The removal is why the schema version moved. A v1 reader walking
	// `arguments` could not tell "gone" from "no arguments".
	if raw.Meta.ExportFormatVersion < 2 {
		t.Errorf("export_format_version = %d, want >= 2 — `arguments` was removed, "+
			"which is a breaking schema change", raw.Meta.ExportFormatVersion)
	}
	tc := raw.Messages[0].ToolCalls[0]
	if _, present := tc["arguments"]; present {
		t.Errorf("JSON still carries the raw arguments map: %+v", tc)
	}
	if tc["args_summary"] != "limit:number path:string" {
		t.Errorf("args_summary = %v", tc["args_summary"])
	}
}

// ---------------------------------------------------------------------
// The two-layer redaction rule, adversarially.
// ---------------------------------------------------------------------

func TestExport_NeverCarriesRawToolArguments(t *testing.T) {
	t.Parallel()
	mgr, rec := newSQLManager(t)
	ctx := context.Background()

	// Legacy row shape: a classic assistant row whose ToolCall carries a
	// structured argument map. The credential scanner only walks
	// TOP-LEVEL string values, so the nested and the array secret are the
	// adversarial cases — they are exactly what a "redact the arguments"
	// contract misses and a "never print the arguments" contract cannot.
	const nested = "nested-secret-8f3a91c4"
	const inArray = "array-secret-2b7d0e6a"
	const topLevel = "sk-ant-api01-abcdefghijklmnopqrstuvwxyz012345"
	if _, err := mgr.AppendMessage(ctx, rec.ID, session.Message{
		Role:    session.RoleAssistant,
		Content: "calling out",
		ToolCalls: []session.ToolCall{{
			ID:   "tc-legacy",
			Name: "http_post",
			Arguments: map[string]any{
				"url":     "https://example.test",
				"token":   topLevel,
				"headers": map[string]any{"authorization": nested},
				"body":    []any{"prefix", inArray},
			},
		}},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	// Move-bearing row shape: the raw arguments live in the MODEL layer's
	// column, which the export must never read.
	msgs := persistMoveTurn(t, mgr, rec.ID, fullTurn)

	for _, format := range []export.Format{export.FormatMarkdown, export.FormatJSON} {
		b, _, err := export.Render(format, rec, msgs, exportNow)
		if err != nil {
			t.Fatalf("Render(%s): %v", format, err)
		}
		s := string(b)
		for _, secret := range []string{nested, inArray, topLevel, "/etc/shadow"} {
			if strings.Contains(s, secret) {
				t.Errorf("%s export leaked %q:\n%s", format, secret, s)
			}
		}
		// The names-and-types summary is what takes its place.
		if !strings.Contains(s, "body:array") ||
			!strings.Contains(s, "headers:object") ||
			!strings.Contains(s, "token:string") {
			t.Errorf("%s export lost the args summary:\n%s", format, s)
		}
	}
}

// TestExport_RedactsArgumentNames closes the hole the adversarial review
// of WP05 found: "argument values never appear" made the export safe
// against a secret in a nested object or an array, but argument NAMES
// are printed by design, and `redactMessages` only ever walked argument
// VALUES. A credential sitting in a key — a header map flattened into
// arguments is the obvious shape — went into both documents verbatim.
//
// Mutation check (kills this test): drop the `RedactValue(k)` call in
// `argsSummaryFromValues` and print `k` directly.
func TestExport_RedactsArgumentNames(t *testing.T) {
	t.Parallel()
	mgr, rec := newSQLManager(t)
	ctx := context.Background()

	const keySecret = "sk-ant-api01-KEYKEYKEYKEYKEYKEYKEY99"
	if _, err := mgr.AppendMessage(ctx, rec.ID, session.Message{
		Role:    session.RoleAssistant,
		Content: "calling out",
		ToolCalls: []session.ToolCall{{
			ID: "tc-key", Name: "http_post",
			Arguments: map[string]any{keySecret: 1, "url": "https://example.test"},
		}},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	msgs, err := mgr.ListMessages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	for _, format := range []export.Format{export.FormatMarkdown, export.FormatJSON} {
		b, _, err := export.Render(format, rec, msgs, exportNow)
		if err != nil {
			t.Fatalf("Render(%s): %v", format, err)
		}
		s := string(b)
		if strings.Contains(s, keySecret) {
			t.Errorf("%s export leaked a credential sitting in an argument NAME:\n%s",
				format, s)
		}
		// `encoding/json` HTML-escapes the angle brackets, so the marker
		// reads `<REDACTED:…>` in the JSON document.
		want := "<REDACTED:anthropic-key>:number"
		if format == export.FormatJSON {
			want = `\u003cREDACTED:anthropic-key\u003e:number`
		}
		if !strings.Contains(s, want) {
			t.Errorf("%s export dropped the redacted name from the summary:\n%s",
				format, s)
		}
	}
}

func TestExport_CapsToolOutput(t *testing.T) {
	t.Parallel()
	mgr, rec := newSQLManager(t)

	huge := strings.Repeat("x", 40000)
	msgs := persistMoveTurn(t, mgr, rec.ID, func(span string) []session.TranscriptEntry {
		e := fullTurn(span)
		e[2].Content = huge
		return e
	})

	for _, format := range []export.Format{export.FormatMarkdown, export.FormatJSON} {
		b, _, err := export.Render(format, rec, msgs, exportNow)
		if err != nil {
			t.Fatalf("Render(%s): %v", format, err)
		}
		s := string(b)
		if !strings.Contains(s, "output truncated") {
			t.Errorf("%s export did not cap a 40k tool result:\n%s", format, s[:min(len(s), 800)])
		}
		if strings.Contains(s, huge) {
			t.Errorf("%s export carried the uncapped output", format)
		}
		if len(s) > 20000 {
			t.Errorf("%s export is %d bytes for one capped result", format, len(s))
		}
	}
}

func TestExport_CapDoesNotSplitARune(t *testing.T) {
	t.Parallel()
	mgr, rec := newSQLManager(t)

	// The cap lands exactly on the multi-byte rune. Cutting at a byte
	// offset here paints U+FFFD into the exported document.
	raw := strings.Repeat("a", export.ToolOutputCap-1) + "日本語"
	msgs := persistMoveTurn(t, mgr, rec.ID, func(span string) []session.TranscriptEntry {
		e := fullTurn(span)
		e[2].Content = raw
		return e
	})
	b, _, err := export.Render(export.FormatMarkdown, rec, msgs, exportNow)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(b)
	if strings.Contains(s, "\uFFFD") {
		t.Errorf("export split a rune at the cap")
	}
	if !strings.Contains(s, "日") || strings.Contains(s, "本") {
		t.Errorf("cap did not land where expected:\n%s", s[len(s)-200:])
	}
}

// ---------------------------------------------------------------------
// The positional answer rule.
// ---------------------------------------------------------------------

func TestExport_InterruptedTurnKeepsItsTrajectory(t *testing.T) {
	t.Parallel()
	mgr, rec := newSQLManager(t)
	// A turn that never reached `final` — the user hit stop, or the
	// kernel errored out.
	msgs := persistMoveTurn(t, mgr, rec.ID, func(span string) []session.TranscriptEntry {
		return fullTurn(span)[:6]
	})

	b, _, err := export.Render(export.FormatMarkdown, rec, msgs, exportNow)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s := string(b)

	// The positional rule says the last non-empty assistant move IS the
	// answer, so the document reads as an answered turn — exactly what
	// the chat surface renders for the same rows. Keying the answer off
	// `kind == final` instead leaves the turn answerless and buries the
	// text the user actually read inside a trajectory disclosure.
	head, _, found := strings.Cut(s, "<details>")
	if !found {
		t.Fatalf("no trajectory disclosure at all:\n%s", s)
	}
	if !strings.Contains(head, "Native tools are blocked; trying bash.") {
		t.Errorf("the turn's last text is not the answer body:\n%s", s)
	}
	if !strings.Contains(head, "## Turn 2 — Assistant\n") {
		t.Errorf("the answer did not get a plain turn heading:\n%s", head)
	}
	// Everything else survives, folded.
	for _, want := range []string{
		"Let me read the key files.",
		"permission denied",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("trajectory dropped %q:\n%s", want, s)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
