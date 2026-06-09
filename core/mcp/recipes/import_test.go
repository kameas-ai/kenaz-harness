package recipes_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// TestImport_OldShape_StdioEntry exercises the dominant real-world
// case: a Claude Desktop config with `command/args/env` style entries.
// Each entry must translate to a stdio recipe with command preserved
// verbatim, env names projected into env_keys (Required=true), and
// Capabilities.Tools=true (the safe default — handshake re-negotiates).
func TestImport_OldShape_StdioEntry(t *testing.T) {
	payload := `{
		"mcpServers": {
			"github": {
				"command": "npx",
				"args": ["-y", "@modelcontextprotocol/server-github"],
				"env": {"GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_xxx"}
			},
			"sqlite": {
				"command": "uvx",
				"args": ["mcp-server-sqlite", "--db-path", "/tmp/x.db"]
			}
		}
	}`

	report, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
	if err != nil {
		t.Fatalf("ImportClaudeDesktop: %v", err)
	}
	if report.KeptCount != 2 {
		t.Fatalf("KeptCount = %d, want 2", report.KeptCount)
	}
	if len(report.Entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2", len(report.Entries))
	}

	byID := map[string]recipes.ImportEntry{}
	for _, e := range report.Entries {
		byID[e.ID] = e
	}

	gh, ok := byID["github"]
	if !ok {
		t.Fatalf("expected entry id 'github', got %v", report.Entries)
	}
	if gh.Status != recipes.ImportStatusKept {
		t.Errorf("github Status = %q, want %q", gh.Status, recipes.ImportStatusKept)
	}
	wantCmd := []string{"npx", "-y", "@modelcontextprotocol/server-github"}
	if len(gh.Recipe.Command) != len(wantCmd) {
		t.Fatalf("github Command = %v, want %v", gh.Recipe.Command, wantCmd)
	}
	for i := range wantCmd {
		if gh.Recipe.Command[i] != wantCmd[i] {
			t.Errorf("github Command[%d] = %q, want %q", i, gh.Recipe.Command[i], wantCmd[i])
		}
	}
	if len(gh.Recipe.EnvKeys) != 1 || gh.Recipe.EnvKeys[0].Name != "GITHUB_PERSONAL_ACCESS_TOKEN" {
		t.Errorf("github EnvKeys = %v", gh.Recipe.EnvKeys)
	}
	if !gh.Recipe.EnvKeys[0].Required {
		t.Error("github EnvKey.Required = false, want true")
	}
	if !gh.Recipe.Capabilities.Tools {
		t.Error("github Capabilities.Tools = false, want true")
	}

	sq, ok := byID["sqlite"]
	if !ok {
		t.Fatalf("expected entry id 'sqlite'")
	}
	if sq.Status != recipes.ImportStatusKept {
		t.Errorf("sqlite Status = %q", sq.Status)
	}
	if len(sq.Recipe.EnvKeys) != 0 {
		t.Errorf("sqlite EnvKeys = %v, want empty", sq.Recipe.EnvKeys)
	}
}

func TestImport_HTTPType_Unsupported(t *testing.T) {
	payload := `{
		"mcpServers": {
			"my-http": {
				"type": "http",
				"url": "https://api.example.com/mcp",
				"headers": {"Authorization": "Bearer ${MY_TOKEN}"}
			}
		}
	}`
	report, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
	if err != nil {
		t.Fatalf("ImportClaudeDesktop: %v", err)
	}
	if report.KeptCount != 0 {
		t.Errorf("KeptCount = %d, want 0", report.KeptCount)
	}
	if report.UnsupportedCount != 1 {
		t.Errorf("UnsupportedCount = %d, want 1", report.UnsupportedCount)
	}
	e := report.Entries[0]
	if e.Status != recipes.ImportStatusUnsupported {
		t.Errorf("Status = %q, want %q", e.Status, recipes.ImportStatusUnsupported)
	}
	if !strings.Contains(e.Reason, "WP03") {
		t.Errorf("Reason = %q, want a reference to WP03", e.Reason)
	}
}

func TestImport_SSEType_Unsupported(t *testing.T) {
	payload := `{
		"mcpServers": {
			"my-sse": {
				"type": "sse",
				"url": "https://api.example.com/sse"
			}
		}
	}`
	report, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
	if err != nil {
		t.Fatalf("ImportClaudeDesktop: %v", err)
	}
	if report.UnsupportedCount != 1 {
		t.Errorf("UnsupportedCount = %d, want 1", report.UnsupportedCount)
	}
	e := report.Entries[0]
	if !strings.Contains(e.Reason, "WP04") {
		t.Errorf("Reason = %q, want a reference to WP04", e.Reason)
	}
}

