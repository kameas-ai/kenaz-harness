// Command checkadaptercatalog implements G-1 (model-settings-reach-the-
// model-01PMZ101 WP12, spec §7 G-1): adapter↔catalog parity, checked in
// BOTH directions.
//
// THE DEFECT CLASS THIS GATE CLOSES
// ----------------------------------
// core/llm/capabilities.Catalog keys its capability descriptors on a
// provider string (core/llm/capabilities/loader.go's
// `c.specs[spec.Provider] = &spec`, populated from each YAML's
// `provider:` field, plus a small alias table for kinds that mirror
// another provider's wire shape). core/llm/registry/registry.go
// separately registers a ProviderAdapter per Kind. Nothing before this
// gate checked that these two sets AGREE.
//
// This is not a hypothetical: it reproduces the mission's own motivating
// P0 (model-settings-reach-the-model-01PMZ101 spec §1.1) — azure-openai
// and custom-openai were registered adapter kinds with NO capability-
// catalog entry, so the gate that decides whether a tool-bearing request
// is refused fell into the unknown-provider branch and refused every
// tool call and every image attachment. That defect shipped and was only
// found by a manual audit. This gate exists so the NEXT adapter added
// without a catalog entry fails CI instead of shipping silently — this
// is precisely the gate that would have caught this mission's own
// motivating P0, and its absence reproduces roadmap finding P-7 (a
// landed banner claiming a test that does not exist).
//
// It also checks the REVERSE direction: a catalog YAML file whose
// `provider:` matches no registered adapter kind and no alias target is
// dead data nobody's gate resolves against — this is what makes
// ollama.yaml visible before WP13 registered a real `ollama` adapter
// kind (E-001, spec §11).
//
// HOW IT RESOLVES "REGISTERED"
// -----------------------------
// core/llm/registry/registry.go registers adapters as
// `r.adapters[<pkg>.Kind] = <pkg>.New()`, sometimes behind a runtime
// feature flag (`if azure.AzureOpenAIEnabled() { ... }`,
// `if gemini.IsEnabled() { ... }`, `if ca := custom.New(); ca != nil {
// ... }`). THREE of the seven-plus registrations are conditional this
// way (spec §13 item 5's stated hazard) — a parser that only sees
// unconditional top-level assignments misses exactly the kinds this
// mission is about. This tool does not attempt to understand control
// flow at all: it greps every `r.adapters[<pkg>.Kind] = ` occurrence in
// the file regardless of the indentation or braces around it, which
// trivially includes conditional registrations (their weight is exactly
// the source text, not the control-flow path that reaches them).
//
// <pkg> is then resolved to an import path via registry.go's own import
// block (so a future rename of an import alias is followed, not
// hardcoded), and the literal `const Kind = "..."` is read out of that
// package's own non-test .go source — never trusted from registry.go's
// call-site text, which only has the SHORT package identifier, not the
// registered string.
//
// DISCOVERY FLOORS
// -----------------
// Zero registered adapters or zero catalog YAML files is a tool failure
// (a broken scan reporting a false "clean"), not a pass — see requireNonEmpty.
//
// ALLOWLIST
// ---------
// scripts/ci/allowlists/i20-adapter-catalog-parity.txt. One bare provider
// string per line, followed by a `#` comment naming the DATE, the
// BLOCKER and the OWNER. Allowlists shrink monotonically (CLAUDE.md); a
// line that no longer corresponds to a live violation is STALE and this
// tool fails the build over it, exactly like every other gate in this
// directory.
//
// Exit codes:
//
//	0 — every registered adapter kind resolves to a catalog entry (direct
//	    or aliased) and every catalog file is reachable from a registered
//	    kind (direct or as an alias target), modulo the allowlist; the
//	    allowlist itself has no stale entries.
//	1 — the scan itself failed (missing file, zero adapters/files found,
//	    malformed alias table, etc.) — a tool defect, not a finding.
//	2 — at least one real violation (unregistered catalog file, or
//	    registered kind with no resolution) was found and is not
//	    allowlisted, OR the allowlist has a stale entry.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	registryFile  = "core/llm/registry/registry.go"
	loaderFile    = "core/llm/capabilities/loader.go"
	catalogDir    = "core/llm/capabilities/data"
	allowlistPath = "scripts/ci/allowlists/i20-adapter-catalog-parity.txt"
	modulePrefix  = "github.com/kameas-ai/kenaz-harness/"

	// overlayEnvVar names a JSON file (shape: {"Replace": {"<absolute
	// real path>": "<scratch path>"}}) that lets gates_can_fail_test.go
	// plant a violation without writing to the real tree — same
	// mechanism checknilopts's NIL_OPTIONAL_DEPS_OVERLAY already
	// established for this class of test.
	overlayEnvVar = "ADAPTER_CATALOG_OVERLAY"
)

