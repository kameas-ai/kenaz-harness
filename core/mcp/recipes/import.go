// Package recipes — clipboard import translator.
//
// ImportClaudeDesktop translates a pasted Claude Desktop / Cursor
// `mcpServers` JSON config into harness-shaped Recipe entries. The
// translator handles the real-world shapes:
//
//   - "Old style" stdio entries — `{ "mcpServers": { "<name>":
//     { "command": "...", "args": [...], "env": {...} } } }` — translate
//     directly to stdio recipes the harness can spawn today.
//   - "New `type` style" HTTP entries — `{ "type": "http", "url": ...,
//     "headers": {...} }` — translate to a Recipe with
//     Transport="http", URL, and HeadersTemplate. The MCP HTTP
//     transport (core/mcp/transport/http) is live and is what the
//     majority of the shipped catalog already speaks.
//   - "New `type` style" SSE entries — `{ "type": "sse", "url": ...,
//     "post_url"|"postUrl": ... }` — translate to a Recipe with
//     Transport="sse", URL, HeadersTemplate, and PostURL. The harness's
//     SSE transport (core/mcp/transport/sse) requires a static
//     client->server POST endpoint (it does not perform the classic
//     SSE "endpoint" discovery event some servers rely on), so an SSE
//     entry with no post_url/postUrl field is reported malformed with
//     a reason naming that specific gap — not a categorical "SSE is
//     unsupported" refusal, because it is not.
//
// Entries with unrecognised auth methods (oauth2, custom auth schemes,
// etc.) and entries whose `type` names a transport the harness does not
// speak at all are flagged unsupported with a one-line reason describing
// the actual limitation. No refusal reason names a work package — a
// note-to-self that outlives its context becomes a claim to users; see
// paste-import-accepts-what-we-support-01PMZG16 for the finding this
// fixed (two refusal arms cited already-shipped work packages as reasons
// two of the three real-world entry shapes were unsupported, when both
// were fully implemented). Malformed entries surface with parser-level
// error refs.
//
// Output: TranslationReport with one ImportEntry per `mcpServers`
// member. Callers (the RPC wrapper in `core/rpc/views/mcp/import.go`)
// pick which entries to keep, write the kept entries' translated YAML
// to <DataDir>/mcp/recipes/_imports/<id>.yaml, and preserve each
// entry's original JSON at <DataDir>/mcp/recipes/_imports/<id>.json.
//
// Spec mapping: WP08 of mission mcp-server-install-01KQ8TDP (original
// translator); paste-import-accepts-what-we-support-01PMZG16 (HTTP/SSE
// translation, this file's WP03). See FR-004 (translator), NFR-003
// (64 KiB payload cap), risk register row "Clipboard import accepts a
// credential-dump JSON pasted by mistake" (cap + missing-mcpServers
// reject).
package recipes

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxImportPayloadBytes is the hard cap on the clipboard payload the
// translator accepts. NFR-003: a typical Claude Desktop config is well
// under 4 KiB; 64 KiB leaves generous headroom while protecting against
// credential-dump pastes.
const MaxImportPayloadBytes = 64 * 1024

// ImportEntry status values.
const (
	// ImportStatusKept means the entry parsed cleanly, validated, and
	// produced a Recipe ready to save. The translator stamps this on
	// every successfully-translated stdio entry.
	ImportStatusKept = "kept"
	// ImportStatusUnsupported means the entry parsed but the harness
	// cannot adopt it today. Reason is populated with a one-line
	// human-readable explanation of the actual limitation (e.g. an
	// unrecognised transport or auth method). Reason must never cite a
	// work package — see this file's package doc comment for why: a
	// note-to-self that outlives its context becomes a false claim to
	// users the moment the cited work ships.
	ImportStatusUnsupported = "unsupported"
	// ImportStatusMalformed means the entry's JSON is structurally
	// invalid for the translator (missing required fields, wrong
	// types, etc.). Reason is populated with the parser-level error.
	ImportStatusMalformed = "malformed"
	// ImportStatusCollisionWarning means the entry translated cleanly
	// but its id collides with an existing recipe (shipped, registry,
	// or user). The entry is kept-with-warning: callers may still save
	// it. The MergedCatalog precedence (user > registry > shipped)
	// handles which wins at runtime.
	ImportStatusCollisionWarning = "collision_warning"
)

