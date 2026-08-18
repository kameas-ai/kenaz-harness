package mcp

// wp04_watcher_deletion_test.go — mcp-connector-lifecycle-01PMMC01 WP04.
//
// WP04 deleted recipes.UserStore's fsnotify watcher (StartWatch,
// watchLoop, the watcher arm of Close, ErrAlreadyWatching) after WP03
// made every merged-catalog consumer reload the store from disk on
// every call, leaving no cache for a watcher's onChange to invalidate.
// Deletion is "self-proving" in the sense that a re-added StartWatch call
// site would just fail to compile against the deleted method — but the
// LOAD-BEARING artefact the tasks called out is this package's own
// import.go docstring, which used to assert a working watcher
// ("UserStore.Reload (or the fsnotify watcher) picks up the new
// _imports/<id>.yaml on the next event tick"). That claim is prose, not
// code — nothing stops someone from typing it back in. This test is the
// guard: it fails if either the watcher symbol reappears anywhere under
// core/ (outside the unrelated core/contexts and core/fswatch watchers,
// which are a different subsystem entirely and were never part of this
// mission), or if import.go's docstring re-acquires the word "watcher".
import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// startWatchWordRE matches the exact symbol StartWatch (call, method
// reference, or declaration) but NOT unrelated identifiers that merely
// contain it as a substring — core/contexts's own (different, legitimate)
// corecontexts.Library.StartWatching is one such neighbour.
var startWatchWordRE = regexp.MustCompile(`\bStartWatch\b`)

// repoRootForWatcherCheck resolves the repository root from this test
// file's own path, so the test works regardless of the working directory
// `go test` is invoked from.
func repoRootForWatcherCheck(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// here = <root>/core/rpc/views/mcp/wp04_watcher_deletion_test.go
	root := filepath.Join(filepath.Dir(here), "..", "..", "..", "..")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		t.Fatalf("resolved root %q has no go.mod — path arithmetic is wrong: %v", abs, err)
	}
	return abs
}

// TestWP04_StartWatchNotReintroduced walks core/ (excluding the
// unrelated core/contexts and core/fswatch fsnotify watchers, and the
// vendor/build directories) and fails if "StartWatch" appears anywhere
// outside a comment describing its deletion.
//
// Mutation: paste recipes.UserStore's deleted StartWatch method back
// into core/mcp/recipes/user.go (verbatim, as production code — not a
// comment). This test must fail.
func TestWP04_StartWatchNotReintroduced(t *testing.T) {
	root := repoRootForWatcherCheck(t)
	coreDir := filepath.Join(root, "core")

	var offenders []string
	err := filepath.WalkDir(coreDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == "contexts" || base == "fswatch" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if d.Name() == "wp04_watcher_deletion_test.go" {
			// This file's own source necessarily contains the literal
			// string "StartWatch" in code (not comments) — it is the
			// scanner, not a reintroduction of the deleted method.
			return nil
		}
		data, rerr := os.ReadFile(path) // #nosec G304 — path is walked from repo root, test-only
		if rerr != nil {
			return rerr
		}
		text := string(data)
		if !startWatchWordRE.MatchString(text) {
			return nil
		}
		// Every surviving reference must be prose (inside a `//` comment
		// line) documenting the deletion, not a live call site or
		// declaration. Check each line that mentions it.
		for _, line := range strings.Split(text, "\n") {
			if !startWatchWordRE.MatchString(line) {
				continue
			}
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "//") {
				offenders = append(offenders, path+": "+trimmed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", coreDir, err)
	}
	if len(offenders) > 0 {
		t.Errorf("StartWatch reappeared outside a comment (core/contexts and core/fswatch excluded — different subsystem):\n%s",
			strings.Join(offenders, "\n"))
	}
}

// TestWP04_ImportDocstringDoesNotClaimAWatcher fails if
// core/rpc/views/mcp/import.go's package docstring re-acquires the word
// "watcher" — the exact word the pre-WP04 docstring used to assert a
// working fsnotify-driven pickup that, per the audit, never actually ran
// in the desktop path. Deleting the watcher without correcting a claim
// like this back in would leave the lie in place even after the code
// that would have made it true is gone.
//
// Mutation: restore the deleted sentence "UserStore.Reload (or the
// fsnotify watcher) picks up the new _imports/<id>.yaml on the next
// event tick, completing the round-trip." to import.go's docstring.
// This test must fail.
func TestWP04_ImportDocstringDoesNotClaimAWatcher(t *testing.T) {
	root := repoRootForWatcherCheck(t)
	path := filepath.Join(root, "core", "rpc", "views", "mcp", "import.go")
	data, err := os.ReadFile(path) // #nosec G304 — fixed path under repo root, test-only
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := strings.ToLower(string(data))
	if strings.Contains(text, "watcher") {
		t.Errorf("%s contains the word %q — WP04 deleted the watcher this docstring used to describe; "+
			"if a background reload mechanism has genuinely been reintroduced, describe it accurately and delete this test's assumption, "+
			"don't just reuse the word", path, "watcher")
	}
}