// overlay maps an absolute real path to the scratch file that should be
// read instead. Populated once in main() from overlayEnvVar, if set.
var overlay map[string]string

// readFile is os.ReadFile with overlay awareness: path is resolved to an
// absolute path (repo-root-relative paths are used throughout this
// tool) and, if that absolute path is a key in overlay, the SCRATCH
// file's content is returned instead of the real file's.
func readFile(path string) ([]byte, error) {
	if len(overlay) > 0 {
		if abs, err := filepath.Abs(path); err == nil {
			if scratch, ok := overlay[abs]; ok {
				return os.ReadFile(scratch)
			}
		}
	}
	return os.ReadFile(path)
}

func loadOverlay() (map[string]string, error) {
	path := os.Getenv(overlayEnvVar)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s=%s: %w", overlayEnvVar, path, err)
	}
	var doc struct {
		Replace map[string]string `json:"Replace"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s=%s: %w", overlayEnvVar, path, err)
	}
	return doc.Replace, nil
}

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[adapter-catalog-parity] FAIL: could not resolve repo root:", err)
		os.Exit(1)
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintln(os.Stderr, "[adapter-catalog-parity] FAIL:", err)
		os.Exit(1)
	}

	overlay, err = loadOverlay()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[adapter-catalog-parity] FAIL:", err)
		os.Exit(1)
	}

	importPaths, err := parseImports(registryFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[adapter-catalog-parity] FAIL: parsing imports:", err)
		os.Exit(1)
	}

	registeredKindList, err := parseRegistrations(registryFile, importPaths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[adapter-catalog-parity] FAIL: parsing registrations:", err)
		os.Exit(1)
	}
	{
		names := make([]string, len(registeredKindList))
		for i, r := range registeredKindList {
			names[i] = r.kind
		}
		if err := requireNonEmpty("registered adapter kinds", names); err != nil {
			fmt.Fprintln(os.Stderr, "[adapter-catalog-parity] FAIL:", err)
			os.Exit(1)
		}
	}

	registeredKinds := map[string]string{} // kind string -> source description (for error messages)
	for _, r := range registeredKindList {
		registeredKinds[r.kind] = r.source
	}

	catalogProviders, err := parseCatalogProviders(catalogDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[adapter-catalog-parity] FAIL: parsing catalog data:", err)
		os.Exit(1)
	}
	if err := requireNonEmpty("catalog YAML files", keysOf(catalogProviders)); err != nil {
		fmt.Fprintln(os.Stderr, "[adapter-catalog-parity] FAIL:", err)
		os.Exit(1)
	}

	aliases, err := parseProviderAlias(loaderFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[adapter-catalog-parity] FAIL: parsing providerAlias:", err)
		os.Exit(1)
	}

	allowlist, err := loadAllowlist(allowlistPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[adapter-catalog-parity] FAIL: reading allowlist:", err)
		os.Exit(1)
	}
	used := map[string]bool{}

	var violations []string

	// Direction 1: every registered kind resolves to a catalog entry,
	// directly or through the alias table.
	for kind, pkg := range registeredKinds {
		if catalogProviders[kind] {
			continue
		}
		if target, ok := aliases[kind]; ok && catalogProviders[target] {
			continue
		}
		if allowlist[kind] {
			used[kind] = true
			continue
		}
		violations = append(violations, fmt.Sprintf(
			"registered adapter kind %q (package %s) has NO capability-catalog entry "+
				"(no core/llm/capabilities/data/*.yaml with provider: %s, and no providerAlias target) — "+
				"a tool-bearing request on this kind falls into Catalog's unknown-provider branch and is refused. "+
				"Add a catalog YAML, an alias table entry, or a dated line in %s.",
			kind, pkg, kind, allowlistPath))
	}

	// Direction 2: every catalog file is reachable from some registered
	// kind, directly or as an alias TARGET.
	aliasTargets := map[string]bool{}
	for _, target := range aliases {
		aliasTargets[target] = true
	}
	for provider := range catalogProviders {
		if registeredKinds[provider] != "" {
			continue
		}
		if aliasTargets[provider] {
			continue
		}
		if allowlist[provider] {
			used[provider] = true
			continue
		}
		violations = append(violations, fmt.Sprintf(
			"capability-catalog file for provider %q matches NO registered adapter kind "+
				"and is not an alias target — this data ships in the binary but nothing ever "+
				"resolves it (this is the class that left ollama.yaml unreachable before WP13 "+
				"registered a real \"ollama\" adapter kind). Register the adapter kind, alias an "+
				"existing one onto it, or add a dated line in %s.",
			provider, allowlistPath))
	}

	var stale []string
	for entry := range allowlist {
		if !used[entry] {
			stale = append(stale, entry)
		}
	}
	sort.Strings(stale)
	for _, s := range stale {
		violations = append(violations, fmt.Sprintf(
			"allowlist entry %q in %s no longer corresponds to a live violation — "+
				"allowlists shrink monotonically (CLAUDE.md); remove the stale line.",
			s, allowlistPath))
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, "[adapter-catalog-parity] FAIL:", v)
		}
		os.Exit(2)
	}

	fmt.Printf("[adapter-catalog-parity] clean — %d registered kinds, %d catalog files, %d aliases.\n",
		len(registeredKinds), len(catalogProviders), len(aliases))
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			return "", err
		}
		return wd, nil
	}
	return strings.TrimSpace(string(out)), nil
}

func requireNonEmpty(what string, items []string) error {
	if len(items) == 0 {
		return fmt.Errorf("found ZERO %s — this is a broken scan, not a clean bill of health "+
			"(a gate that inspects nothing must not pass)", what)
	}
	return nil
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// importLineRe matches both `"path"` and `alias "path"` import lines.
var importLineRe = regexp.MustCompile(`^\s*(?:(\w+)\s+)?"([^"]+)"\s*$`)