// ImportEntry is one translated entry from a pasted mcpServers map.
// Exactly one of Recipe / Reason is populated based on Status:
//   - Status=kept | collision_warning → Recipe is set; Reason may
//     additionally carry collision metadata.
//   - Status=unsupported | malformed → Reason is set; Recipe is the
//     zero value (caller MUST NOT save it).
type ImportEntry struct {
	// ID is the entry's `mcpServers` key, sanitised to harness-safe
	// recipe id form (lowercase, dash-separated, prefix-letter). When
	// the original key cannot be sanitised to a valid id, ID is set to
	// the sanitised best-effort and Status is "malformed".
	ID string `json:"id"`
	// OriginalName preserves the exact key from the pasted JSON so the
	// UI can render "imported as <ID> (was <OriginalName>)" when the
	// id was rewritten.
	OriginalName string `json:"original_name"`
	// Status is one of the ImportStatus* constants.
	Status string `json:"status"`
	// Reason is the one-line human-readable explanation for
	// non-kept entries (or the collision target id for
	// collision_warning).
	Reason string `json:"reason,omitempty"`
	// Recipe is the translated harness recipe. Zero-value for
	// non-kept entries.
	Recipe Recipe `json:"recipe"`
	// OriginalJSON preserves this entry's pasted payload verbatim so
	// the RPC wrapper can write it to _imports/<id>.json. Populated
	// for every entry — even unsupported / malformed ones — so the
	// UI can echo back what the user actually pasted.
	OriginalJSON json.RawMessage `json:"original_json,omitempty"`
}

// TranslationReport is the result of one ImportClaudeDesktop call.
// The slice order matches the iteration order of the parsed
// `mcpServers` map (Go's map iteration is unordered, so the
// translator sorts entries by id for stable output).
type TranslationReport struct {
	Entries          []ImportEntry `json:"entries"`
	KeptCount        int           `json:"kept_count"`
	UnsupportedCount int           `json:"unsupported_count"`
	MalformedCount   int           `json:"malformed_count"`
	CollisionCount   int           `json:"collision_count"`
}

// ImportClaudeDesktop translates a pasted Claude Desktop / Cursor
// mcpServers JSON config into a TranslationReport. existingIDs is the
// set of recipe ids already known to the merged catalog (shipped +
// registry + user) used for collision detection; nil tolerates.
//
// dryRun mode is enforced by the RPC wrapper, not here — this function
// is pure translation, no I/O. Callers feeding dryRun=false call
// (*UserStore).WriteImports / equivalent to persist after.
//
// Errors are reserved for wholesale-payload problems (oversize, no
// `mcpServers` top-level key, JSON parse failure on the outer
// envelope). Per-entry problems land as ImportEntry.Status=malformed
// with a Reason — the function still returns a report, never fails the
// whole batch on one bad entry.
func ImportClaudeDesktop(rawJSON []byte, existingIDs map[string]bool) (TranslationReport, error) {
	if len(rawJSON) == 0 {
		return TranslationReport{}, ErrImportEmptyPayload
	}
	if len(rawJSON) > MaxImportPayloadBytes {
		return TranslationReport{}, fmt.Errorf("%w: payload is %d bytes, cap is %d",
			ErrImportPayloadTooLarge, len(rawJSON), MaxImportPayloadBytes)
	}

	// Top-level envelope: { "mcpServers": { ... } } or some configs use
	// "servers" / a bare map. We accept the canonical shape; otherwise
	// reject with a clear message.
	var envelope struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(rawJSON, &envelope); err != nil {
		return TranslationReport{}, fmt.Errorf("%w: %v", ErrImportInvalidJSON, err)
	}
	if envelope.MCPServers == nil {
		return TranslationReport{}, ErrImportMissingMCPServers
	}

	// Sort keys for stable output. Go's map iteration is randomised,
	// which would make tests flaky and the modal listing reorder on
	// every paste.
	keys := make([]string, 0, len(envelope.MCPServers))
	for k := range envelope.MCPServers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	report := TranslationReport{Entries: make([]ImportEntry, 0, len(keys))}
	for _, name := range keys {
		raw := envelope.MCPServers[name]
		entry := translateOne(name, raw, existingIDs)
		switch entry.Status {
		case ImportStatusKept:
			report.KeptCount++
		case ImportStatusUnsupported:
			report.UnsupportedCount++
		case ImportStatusMalformed:
			report.MalformedCount++
		case ImportStatusCollisionWarning:
			report.CollisionCount++
			report.KeptCount++ // a collision is still kept-with-warning
		}
		report.Entries = append(report.Entries, entry)
	}
	return report, nil
}

