package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RuntimePrereq single runtime dependency recipe requires in
// $PATH before spawning. Name user-visible name (e.g. "uv"); Cmds
// lists one or more candidate binary names probe via exec.LookPath (the
// first hit satisfies requirement). InstallHint brief human-readable
// one-liner pointing user install missing runtime.
type RuntimePrereq struct {
	Name        string // human name, e.g. "uv"
	Cmds        []string // candidate binary names LookPath
	InstallHint string // how install if missing
}

// FileSetupGuide describes the manual steps to place a required file.
// It is embedded in a MissingPrereq when Kind == "file" so the frontend
// can render a concrete guided-setup section rather than raw CLI advice.
type FileSetupGuide struct {
	// TargetPath is the absolute (post-expansion) file path that must
	// exist. The frontend surfaces this as the destination path in the
	// guided UI and as the default location for the native file picker.
	TargetPath string `json:"target_path"`
	// Steps is an ordered list of human-readable setup steps (rendered
	// as a numbered list in the modal). Each step is a short imperative
	// sentence; URLs may appear inline.
	Steps []string `json:"steps"`
	// DocsURL is an optional link to provider documentation. Empty when
	// no canonical reference URL is available.
	DocsURL string `json:"docs_url,omitempty"`
}

// MissingPrereq describes one runtime dependency could not resolved.
type MissingPrereq struct {
	// Name human-readable runtime name (e.g. "uv", "npx", or for file
	// prereqs a short description like "Gmail OAuth credentials file").
	Name string `json:"name"`
	// InstallHint short how-to-get-it hint (e.g. "install via
	// https://astral.sh/uv or 'curl -LsSf https://astral.sh/uv/install.sh | sh'").
	// For file prereqs this is a brief one-liner; the full guided steps
	// are in FileSetupGuide.
	InstallHint string `json:"install_hint"`
	// Kind classifies the prereq type. Empty string (or absent) means
	// "runtime" (the original binary-in-PATH variant). "file" means a
	// required file is absent from the filesystem — the frontend renders
	// a guided setup section rather than a generic install hint.
	Kind string `json:"kind,omitempty"`
	// FileSetupGuide is populated when Kind == "file". It carries the
	// full guided-setup details (target path, ordered steps, docs URL)
	// so the frontend can render an in-app onboarding flow instead of
	// surfacing raw CLI advice.
	FileSetupGuide *FileSetupGuide `json:"file_setup_guide,omitempty"`
}

// knownPrereqs maps first element recipe Command prereq
// implies. We intentionally only map canonical first-element commands; custom
// recipes arbitrary first elements not pre-flighted.
var knownPrereqs = map[string]RuntimePrereq{
	"uvx": {
		Name:        "uv / uvx",
		Cmds:        []string{"uvx", "uv"},
		InstallHint: "install uv: https://astral.sh/uv (macOS: brew install uv)",
	},
	"uv": {
		Name:        "uv",
		Cmds:        []string{"uv"},
		InstallHint: "install uv: https://astral.sh/uv (macOS: brew install uv)",
	},
	"npx": {
		Name:        "Node.js / npx",
		Cmds:        []string{"npx", "node"},
		InstallHint: "install Node.js: https://nodejs.org (macOS: brew install node)",
	},
	"node": {
		Name:        "Node.js",
		Cmds:        []string{"node"},
		InstallHint: "install Node.js: https://nodejs.org (macOS: brew install node)",
	},
}