// parseImports returns import-alias (or last path segment when
// unaliased) -> full import path, for file's single import block.
func parseImports(file string) (map[string]string, error) {
	data, err := readFile(file)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	out := map[string]string{}
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inBlock {
			if trimmed == "import (" {
				inBlock = true
			}
			continue
		}
		if trimmed == ")" {
			break
		}
		m := importLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		alias, path := m[1], m[2]
		name := alias
		if name == "" {
			segs := strings.Split(path, "/")
			name = segs[len(segs)-1]
		}
		out[name] = path
	}
	return out, nil
}

// registration is one resolved (kind string, human-readable source)
// pair, from either registration shape parseRegistrations recognises.
type registration struct {
	kind   string
	source string
}

// pkgFormRe matches `r.adapters[<pkg>.Kind] = ` regardless of
// surrounding indentation or control flow (deliberately blind to
// conditionals — see the package doc comment): three of the eight
// registrations in this file are behind runtime feature flags, and a
// parser that only sees unconditional assignments would miss exactly
// the kinds this mission is about.
var pkgFormRe = regexp.MustCompile(`r\.adapters\[(\w+)\.Kind\]\s*=`)

// literalFormRe matches the (currently hypothetical, but valid Go)
// `r.adapters["some-literal-kind"] = ...` shape — a registration whose
// map key is a bare string literal rather than an imported package's
// Kind constant. Recognising this shape too is what lets a planted
// violation like `r.adapters["zz-gate-probe"] = openai.New()` (no
// backing package, no catalog entry) actually reach direction 1's check
// instead of silently vanishing because it doesn't match the `.Kind`
// shape every real registration in this file uses today.
var literalFormRe = regexp.MustCompile(`r\.adapters\[\s*"([^"]+)"\s*\]\s*=`)