// translateOne handles one entry from the mcpServers map. It dispatches
// by transport shape and accumulates an ImportEntry. The function never
// returns an error — every failure becomes an Status=malformed or
// Status=unsupported entry so the caller still gets one row per pasted
// member.
func translateOne(name string, raw json.RawMessage, existingIDs map[string]bool) ImportEntry {
	entry := ImportEntry{
		OriginalName: name,
		OriginalJSON: append(json.RawMessage(nil), raw...),
	}

	// First decode into a permissive map so we can inspect the `type`
	// field (new shape) before falling through to the old shape.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		entry.ID = sanitiseRecipeID(name)
		entry.Status = ImportStatusMalformed
		entry.Reason = fmt.Sprintf("entry %q is not a JSON object: %v", name, err)
		return entry
	}

	// Auth-method detection. Some Claude Desktop / Cursor configs carry
	// an "auth" or "authentication" field; we reject any non-static
	// scheme up front so the user sees a clear message rather than a
	// generic "unsupported field".
	if reason := detectUnsupportedAuth(probe); reason != "" {
		entry.ID = sanitiseRecipeID(name)
		entry.Status = ImportStatusUnsupported
		entry.Reason = reason
		return entry
	}

	// Dispatch by transport `type`.
	transportType := stringField(probe, "type")
	switch strings.ToLower(strings.TrimSpace(transportType)) {
	case "", "stdio":
		recipe, err := translateStdio(name, raw, probe)
		return finishEntry(entry, name, recipe, err, existingIDs)
	case "http":
		recipe, err := translateRemote(name, probe, TransportHTTP)
		return finishEntry(entry, name, recipe, err, existingIDs)
	case "sse":
		recipe, err := translateRemote(name, probe, TransportSSE)
		return finishEntry(entry, name, recipe, err, existingIDs)
	default:
		entry.ID = sanitiseRecipeID(name)
		entry.Status = ImportStatusUnsupported
		entry.Reason = fmt.Sprintf("unknown transport type %q", transportType)
		return entry
	}
}

// finishEntry stamps Status / Reason / Recipe on entry based on a
// translator's (Recipe, error) result and runs the collision check.
// err != nil always means the entry is malformed — both translateStdio
// and translateRemote only return an error for a per-entry problem
// (missing/malformed field, failed Recipe.Validate), never for a
// categorical "this transport isn't supported" refusal; that case is
// handled by translateOne's default arm before either translator runs.
func finishEntry(entry ImportEntry, name string, recipe Recipe, err error, existingIDs map[string]bool) ImportEntry {
	if err != nil {
		entry.ID = sanitiseRecipeID(name)
		entry.Status = ImportStatusMalformed
		entry.Reason = err.Error()
		return entry
	}
	entry.ID = recipe.ID
	entry.Recipe = recipe
	if existingIDs != nil && existingIDs[entry.ID] {
		entry.Status = ImportStatusCollisionWarning
		entry.Reason = fmt.Sprintf("recipe id %q collides with an existing recipe; saving will shadow it", entry.ID)
		return entry
	}
	entry.Status = ImportStatusKept
	return entry
}