func TestImport_OAuth2_Unsupported(t *testing.T) {
	cases := map[string]string{
		"string-form": `{"mcpServers":{"x":{"command":"npx","args":["x"],"auth":"oauth2"}}}`,
		"object-form": `{"mcpServers":{"x":{"command":"npx","args":["x"],"auth":{"method":"oauth2"}}}}`,
		// "type" sub-field of auth.
		"object-type-form": `{"mcpServers":{"x":{"command":"npx","args":["x"],"authentication":{"type":"OAUTH2"}}}}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			report, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
			if err != nil {
				t.Fatalf("ImportClaudeDesktop: %v", err)
			}
			if report.UnsupportedCount != 1 || report.KeptCount != 0 {
				t.Fatalf("counts = (kept=%d, unsup=%d)", report.KeptCount, report.UnsupportedCount)
			}
			e := report.Entries[0]
			if e.Status != recipes.ImportStatusUnsupported {
				t.Errorf("Status = %q", e.Status)
			}
			if !strings.Contains(strings.ToLower(e.Reason), "oauth2") {
				t.Errorf("Reason = %q, want oauth2 mention", e.Reason)
			}
		})
	}
}

func TestImport_UnknownAuthMethod_Unsupported(t *testing.T) {
	payload := `{"mcpServers":{"x":{"command":"npx","args":["x"],"auth":{"method":"saml"}}}}`
	report, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
	if err != nil {
		t.Fatalf("ImportClaudeDesktop: %v", err)
	}
	if report.UnsupportedCount != 1 {
		t.Errorf("UnsupportedCount = %d", report.UnsupportedCount)
	}
	if !strings.Contains(report.Entries[0].Reason, "saml") {
		t.Errorf("Reason = %q, want saml mention", report.Entries[0].Reason)
	}
}

func TestImport_StaticAuth_AcceptedAsStdio(t *testing.T) {
	// "static" / "env" auth is a no-op annotation — the entry must
	// translate cleanly.
	payload := `{"mcpServers":{"my":{"command":"npx","args":["x"],"auth":"env"}}}`
	report, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
	if err != nil {
		t.Fatalf("ImportClaudeDesktop: %v", err)
	}
	if report.KeptCount != 1 {
		t.Errorf("KeptCount = %d, want 1", report.KeptCount)
	}
}

func TestImport_MalformedEntry_MarkedMalformed(t *testing.T) {
	cases := map[string]string{
		// Missing command field.
		"missing-command": `{"mcpServers":{"x":{"args":["a"]}}}`,
		// command must be string.
		"command-array":   `{"mcpServers":{"x":{"command":["npx","-y"]}}}`,
		// args must be string array.
		"args-not-array":  `{"mcpServers":{"x":{"command":"npx","args":"not-an-array"}}}`,
		// env must be object.
		"env-not-object":  `{"mcpServers":{"x":{"command":"npx","env":["bad"]}}}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			report, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
			if err != nil {
				t.Fatalf("ImportClaudeDesktop: %v", err)
			}
			if report.MalformedCount != 1 {
				t.Errorf("MalformedCount = %d, want 1; report=%+v", report.MalformedCount, report)
			}
			if report.Entries[0].Reason == "" {
				t.Error("Reason empty for malformed entry")
			}
		})
	}
}

func TestImport_MissingMCPServersKey(t *testing.T) {
	// Pasted a non-config blob.
	payload := `{"foo": "bar"}`
	_, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
	if !errors.Is(err, recipes.ErrImportMissingMCPServers) {
		t.Errorf("err = %v, want ErrImportMissingMCPServers", err)
	}
}

func TestImport_InvalidJSON(t *testing.T) {
	payload := `{not even close`
	_, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
	if !errors.Is(err, recipes.ErrImportInvalidJSON) {
		t.Errorf("err = %v, want ErrImportInvalidJSON", err)
	}
}

func TestImport_EmptyPayload(t *testing.T) {
	_, err := recipes.ImportClaudeDesktop(nil, nil)
	if !errors.Is(err, recipes.ErrImportEmptyPayload) {
		t.Errorf("err = %v, want ErrImportEmptyPayload", err)
	}
	_, err = recipes.ImportClaudeDesktop([]byte(""), nil)
	if !errors.Is(err, recipes.ErrImportEmptyPayload) {
		t.Errorf("err = %v, want ErrImportEmptyPayload", err)
	}
}