// recipeFilePrereqs maps recipe IDs to file-based prerequisites that must
// exist on the filesystem before the server is allowed to spawn. Each entry
// describes the required file path (after ~ expansion) and the guided setup
// steps surfaced in the install modal.
//
// Unlike binary-in-PATH prereqs (which are inferred from Command[0]),
// file prereqs are keyed by recipe ID because the requirement is implicit in
// the server's runtime behaviour — not visible from the command argv.
//
// KAMEAS_GOOGLE_OAUTH_CLIENT_ID config seam:
// TODO: Once a Kameas-registered Google OAuth client completes restricted-
// scope verification (Gmail + CASA tier 2) and is approved by Google, the
// gmail-autoauth server can be bundled with its own client credentials,
// removing the need for the user to supply gcp-oauth.keys.json entirely.
// At that point, remove the "gmail" entry from this map and set the env var
// KAMEAS_GOOGLE_OAUTH_CLIENT_ID to the registered client ID at build time.
// Tracked: blocked on Google restricted-scope review + CASA; do NOT
// implement until Google approval lands.
var recipeFilePrereqs = map[string]MissingPrereq{
	"gmail": {
		Name:        "Gmail OAuth credentials file",
		InstallHint: "create a Google Cloud OAuth client and save the credentials JSON to ~/.gmail-mcp/gcp-oauth.keys.json",
		Kind:        "file",
		FileSetupGuide: &FileSetupGuide{
			TargetPath: gmailCredsPath(),
			Steps: []string{
				"Open the Google Cloud Console (console.cloud.google.com) and create or select a project.",
				"Enable the Gmail API: navigate to APIs & Services → Enable APIs and Services → search for \"Gmail API\" → Enable.",
				"Create an OAuth 2.0 client: go to APIs & Services → Credentials → Create Credentials → OAuth client ID.",
				"Select \"Desktop app\" as the application type, give it a name, and click Create.",
				"On the credential detail page, click \"Download JSON\" to save the file.",
				"Use the file picker below to place the downloaded file into ~/.gmail-mcp/ — the harness will rename it to gcp-oauth.keys.json automatically.",
			},
			DocsURL: "https://developers.google.com/gmail/api/quickstart/go#authorize_credentials_for_a_desktop_application",
		},
	},
}

// gmailCredsPath returns the expected absolute path for the Gmail OAuth
// credentials file, expanding ~ to the user's home directory. Falls back
// to the literal "~/.gmail-mcp/gcp-oauth.keys.json" if home cannot be
// resolved (the file-picker stub will still work in tests).
func gmailCredsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/.gmail-mcp/gcp-oauth.keys.json"
	}
	return filepath.Join(home, ".gmail-mcp", "gcp-oauth.keys.json")
}

// checkFilePrereqs looks up any file-based prerequisite registered for
// recipeID and returns a MissingPrereq if the required file is absent.
// Returns nil when no file prereq is registered or the file exists.
// Exposed as a var so tests can stub the stat call and the registry.
var checkFilePrereqs = func(recipeID string) *MissingPrereq {
	fp, ok := recipeFilePrereqs[recipeID]
	if !ok {
		return nil
	}
	targetPath := fp.FileSetupGuide.TargetPath
	if _, err := os.Stat(targetPath); err == nil {
		// File exists — prereq satisfied.
		return nil
	}
	// Return a copy with the resolved target path (already absolute).
	result := fp
	return &result
}

// CheckPrereqs inspects command looking for missing runtime dependencies in
// $PATH. Returns a (possibly empty) slice of MissingPrereq describing every
// unsatisfied runtime prerequisite found. The recipeID parameter is optional
// (pass "" for commands without a recipe context); when non-empty, file-based
// prereqs registered in recipeFilePrereqs are also checked.
//
// Custom / built-in servers are not pre-flighted (only commands whose first
// element appears in knownPrereqs are checked for $PATH membership).
func CheckPrereqs(command []string, recipeID ...string) []MissingPrereq {
	var result []MissingPrereq

	// Runtime (binary-in-PATH) check.
	if len(command) > 0 {
		// Only first element significant prereq lookup.
		first := command[0]
		// Strip any path prefix (e.g. "/usr/local/bin/npx" → "npx").
		if idx := strings.LastIndexByte(first, '/'); idx >= 0 {
			first = first[idx+1:]
		}
		if prereq, known := knownPrereqs[first]; known {
			// Check any candidate binaries be resolved.
			found := false
			for _, cmd := range prereq.Cmds {
				if _, err := exec.LookPath(cmd); err == nil {
					found = true
					break
				}
			}
			if !found {
				result = append(result, MissingPrereq{
					Name:        prereq.Name,
					InstallHint: prereq.InstallHint,
				})
			}
		}
	}

	// File-based prereq check (only when a recipe ID is supplied).
	if len(recipeID) > 0 && recipeID[0] != "" {
		if fp := checkFilePrereqs(recipeID[0]); fp != nil {
			result = append(result, *fp)
		}
	}

	return result
}

// PrereqError formats human-readable error message slice missing
// prereqs suitable surfacing in install dialog or RPC error.
func PrereqError(missing []MissingPrereq) error {
	if len(missing) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("missing runtime(s) required run recipe:\n")
	for _, m := range missing {
		fmt.Fprintf(&b, " • %s — %s\n", m.Name, m.InstallHint)
	}
	return fmt.Errorf("%s", b.String())
}