// translateStdio handles the old-shape stdio entry:
//
//	{ "command": "...", "args": [...], "env": {...} }
//
// The translator preserves command / args verbatim, projects env keys
// into Recipe.EnvKeys with Required=true (Claude Desktop env entries
// are always required), and sets Capabilities.Tools=true (the safe
// default — the harness re-negotiates capabilities at handshake).
func translateStdio(name string, _ json.RawMessage, probe map[string]json.RawMessage) (Recipe, error) {
	id := sanitiseRecipeID(name)
	if err := ValidateRecipeID(id); err != nil {
		return Recipe{}, fmt.Errorf("entry %q: cannot derive valid recipe id from name (got %q): %w", name, id, err)
	}

	cmdRaw, hasCmd := probe["command"]
	if !hasCmd {
		return Recipe{}, fmt.Errorf("entry %q: missing required field \"command\"", name)
	}
	var commandStr string
	if err := json.Unmarshal(cmdRaw, &commandStr); err != nil {
		return Recipe{}, fmt.Errorf("entry %q: \"command\" must be a string: %v", name, err)
	}
	commandStr = strings.TrimSpace(commandStr)
	if commandStr == "" {
		return Recipe{}, fmt.Errorf("entry %q: \"command\" is empty", name)
	}

	var args []string
	if argsRaw, ok := probe["args"]; ok {
		if err := json.Unmarshal(argsRaw, &args); err != nil {
			return Recipe{}, fmt.Errorf("entry %q: \"args\" must be an array of strings: %v", name, err)
		}
	}

	var envMap map[string]string
	if envRaw, ok := probe["env"]; ok {
		if err := json.Unmarshal(envRaw, &envMap); err != nil {
			return Recipe{}, fmt.Errorf("entry %q: \"env\" must be a string→string map: %v", name, err)
		}
	}

	cmd := append([]string{commandStr}, args...)

	// Claude Desktop's env shape is {NAME: VALUE}. The harness recipe
	// shape carries env_keys (the names; values resolve through the
	// secrets backend at spawn time). We project keys → EnvKeys with
	// Required=true and Display=Name. We DO NOT persist the values:
	// the recipe goes to disk; the values live in the keychain. The
	// import RPC layer is responsible for offering the user a
	// per-key paste-into-secrets affordance — WP08 only translates.
	envKeys := make([]EnvKey, 0, len(envMap))
	envNames := make([]string, 0, len(envMap))
	for k := range envMap {
		envNames = append(envNames, k)
	}
	sort.Strings(envNames)
	for _, k := range envNames {
		if k == "" {
			return Recipe{}, fmt.Errorf("entry %q: env contains empty key", name)
		}
		envKeys = append(envKeys, EnvKey{
			Name:     k,
			Display:  k,
			Required: true,
		})
	}

	r := Recipe{
		ID:          id,
		DisplayName: deriveDisplayName(name),
		Description: fmt.Sprintf("Imported from clipboard (originally %q).", name),
		Category:    "imported",
		Command:     cmd,
		EnvKeys:     envKeys,
		Capabilities: Capabilities{
			Tools: true,
		},
		// Imports default to recipe-level timeouts. Zero values pick up
		// the package defaults (5s init, 30s ping) at spawn.
	}
	if err := r.Validate(); err != nil {
		return Recipe{}, fmt.Errorf("entry %q: validation failed: %w", name, err)
	}
	return r, nil
}

