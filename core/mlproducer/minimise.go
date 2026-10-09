package mlproducer

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// Built-in tool names this package reasons about. Copied from
// core/tools/fsbuiltins/fsbuiltins.go:46-52, core/tools/bash, webfetch and
// websearch rather than imported (those packages drag cedar and the fs
// gate in); minimise_test.go pins them against the real constants.
const (
	toolReadFile  = "kenaz__read_file"
	toolListDir   = "kenaz__list_dir"
	toolGlob      = "kenaz__glob"
	toolGrep      = "kenaz__grep"
	toolWriteFile = "kenaz__write_file"
	toolEditFile  = "kenaz__edit_file"
	toolBash      = "kenaz__bash"
	toolWebFetch  = "kenaz__web_fetch"
	toolWebSearch = "kenaz__web_search"
)

// builtinPrefix is the server segment reserved for built-in tools.
const builtinPrefix = "kenaz__"

// ServerClassifier says whether an MCP server name is user-chosen: a
// custom recipe (recipes.Source user / imported). Such names are sent as
// custom__h(server) so a person's private naming does not leave the
// device (spec §3.2). WP03 implements it over core/mcp/recipes; nil
// treats every server as a catalog server.
type ServerClassifier interface {
	IsCustomServer(server string) bool
}

// ServerClassifierFunc adapts a func to ServerClassifier.
type ServerClassifierFunc func(server string) bool

// IsCustomServer implements ServerClassifier.
func (f ServerClassifierFunc) IsCustomServer(server string) bool { return f(server) }

var safeToolName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// minimiser bundles the keyed hash and the server classifier.
type minimiser struct {
	hasher  *Hasher
	servers ServerClassifier
}

// h is the hash or "" on a key error (callers check before recording).
func (m minimiser) h(x string) string {
	v, err := m.hasher.H(x)
	if err != nil {
		return ""
	}
	return v
}

// toolName returns the exportable tool name: built-ins and catalog MCP
// tools verbatim (server__tool), custom servers as custom__h(server)__tool,
// and any name that is not a plain identifier (a model-invented string
// that reached dispatch) as invalid__h(name).
func (m minimiser) toolName(name string) string {
	if !safeToolName.MatchString(name) {
		return "invalid__" + m.h(name)
	}
	if strings.HasPrefix(name, builtinPrefix) {
		return name
	}
	server, tool, ok := strings.Cut(name, "__")
	if !ok || server == "" || tool == "" {
		return name
	}
	if m.servers != nil && m.servers.IsCustomServer(server) {
		return "custom__" + m.h(server) + "__" + tool
	}
	return name
}

var safeExt = regexp.MustCompile(`^[A-Za-z0-9]{1,12}$`)

// pathToken returns h(abs)+"."+ext for an absolute path (spec §3.2); ext
// is lower-cased and dropped when it is not a short alphanumeric suffix.
// A path with no extension is h(abs) alone.
func (m minimiser) pathToken(abs string) string {
	hv := m.h(abs)
	ext := strings.TrimPrefix(filepath.Ext(abs), ".")
	if ext == "" || !safeExt.MatchString(ext) {
		return hv
	}
	return hv + "." + strings.ToLower(ext)
}

// absPath resolves a tool's path argument against the workspace.
func absPath(p, workspace string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		if workspace == "" {
			return ""
		}
		p = filepath.Join(workspace, p)
	}
	return filepath.Clean(p)
}

// credentialish matches tokens that look like a secret: well-known key
// prefixes, a JWT, or a long mixed letter+digit run.
var credentialPrefixes = []string{
	"sk-", "sk_", "pk_", "rk_", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_",
	"xoxa-", "xoxb-", "xoxp-", "xoxr-", "xoxs-", "glpat-", "akia", "asia", "aiza", "eyj",
	"ya29.", "npm_", "pypi-", "hf_", "dop_v1_", "shpat_", "sq0atp-",
}

