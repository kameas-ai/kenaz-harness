package wiring_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction/wiring"
	llmcapabilities "github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
)

// context_window_single_source_test.go is the WP10 invariant for
// agentgraph-total-convergence-01PMGX01: exactly one source of truth for
// model context windows.
//
// THE DEFECT. The harness used to carry two: the YAML catalog under
// core/llm/capabilities/data/*.yaml, and a hand-maintained
// `builtinContextWindows` map in this package. Compaction triggers fire
// off whichever copy the caller happened to reach, so the same model
// could be "80% full" on one path and "40% full" on the other, and a
// model added to one table was silently absent from the other. The
// duplicate table is gone; these tests are what stops it coming back,
// because nothing else would notice for months.
//
// Two complementary checks, because either alone is defeatable:
//
//  1. a behavioural check that the live lookup returns the catalog's
//     number for real models and preserves the documented fallback for
//     models the catalog does not know; and
//  2. a source check that no second table has been introduced.

// TestCapabilityLookup_DefersToTheYAMLCatalog walks every provider the
// catalog ships and asserts the compaction trigger's lookup agrees with
// it exactly. A reintroduced builtin table would show up here as a
// disagreement on any model whose number drifted.
func TestCapabilityLookup_DefersToTheYAMLCatalog(t *testing.T) {
	t.Parallel()
	cat, err := llmcapabilities.LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	lookup := wiring.NewCapabilityLookup()

	checked := 0
	for _, provider := range []string{"anthropic", "openai", "bedrock", "gemini", "openrouter", "ollama"} {
		for _, km := range cat.KnownModels(provider) {
			want := cat.ContextWindow(provider, km.ModelID)
			if want <= 0 {
				// The catalog has a tier entry but no window for this
				// model. The lookup must report "unknown" rather than
				// invent one.
				if got, ok := lookup.MaxContextTokens(compaction.ProviderProfileRef{
					ProviderID: provider, ModelID: km.ModelID,
				}); ok {
					t.Errorf("%s/%s: catalog has no context window but lookup returned %d — a second source is answering",
						provider, km.ModelID, got)
				}
				continue
			}
			got, ok := lookup.MaxContextTokens(compaction.ProviderProfileRef{
				ProviderID: provider, ModelID: km.ModelID,
			})
			if !ok {
				t.Errorf("%s/%s: catalog says %d but the compaction lookup reports unknown", provider, km.ModelID, want)
				continue
			}
			if got != want {
				t.Errorf("%s/%s: compaction lookup = %d, catalog = %d — two sources of truth have drifted",
					provider, km.ModelID, got, want)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatalf("checked no models; the catalog enumeration broke and this test is now vacuous")
	}
}

// TestCapabilityLookup_UnknownModelFallback pins the semantics for a
// model the catalog does not carry. This is the behaviour the deleted
// builtin table used to supply for models missing from the YAML, so it
// is exactly what a well-meaning future change might "restore".
//
// The contract is (0, false), and the engine's documented response to
// false is to SKIP its pre-flight cap check and let the provider
// surface its own context_length_exceeded. That is deliberate: a guessed
// window drives compaction to the wrong target, which is worse than not
// compacting and strictly worse than an honest provider error.
func TestCapabilityLookup_UnknownModelFallback(t *testing.T) {
	t.Parallel()
	lookup := wiring.NewCapabilityLookup()

	for _, ref := range []compaction.ProviderProfileRef{
		{ProviderID: "anthropic", ModelID: "claude-model-from-the-future-9"},
		{ProviderID: "no-such-provider", ModelID: "gpt-4o"},
		{ProviderID: "", ModelID: ""},
	} {
		if got, ok := lookup.MaxContextTokens(ref); ok {
			t.Errorf("%s: got (%d, true), want (0, false) — an unknown model must not be given a guessed window", ref, got)
		}
	}

	// The override table remains as the escape hatch for a model the
	// catalog does not know yet. That is a per-operator override, not a
	// second curated table: it starts empty and only an explicit
	// SetTable call puts anything in it.
	lookup.SetTable("no-such-provider", "gpt-4o", 4242)
	if got, ok := lookup.MaxContextTokens(compaction.ProviderProfileRef{
		ProviderID: "no-such-provider", ModelID: "gpt-4o",
	}); !ok || got != 4242 {
		t.Errorf("override table = (%d, %v), want (4242, true)", got, ok)
	}
}

// TestNoSecondContextWindowTable is the structural half: no non-test Go
// file outside the catalog package may declare a map literal keyed by
// model name and valued by a context-window-sized integer.
//
// A behavioural test cannot catch a second table that happens to agree
// today — that is precisely how the two tables coexisted for so long
// without anyone noticing. This one catches the shape.
func TestNoSecondContextWindowTable(t *testing.T) {
	t.Parallel()

	// A map literal whose values are five-or-more-digit numbers, with or
	// without Go's digit separators. Context windows are the only
	// quantity in this codebase that looks like that in a model-keyed
	// map.
	windowEntry := regexp.MustCompile(`"[a-zA-Z0-9._:/-]+"\s*:\s*[0-9][0-9_]{4,}\s*,`)

	root := filepath.Join("..", "..") // core/
	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// The catalog package is the one source of truth; its loader is
		// allowed to hold windows in memory.
		if strings.Contains(filepath.ToSlash(path), "core/llm/capabilities") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "//") {
				continue
			}
			if windowEntry.MatchString(line) {
				offenders = append(offenders, filepath.ToSlash(path)+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk core/: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("possible second context-window table (spec §6, WP10 — one source of truth is core/llm/capabilities/data/*.yaml):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// itoa avoids pulling strconv in for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