// translateRemote handles the new-`type`-style HTTP and SSE entries:
//
//	{ "type": "http", "url": "...", "headers": {...} }
//	{ "type": "sse",  "url": "...", "headers": {...},
//	  "post_url" | "postUrl": "..." }
//
// The translator preserves url/headers verbatim into Recipe.URL /
// Recipe.HeadersTemplate — including any ${VAR} tokens a hand-authored
// header value carries, which the transport substitutes at
// connection-open time, exactly like a shipped-catalog recipe.
// (*Recipe).Validate (recipes.go, WP01 of mcp-server-install-01KQ8TDP)
// is the single source of truth for URL/PostURL invariants — scheme,
// non-empty host, no fragment, no userinfo; this function does not
// duplicate those checks, it only shapes the pasted fields into a
// Recipe and lets Validate reject what it would reject for a
// hand-authored one.
//
// SSE additionally requires a client->server POST endpoint. The
// harness's SSE transport (core/mcp/transport/sse) takes that
// statically from Recipe.PostURL — it does not implement the classic
// SSE-transport "endpoint" discovery event some servers emit on the
// stream to announce it at runtime. A pasted Claude Desktop / Cursor
// `type: sse` entry commonly carries only `url`, because that
// discovery model is what many real servers rely on instead. Rather
// than translate the "unsupported" refusal from "SSE" (false — see
// this package's doc comment) to "the pasted entry left out a field
// this harness needs" (a much narrower, honest claim), an entry missing
// every recognised post-URL field name is reported malformed with a
// reason that says exactly that.
func translateRemote(name string, probe map[string]json.RawMessage, transportKind string) (Recipe, error) {
	id := sanitiseRecipeID(name)
	if err := ValidateRecipeID(id); err != nil {
		return Recipe{}, fmt.Errorf("entry %q: cannot derive valid recipe id from name (got %q): %w", name, id, err)
	}

	urlStr := strings.TrimSpace(stringField(probe, "url"))
	if urlStr == "" {
		return Recipe{}, fmt.Errorf("entry %q: missing required field \"url\"", name)
	}

	var headers map[string]string
	if headersRaw, ok := probe["headers"]; ok {
		if err := json.Unmarshal(headersRaw, &headers); err != nil {
			return Recipe{}, fmt.Errorf("entry %q: \"headers\" must be a string→string map: %v", name, err)
		}
	}

	r := Recipe{
		ID:              id,
		DisplayName:     deriveDisplayName(name),
		Description:     fmt.Sprintf("Imported from clipboard (originally %q).", name),
		Category:        "imported",
		Transport:       transportKind,
		URL:             urlStr,
		HeadersTemplate: headers,
		Capabilities: Capabilities{
			Tools: true,
		},
	}

	if transportKind == TransportSSE {
		postURL := strings.TrimSpace(firstStringField(probe, "post_url", "postUrl", "message_url", "messageUrl"))
		if postURL == "" {
			return Recipe{}, fmt.Errorf(
				"entry %q: sse transport requires a %q (or %q) field naming the client-to-server POST endpoint; "+
					"this harness reads it statically and does not perform SSE \"endpoint\"-event auto-discovery",
				name, "post_url", "postUrl")
		}
		r.PostURL = postURL
	}

	if err := r.Validate(); err != nil {
		return Recipe{}, fmt.Errorf("entry %q: validation failed: %w", name, err)
	}
	return r, nil
}

// detectUnsupportedAuth inspects probe for an "auth" / "authentication"
// field whose method is anything other than env-var-injected. Returns
// a one-line reason when we should reject; empty string otherwise.
func detectUnsupportedAuth(probe map[string]json.RawMessage) string {
	for _, key := range []string{"auth", "authentication"} {
		raw, ok := probe[key]
		if !ok {
			continue
		}
		// Auth may be a string ("oauth2") or an object with a
		// "method"/"type" field. Probe both.
		var method string
		var asString string
		if err := json.Unmarshal(raw, &asString); err == nil {
			method = strings.ToLower(strings.TrimSpace(asString))
		} else {
			var asObj map[string]json.RawMessage
			if err := json.Unmarshal(raw, &asObj); err == nil {
				if m := stringField(asObj, "method"); m != "" {
					method = strings.ToLower(strings.TrimSpace(m))
				} else if t := stringField(asObj, "type"); t != "" {
					method = strings.ToLower(strings.TrimSpace(t))
				}
			}
		}
		switch method {
		case "":
			// Couldn't determine a method but the field is present —
			// reject conservatively rather than silently accepting.
			return "auth field present but method unrecognised; only env-var-injected secrets supported in v1"
		case "env", "env-var", "envvar", "static", "bearer-env", "api-key-env":
			// Static / env-injected — fine, fall through to normal
			// translation.
			continue
		case "oauth2", "oauth":
			return "auth method \"oauth2\" not supported (env-var-injected secrets only in v1)"
		default:
			return fmt.Sprintf("auth method %q not supported (env-var-injected secrets only in v1)", method)
		}
	}
	return ""
}