// parseRegistrations returns one registration per DISTINCT kind found in
// file, resolving the `<pkg>.Kind` shape through importPaths + the
// package's own `const Kind = "..."` declaration, and taking the
// `"literal"` shape at face value.
func parseRegistrations(file string, importPaths map[string]string) ([]registration, error) {
	data, err := readFile(file)
	if err != nil {
		return nil, err
	}
	text := string(data)
	seen := map[string]bool{}
	var out []registration

	for _, m := range pkgFormRe.FindAllStringSubmatch(text, -1) {
		pkg := m[1]
		importPath, ok := importPaths[pkg]
		if !ok {
			return nil, fmt.Errorf("registry.go registers r.adapters[%s.Kind] but %q is not imported in %s", pkg, pkg, file)
		}
		dir := strings.TrimPrefix(importPath, modulePrefix)
		kind, kerr := findKindConst(dir)
		if kerr != nil {
			return nil, fmt.Errorf("resolving %s.Kind (dir %s): %w", pkg, dir, kerr)
		}
		if !seen[kind] {
			seen[kind] = true
			out = append(out, registration{kind: kind, source: fmt.Sprintf("package %s (%s)", pkg, dir)})
		}
	}

	for _, m := range literalFormRe.FindAllStringSubmatch(text, -1) {
		kind := m[1]
		if !seen[kind] {
			seen[kind] = true
			out = append(out, registration{kind: kind, source: "string literal in " + file})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].kind < out[j].kind })
	return out, nil
}

var kindConstRe = regexp.MustCompile(`(?m)^const\s+Kind\s*=\s*"([^"]+)"`)

// findKindConst greps every non-test .go file directly inside dir (one
// level, matching check-codegen.sh's own documented "one level deep"
// precision tradeoff) for `const Kind = "..."` and returns the literal.
func findKindConst(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := readFile(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		if m := kindConstRe.FindStringSubmatch(string(data)); m != nil {
			return m[1], nil
		}
	}
	return "", fmt.Errorf("no `const Kind = \"...\"` found in any non-test .go file")
}

var providerLineRe = regexp.MustCompile(`(?m)^provider:\s*(\S+)\s*$`)

func parseCatalogProviders(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := readFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		m := providerLineRe.FindStringSubmatch(string(data))
		if m == nil {
			return nil, fmt.Errorf("%s: no top-level `provider:` key found", e.Name())
		}
		out[strings.Trim(m[1], `"'`)] = true
	}
	return out, nil
}

var aliasEntryRe = regexp.MustCompile(`"([^"]+)"\s*:\s*"([^"]+)"\s*,?`)

// parseProviderAlias extracts the `var providerAlias = map[string]string{
// ... }` block from loaderFile. Returns an empty (non-nil) map, not an
// error, if the block is missing — an empty alias table is a legitimate
// state this tool must not treat as a parse failure.
func parseProviderAlias(file string) (map[string]string, error) {
	data, err := readFile(file)
	if err != nil {
		return nil, err
	}
	text := string(data)
	start := strings.Index(text, "var providerAlias = map[string]string{")
	out := map[string]string{}
	if start == -1 {
		return out, nil
	}
	rest := text[start:]
	end := strings.Index(rest, "}")
	if end == -1 {
		return nil, fmt.Errorf("providerAlias map literal has no closing brace")
	}
	block := rest[:end]
	for _, m := range aliasEntryRe.FindAllStringSubmatch(block, -1) {
		out[m[1]] = m[2]
	}
	return out, nil
}

// loadAllowlist reads one bare provider-string entry per non-comment,
// non-blank line (leading/trailing whitespace trimmed; a `#` anywhere on
// the line starts a comment that is stripped before trimming, mirroring
// the other allowlists in this directory).
func loadAllowlist(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out[line] = true
	}
	return out, nil
}