func TestImport_PayloadCap(t *testing.T) {
	// Build a 65 KiB payload that's syntactically valid JSON.
	var sb strings.Builder
	sb.WriteString(`{"mcpServers":{"x":{"command":"npx","args":["`)
	// Pad with non-quote chars until oversize.
	pad := strings.Repeat("A", recipes.MaxImportPayloadBytes+128)
	sb.WriteString(pad)
	sb.WriteString(`"]}}}`)
	_, err := recipes.ImportClaudeDesktop([]byte(sb.String()), nil)
	if !errors.Is(err, recipes.ErrImportPayloadTooLarge) {
		t.Errorf("err = %v, want ErrImportPayloadTooLarge", err)
	}
}

func TestImport_IDCollision_KeptWithWarning(t *testing.T) {
	payload := `{"mcpServers":{"github":{"command":"npx","args":["x"]}}}`
	existing := map[string]bool{"github": true}
	report, err := recipes.ImportClaudeDesktop([]byte(payload), existing)
	if err != nil {
		t.Fatalf("ImportClaudeDesktop: %v", err)
	}
	if report.KeptCount != 1 {
		t.Errorf("KeptCount = %d, want 1 (collision is kept-with-warning)", report.KeptCount)
	}
	if report.CollisionCount != 1 {
		t.Errorf("CollisionCount = %d, want 1", report.CollisionCount)
	}
	if report.Entries[0].Status != recipes.ImportStatusCollisionWarning {
		t.Errorf("Status = %q", report.Entries[0].Status)
	}
	if !strings.Contains(report.Entries[0].Reason, "github") {
		t.Errorf("Reason should mention the colliding id, got %q", report.Entries[0].Reason)
	}
	// Recipe still populated so the caller can save it.
	if report.Entries[0].Recipe.ID != "github" {
		t.Errorf("Recipe.ID = %q", report.Entries[0].Recipe.ID)
	}
}

func TestImport_NameSanitised(t *testing.T) {
	// Underscores, capitals, and dots get coerced to dashes.
	payload := `{"mcpServers":{"My_Tool.v2":{"command":"npx","args":["x"]}}}`
	report, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
	if err != nil {
		t.Fatalf("ImportClaudeDesktop: %v", err)
	}
	e := report.Entries[0]
	if e.Status != recipes.ImportStatusKept {
		t.Fatalf("Status = %q (Reason=%q)", e.Status, e.Reason)
	}
	if e.OriginalName != "My_Tool.v2" {
		t.Errorf("OriginalName = %q", e.OriginalName)
	}
	if !strings.Contains(e.ID, "tool") {
		t.Errorf("sanitised ID lost the meat: %q", e.ID)
	}
	// ID must satisfy the recipe-id regex.
	if err := recipes.ValidateRecipeID(e.ID); err != nil {
		t.Errorf("sanitised ID fails ValidateRecipeID: %v", err)
	}
}

func TestImport_NameWithPathSeparators_RejectedSafely(t *testing.T) {
	// Even if a hostile paste tries to escape via path separators, the
	// sanitiser must produce a safe id (or surface as malformed).
	payload := `{"mcpServers":{"../etc/passwd":{"command":"x"}}}`
	report, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
	if err != nil {
		t.Fatalf("ImportClaudeDesktop: %v", err)
	}
	for _, e := range report.Entries {
		// Whatever the outcome, the resulting ID must NOT contain
		// path separators or `..`.
		if strings.ContainsAny(e.ID, "/\\") || strings.Contains(e.ID, "..") {
			t.Errorf("sanitised ID retains path-traversal chars: %q", e.ID)
		}
	}
}

func TestImport_StableEntryOrder(t *testing.T) {
	// Map iteration is unordered; the translator must emit entries in
	// id-sorted order so tests aren't flaky.
	payload := `{"mcpServers":{
		"zeta":{"command":"npx","args":["z"]},
		"alpha":{"command":"npx","args":["a"]},
		"middle":{"command":"npx","args":["m"]}
	}}`
	report, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
	if err != nil {
		t.Fatalf("ImportClaudeDesktop: %v", err)
	}
	if len(report.Entries) != 3 {
		t.Fatalf("entries=%d", len(report.Entries))
	}
	for i := range report.Entries {
		if i > 0 && report.Entries[i].ID < report.Entries[i-1].ID {
			t.Errorf("entries not sorted: %v then %v", report.Entries[i-1].ID, report.Entries[i].ID)
		}
	}
}