// stringField extracts a string field from a json.RawMessage map.
// Missing / non-string values yield "".
func stringField(probe map[string]json.RawMessage, key string) string {
	raw, ok := probe[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// firstStringField returns the first non-empty string field found among
// keys, checked in order. Used where a pasted config might spell the
// same concept under one of several synonymous field names (e.g. an
// SSE post endpoint as "post_url" or "postUrl").
func firstStringField(probe map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		if v := stringField(probe, k); v != "" {
			return v
		}
	}
	return ""
}

// recipeIDSanitiser matches every char that is NOT [a-z0-9-]. Anything
// matched gets collapsed to a dash before the leading-character /
// length checks run.
var recipeIDSanitiser = regexp.MustCompile(`[^a-z0-9-]+`)

// sanitiseRecipeID best-effort coerces a Claude Desktop server name
// into a harness recipe id. Strategy:
//  1. Lowercase.
//  2. Replace every run of non-[a-z0-9-] with a single dash.
//  3. Trim leading dashes / digits (id must start with a letter).
//  4. Trim trailing dashes.
//  5. Truncate to 64 chars.
//
// The returned id is then run through ValidateRecipeID by the caller;
// best-effort sanitisation that still fails validation surfaces as a
// malformed entry with a clear reason.
//
// Path-safety note: the returned id is also used as the basename of
// _imports/<id>.{yaml,json}. Step 2 above eliminates every shell
// metacharacter — `..`, `/`, leading `-`, NUL — by the time the id
// reaches the file writer.
func sanitiseRecipeID(name string) string {
	out := strings.ToLower(strings.TrimSpace(name))
	out = recipeIDSanitiser.ReplaceAllString(out, "-")
	// Trim leading non-letters: id must start with [a-z].
	for len(out) > 0 {
		c := out[0]
		if c >= 'a' && c <= 'z' {
			break
		}
		out = out[1:]
	}
	// Trim trailing dashes.
	out = strings.TrimRight(out, "-")
	if len(out) > 64 {
		out = out[:64]
		out = strings.TrimRight(out, "-")
	}
	return out
}

// deriveDisplayName turns a Claude Desktop server name into a
// human-readable display string. We keep the original name verbatim
// so the user sees their name, capitalising the first letter when
// possible.
func deriveDisplayName(name string) string {
	if name == "" {
		return "Imported MCP server"
	}
	// Strip any leading non-alpha so display starts with a letter.
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "Imported MCP server"
	}
	first := trimmed[0]
	if first >= 'a' && first <= 'z' {
		return strings.ToUpper(string(first)) + trimmed[1:]
	}
	return trimmed
}

// WriteImportArtifacts persists one kept entry's translated YAML and
// the original pasted JSON for that entry into <DataDir>/mcp/recipes/_imports/.
// dryRun=true is a noop. The caller is responsible for the dryRun
// gate; this function does not consult any flag.
//
// Path safety: the entry's ID has already been sanitised by
// sanitiseRecipeID; we additionally double-check via filepath.Clean +
// rejecting separators / `..` so a malicious caller can't escape the
// _imports dir even by hand-crafting an ImportEntry.
//
// Returns the YAML and JSON output paths on success.
func WriteImportArtifacts(dataDir string, entry ImportEntry) (yamlPath string, jsonPath string, err error) {
	if dataDir == "" {
		return "", "", errors.New("recipes: WriteImportArtifacts: dataDir empty")
	}
	if entry.ID == "" {
		return "", "", errors.New("recipes: WriteImportArtifacts: entry ID empty")
	}
	// Defence-in-depth: reject ids that would escape the _imports dir
	// even though sanitiseRecipeID already strips path separators.
	if strings.ContainsAny(entry.ID, "/\\") || strings.Contains(entry.ID, "..") {
		return "", "", fmt.Errorf("recipes: WriteImportArtifacts: unsafe entry id %q", entry.ID)
	}
	if err := ValidateRecipeID(entry.ID); err != nil {
		return "", "", fmt.Errorf("recipes: WriteImportArtifacts: %w", err)
	}

	importsDir := filepath.Join(dataDir, userRecipesSubdir, importsSubdir)
	if err := os.MkdirAll(importsDir, 0o700); err != nil {
		return "", "", fmt.Errorf("recipes: mkdir _imports: %w", err)
	}

	yamlPath = filepath.Join(importsDir, entry.ID+".yaml")
	jsonPath = filepath.Join(importsDir, entry.ID+".json")

	// Encode recipe as YAML. The Recipe struct only carries `json:`
	// tags, so we route through JSON → generic map → YAML mirroring
	// UserStore.parseFile's strategy in reverse. This keeps the YAML
	// schema 1:1 with shipped.json without needing parallel `yaml:`
	// tags.
	yamlBytes, err := encodeRecipeYAML(entry.Recipe)
	if err != nil {
		return "", "", fmt.Errorf("recipes: encode yaml: %w", err)
	}
	if err := os.WriteFile(yamlPath, yamlBytes, 0o600); err != nil {
		return "", "", fmt.Errorf("recipes: write yaml: %w", err)
	}

	// Original JSON: pretty-print for human inspection (the user may
	// open it from the dialog's "show original" link).
	var pretty []byte
	if len(entry.OriginalJSON) > 0 {
		pretty, err = prettifyJSON(entry.OriginalJSON)
		if err != nil {
			// Fall back to raw bytes if pretty fails — not a hard
			// error since we still want to preserve what the user
			// pasted.
			pretty = entry.OriginalJSON
		}
	}
	if err := os.WriteFile(jsonPath, pretty, 0o600); err != nil {
		return "", "", fmt.Errorf("recipes: write json: %w", err)
	}
	return yamlPath, jsonPath, nil
}

