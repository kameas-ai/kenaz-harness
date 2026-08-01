// Package ci_test exercises check-no-model-family-literals.sh
// (versioned-model-profile-01PMDL04 WP04) against small fixture trees
// rather than the real repo — the real repo already has known,
// individually model-lit-allow'd exceptions (cost tables, UI help text)
// documented at their call sites, so a fixture keeps this test focused
// on the script's actual contract: fail on an unexempted literal outside
// core/llm/**, pass on a clean tree, and treat core/llm/** and a
// same-line `// model-lit-allow:` comment as exemptions.
package ci_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runLint runs check-no-model-family-literals.sh against root and
// returns (exit code, combined stdout+stderr).
func runLint(t *testing.T, root string) (int, string) {
	t.Helper()
	scriptPath, err := filepath.Abs("check-no-model-family-literals.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", scriptPath, root)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(out)
	}
	t.Fatalf("running lint script: %v (output: %s)", err, out)
	return -1, ""
}

func writeFixtureFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestModelFamilyLint_PlantedCoreLiteral_Fails plants a hardcoded
// model-family literal in a core-logic-shaped file outside llm/** and
// verifies the lint rejects it — the acceptance criterion the WP04 spec
// calls out explicitly ("lint fails on a planted core literal").
func TestModelFamilyLint_PlantedCoreLiteral_Fails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixtureFile(t, root, "agentgraph/planted.go", `package agentgraph

func tierFromModelID(id string) string {
	if contains(id, "claude-haiku-4") {
		return "small"
	}
	return "medium"
}
`)

	code, out := runLint(t, root)
	if code == 0 {
		t.Fatalf("expected lint to fail on a planted literal, got exit 0: %s", out)
	}
	if !strings.Contains(out, "planted.go") {
		t.Errorf("expected failure output to name the offending file, got: %s", out)
	}
}

// TestModelFamilyLint_CleanTree_Passes verifies a tree with no
// model-family literals outside llm/** passes.
func TestModelFamilyLint_CleanTree_Passes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixtureFile(t, root, "agentgraph/clean.go", `package agentgraph

// Recommend picks a tier via the injected TierSource — no model-family
// string-matching lives here.
func Recommend(providerKind, modelID string, ts TierSource) (string, bool) {
	return ts.Tier(providerKind, modelID)
}
`)

	code, out := runLint(t, root)
	if code != 0 {
		t.Fatalf("expected clean tree to pass, got exit %d: %s", code, out)
	}
}

// TestModelFamilyLint_LLMPackageExempt verifies references under
// <root>/llm/** are exempt — that's where model-family/capability data
// is supposed to live (capabilities/data/*.yaml, provider adapters).
func TestModelFamilyLint_LLMPackageExempt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixtureFile(t, root, "llm/capabilities/loader.go", `package capabilities

var tiers = map[string]string{
	"claude-haiku-4": "small",
	"claude-opus-4":  "large",
	"gpt-4o-mini":    "small",
}
`)

	code, out := runLint(t, root)
	if code != 0 {
		t.Fatalf("expected llm/** references to be exempt, got exit %d: %s", code, out)
	}
}

// TestModelFamilyLint_InlineAllowComment_Exempts verifies a same-line
// `// model-lit-allow:` comment opts a line out even outside llm/**, for
// genuine non-classification references (cost tables, UI help text).
func TestModelFamilyLint_InlineAllowComment_Exempts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixtureFile(t, root, "workflows/catalog/pricing.go", `package catalog

var modelPricing = []struct {
	prefix string
	price  float64
}{
	{"claude-3-opus", 15.00}, // model-lit-allow: cost-estimation table, not classification
}
`)

	code, out := runLint(t, root)
	if code != 0 {
		t.Fatalf("expected model-lit-allow'd line to be exempt, got exit %d: %s", code, out)
	}
}

// TestModelFamilyLint_CommentOnlyMention_DoesNotFail verifies a
// mention of a model-family name inside a full-line comment (prose,
// not code) doesn't trip the lint even without an explicit exemption —
// the lint targets classification logic, not any mention of a model
// name in a doc comment.
func TestModelFamilyLint_CommentOnlyMention_DoesNotFail(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixtureFile(t, root, "agentgraph/commented.go", `package agentgraph

// This package used to guess a tier from names like claude-haiku-4 or
// gpt-4o-mini; it now asks a TierSource instead.
func Noop() {}
`)

	code, out := runLint(t, root)
	if code != 0 {
		t.Fatalf("expected a comment-only mention to pass, got exit %d: %s", code, out)
	}
}