func TestImport_OriginalJSONPreservedPerEntry(t *testing.T) {
	payload := `{"mcpServers":{"x":{"command":"npx","args":["a"]}}}`
	report, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
	if err != nil {
		t.Fatalf("ImportClaudeDesktop: %v", err)
	}
	if len(report.Entries[0].OriginalJSON) == 0 {
		t.Fatal("OriginalJSON empty; expected per-entry preservation")
	}
	var got map[string]any
	if err := json.Unmarshal(report.Entries[0].OriginalJSON, &got); err != nil {
		t.Fatalf("OriginalJSON not valid JSON: %v", err)
	}
	if got["command"] != "npx" {
		t.Errorf("OriginalJSON content lost: %v", got)
	}
}

// TestWriteImportArtifacts_DryRunNotInvoked is a guard: the helper
// itself does NOT consult any dryRun flag — that gate lives in the
// RPC wrapper. We only assert the helper writes when called and that
// the resulting yaml round-trips through UserStore.
func TestWriteImportArtifacts_RoundTripsThroughUserStore(t *testing.T) {
	dir := t.TempDir()
	entry := recipes.ImportEntry{
		ID:           "round-trip-tool",
		OriginalName: "round-trip-tool",
		Status:       recipes.ImportStatusKept,
		Recipe: recipes.Recipe{
			ID:          "round-trip-tool",
			DisplayName: "Round Trip",
			Description: "test",
			Category:    "imported",
			Command:     []string{"npx", "-y", "round-trip"},
			Capabilities: recipes.Capabilities{Tools: true},
		},
		OriginalJSON: json.RawMessage(`{"command":"npx","args":["-y","round-trip"]}`),
	}
	yamlPath, jsonPath, err := recipes.WriteImportArtifacts(dir, entry)
	if err != nil {
		t.Fatalf("WriteImportArtifacts: %v", err)
	}
	if !strings.HasSuffix(yamlPath, "round-trip-tool.yaml") {
		t.Errorf("yamlPath = %q", yamlPath)
	}
	if !strings.HasSuffix(jsonPath, "round-trip-tool.json") {
		t.Errorf("jsonPath = %q", jsonPath)
	}
	// The yaml lives under <dataDir>/mcp/recipes/_imports/.
	wantDir := filepath.Join(dir, "mcp", "recipes", "_imports")
	if !strings.HasPrefix(yamlPath, wantDir) {
		t.Errorf("yamlPath %q not under %q", yamlPath, wantDir)
	}
	// Round-trip through UserStore.
	store := recipes.NewUserStore(dir, nil)
	got, err := store.Load()
	if err != nil {
		t.Fatalf("UserStore.Load: %v", err)
	}
	var found bool
	for _, r := range got {
		if r.ID == "round-trip-tool" {
			found = true
			if r.Source != recipes.SourceImported {
				t.Errorf("Source = %q, want %q", r.Source, recipes.SourceImported)
			}
			if len(r.Command) != 3 {
				t.Errorf("Command = %v", r.Command)
			}
		}
	}
	if !found {
		t.Errorf("round-trip recipe not loaded: got %d recipes", len(got))
	}
}

func TestWriteImportArtifacts_RejectsUnsafeID(t *testing.T) {
	dir := t.TempDir()
	bad := recipes.ImportEntry{
		ID: "../escape",
		Recipe: recipes.Recipe{
			ID:      "../escape",
			Command: []string{"npx"},
		},
	}
	if _, _, err := recipes.WriteImportArtifacts(dir, bad); err == nil {
		t.Error("WriteImportArtifacts(../escape) = nil, want error")
	}
	// Belt-and-braces: directory should not have been created with a
	// suspicious file inside.
	if _, err := os.Stat(filepath.Join(dir, "..", "escape.yaml")); err == nil {
		t.Error("escape.yaml exists outside dataDir; unsafe write happened")
	}
}

func TestWriteImportArtifacts_EmptyDataDirRejected(t *testing.T) {
	bad := recipes.ImportEntry{ID: "x", Recipe: recipes.Recipe{ID: "x", Command: []string{"npx"}}}
	if _, _, err := recipes.WriteImportArtifacts("", bad); err == nil {
		t.Error("WriteImportArtifacts(empty dataDir) = nil, want error")
	}
}

func TestImport_NewShape_Unrecognized_ReportsType(t *testing.T) {
	payload := `{"mcpServers":{"weird":{"type":"websocket","url":"wss://x"}}}`
	report, err := recipes.ImportClaudeDesktop([]byte(payload), nil)
	if err != nil {
		t.Fatalf("ImportClaudeDesktop: %v", err)
	}
	if report.UnsupportedCount != 1 {
		t.Errorf("UnsupportedCount = %d, want 1", report.UnsupportedCount)
	}
	if !strings.Contains(report.Entries[0].Reason, "websocket") {
		t.Errorf("Reason = %q, want websocket mention", report.Entries[0].Reason)
	}
}