// encodeRecipeYAML marshals a Recipe through JSON (for snake_case
// field tags) into a generic map, then emits YAML. Keys appear in
// json-tag order via yaml.v3's default emit ordering (which honours
// the source map's iteration order — we sort keys to keep output
// stable).
func encodeRecipeYAML(r Recipe) ([]byte, error) {
	jb, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(jb, &m); err != nil {
		return nil, err
	}
	// Drop fields that are zero / empty for cleaner human-readable
	// YAML.
	dropEmptyFields(m)
	out, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	// Prepend a small header comment so editing the file by hand
	// surfaces the import provenance.
	header := "# Translated by harness clipboard import (WP08).\n# Edit freely — your changes survive a reload via fsnotify.\n"
	return append([]byte(header), out...), nil
}

// dropEmptyFields removes zero-valued keys from m so the emitted YAML
// is concise. Zero values for the recipe schema: empty string, empty
// slice, empty map, false bool — we keep zero ints because
// init_timeout_ms=0 is a meaningful explicit-default signal.
func dropEmptyFields(m map[string]any) {
	for k, v := range m {
		switch x := v.(type) {
		case string:
			if x == "" {
				delete(m, k)
			}
		case []any:
			if len(x) == 0 {
				delete(m, k)
			}
		case map[string]any:
			if len(x) == 0 {
				delete(m, k)
			}
		case nil:
			delete(m, k)
		}
	}
}

// prettifyJSON re-formats raw with indentation. Preserves the original
// byte content semantically (encoding/json round-trip).
func prettifyJSON(raw json.RawMessage) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	var buf strings.Builder
	enc := json.NewEncoder(stringWriter{&buf})
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// stringWriter adapts a strings.Builder to io.Writer for json.NewEncoder.
type stringWriter struct{ b *strings.Builder }

func (s stringWriter) Write(p []byte) (int, error) {
	return s.b.Write(p)
}

// Compile-time assertion that stringWriter satisfies io.Writer.
var _ io.Writer = stringWriter{}

// Sentinel errors. Callers branch on errors.Is.
var (
	// ErrImportEmptyPayload is returned when the rawJSON is len-0.
	ErrImportEmptyPayload = errors.New("recipes: import: empty payload")
	// ErrImportPayloadTooLarge is returned when the rawJSON exceeds
	// MaxImportPayloadBytes (NFR-003).
	ErrImportPayloadTooLarge = errors.New("recipes: import: payload too large")
	// ErrImportInvalidJSON wraps the parser error when the outer
	// envelope fails to decode.
	ErrImportInvalidJSON = errors.New("recipes: import: invalid JSON")
	// ErrImportMissingMCPServers is returned when the parsed envelope
	// has no `mcpServers` key. Most commonly: the user pasted a
	// credential dump, a project README, or some other non-config
	// blob.
	ErrImportMissingMCPServers = errors.New("recipes: import: missing top-level \"mcpServers\" key")
)
