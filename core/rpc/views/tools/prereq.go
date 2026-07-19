package tools

import (
	"fmt"
	"os/exec"
	"strings"
)

// RuntimePrereq is a single runtime dependency that a recipe requires in
// $PATH before spawning. Name is the user-visible name (e.g. "uv"); Cmds
// lists one or more candidate binary names to probe via exec.LookPath (the
// first hit satisfies the requirement). InstallHint is a brief human-readable
// one-liner pointing the user at how to install the missing runtime.
type RuntimePrereq struct {
	Name        string   // human name, e.g. "uv"
	Cmds        []string // candidate binary names to LookPath
	InstallHint string   // how to install if missing
}

// MissingPrereq describes one runtime dependency that could not be resolved.
type MissingPrereq struct {
	// Name is the human-readable runtime name (e.g. "uv", "npx").
	Name string `json:"name"`
	// InstallHint is a short how-to-get-it hint (e.g. "install via
	// https://astral.sh/uv or 'curl -LsSf https://astral.sh/uv/install.sh | sh'").
	InstallHint string `json:"install_hint"`
}

// knownPrereqs maps the first element of a recipe Command to the prereq it
// implies. We intentionally only map canonical first-element commands; custom
// recipes with arbitrary first elements are not pre-flighted.
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
		InstallHint: "install Node.js: https://nodejs.org or 'brew install node'",
	},
	"node": {
		Name:        "Node.js",
		Cmds:        []string{"node"},
		InstallHint: "install Node.js: https://nodejs.org or 'brew install node'",
	},
}

// CheckPrereqs inspects the recipe's command and returns any runtimes that
// are not present in $PATH. Returns nil when all runtimes are available, when
// the command is empty, or when the first element is not in the known
// set (custom / built-in servers are not pre-flighted).
func CheckPrereqs(command []string) []MissingPrereq {
	if len(command) == 0 {
		return nil
	}
	// Only the first element is significant for prereq lookup.
	first := command[0]
	// Strip any path prefix (e.g. "/usr/local/bin/npx" → "npx").
	if idx := strings.LastIndexByte(first, '/'); idx >= 0 {
		first = first[idx+1:]
	}
	prereq, known := knownPrereqs[first]
	if !known {
		return nil
	}
	// Check whether any of the candidate binaries can be resolved.
	for _, cmd := range prereq.Cmds {
		if _, err := exec.LookPath(cmd); err == nil {
			return nil // found; prereq satisfied
		}
	}
	return []MissingPrereq{{
		Name:        prereq.Name,
		InstallHint: prereq.InstallHint,
	}}
}

// PrereqError formats a human-readable error message from a slice of missing
// prereqs suitable for surfacing in the install dialog or RPC error.
func PrereqError(missing []MissingPrereq) error {
	if len(missing) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("missing runtime(s) required to run this recipe:\n")
	for _, m := range missing {
		fmt.Fprintf(&b, "  • %s — %s\n", m.Name, m.InstallHint)
	}
	return fmt.Errorf("%s", b.String())
}