func looksCredential(tok string) bool {
	t := strings.Trim(tok, `"'`)
	lower := strings.ToLower(t)
	for _, p := range credentialPrefixes {
		if strings.HasPrefix(lower, p) && len(t) > len(p)+3 {
			return true
		}
	}
	if strings.Contains(t, "=") || strings.Contains(t, "@") || strings.Contains(t, ":") {
		// assignments, --token=…, user@host, scheme:… / user:pass
		return true
	}
	if len(t) >= 20 {
		var letters, digits int
		for _, r := range t {
			switch {
			case r >= '0' && r <= '9':
				digits++
			case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
				letters++
			}
		}
		if letters > 0 && digits > 0 {
			return true
		}
	}
	return false
}

// fileNameToken matches a bare file name with an extension (app.py,
// plan.md): a relative path, so it is hashed like one.
var fileNameToken = regexp.MustCompile(`^[^-][^=]*\.[A-Za-z0-9]{1,12}$`)

// envAssign matches a leading shell env assignment (FOO=bar cmd ...).
var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// commandTokens splits a command line on whitespace and drops leading
// env assignments (they configure the command, they are not it — and are
// where inline credentials live).
func commandTokens(cmd string) []string {
	toks := strings.Fields(cmd)
	for len(toks) > 0 && envAssign.MatchString(toks[0]) {
		toks = toks[1:]
	}
	return toks
}

// cmdPrefix is the `cmd` payload value: the first two whitespace tokens
// (spec §3.2), each additionally minimised — a token carrying a path
// separator becomes its hash (+ext), a credential-looking one becomes
// "[redacted]". Full command lines never leave the device (§2).
func (m minimiser) cmdPrefix(cmd string) string {
	toks := commandTokens(cmd)
	if len(toks) > 2 {
		toks = toks[:2]
	}
	out := make([]string, len(toks))
	for i, t := range toks {
		switch {
		case looksCredential(t):
			out[i] = "[redacted]"
		case strings.ContainsAny(t, `/\`) || fileNameToken.MatchString(t):
			// a path, or a bare file name (a relative path in the cwd)
			out[i] = m.pathToken(t)
		default:
			out[i] = t
		}
	}
	return strings.Join(out, " ")
}

// parseArgs decodes the dispatched argument JSON; nil on anything else.
func parseArgs(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}

func stringArg(args map[string]any, key string) string {
	if s, ok := args[key].(string); ok {
		return s
	}
	return ""
}

// bashExitCode parses kenaz__bash's result JSON ({"exit_code":N,...}).
// ok is false for a background spawn result ({"task_id","status"}), a
// truncated / non-JSON result, or one without the field.
func bashExitCode(result string) (int, bool) {
	var r struct {
		ExitCode *int `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(result), &r); err != nil || r.ExitCode == nil {
		return 0, false
	}
	return *r.ExitCode, true
}

// bashNotRun reads kenaz__bash's explicit no-process markers: refused
// (its own gate said no — Cedar, allowlist, working-dir sandbox, a
// declined prompt) and not_run (secret resolution or background spawn
// failure). exit_code -1 alone is NOT the signal: a real process can
// report it too.
func bashNotRun(result string) (refused, notRun bool) {
	var r struct {
		Refused bool `json:"refused"`
		NotRun  bool `json:"not_run"`
	}
	if err := json.Unmarshal([]byte(result), &r); err != nil {
		return false, false
	}
	return r.Refused, r.NotRun
}

// resultFlagsError reports a fsbuiltins-style {"is_error":true,...} result
// — those tools return it as a successful dispatch (err == nil).
func resultFlagsError(result string) bool {
	t := strings.TrimSpace(result)
	if !strings.HasPrefix(t, "{") {
		return false
	}
	var r struct {
		IsError bool `json:"is_error"`
	}
	if err := json.Unmarshal([]byte(t), &r); err != nil {
		return false
	}
	return r.IsError
}

// isFSBuiltin reports whether name is one of the fsbuiltins tools.
func isFSBuiltin(name string) bool {
	switch name {
	case toolReadFile, toolListDir, toolGlob, toolGrep, toolWriteFile, toolEditFile:
		return true
	}
	return false
}

// isFileWrite reports whether name writes or edits a file.
func isFileWrite(name string) bool {
	return name == toolWriteFile || name == toolEditFile
}
