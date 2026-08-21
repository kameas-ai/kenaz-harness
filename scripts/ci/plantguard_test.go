package ci_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// plantguard_test.go — makes the planted-violation harness survive a
// `go test` timeout.
//
// plant() restores through `defer cleanup()`. A timeout does not unwind
// defers: the test binary is killed, and whatever was planted stays in
// the working tree. That is not theoretical. On 2026-08-21 a killed run
// left `zzGateProbeMoveAssign` in core/session/manager.go, a later
// `git add -A` committed it to a release branch, and it shipped as far
// as the integration gate sweep before check-single-move-writer.sh
// caught it. On 2026-08-22 the same thing happened again with
// `zzGateProbeInlineLiteral` — caught before commit only because someone
// thought to look. Recorded as finding N6 in the review of PR #306.
//
// The fix is a journal, not more discipline. plant() records what it is
// about to touch BEFORE touching it; cleanup clears the record; and
// TestMain replays any records left by a previous run at startup. A run
// killed at any point leaves a journal that the next run heals from,
// which is the only ordering that works when the killing signal cannot
// be intercepted.
//
// The journal lives beside the tests and is git-ignored, so a leftover
// journal never becomes a commit in its own right.

const plantJournalName = ".zz-plant-journal.json"

type plantRecord struct {
	Path string `json:"path"`
	// Orig is the file's content before planting. Empty with Existed
	// false means the file did not exist and must be removed.
	Orig    string `json:"orig"`
	Existed bool   `json:"existed"`
	// CreatedDir is a directory plant() had to create, or "".
	CreatedDir string `json:"created_dir,omitempty"`
}

var (
	journalMu   sync.Mutex
	journalPath string
)

func plantJournalPath() string {
	if journalPath != "" {
		return journalPath
	}
	journalPath = filepath.Join("..", "..", "scripts", "ci", plantJournalName)
	if wd, err := os.Getwd(); err == nil {
		journalPath = filepath.Join(wd, plantJournalName)
	}
	return journalPath
}

func readJournal() []plantRecord {
	b, err := os.ReadFile(plantJournalPath())
	if err != nil {
		return nil
	}
	var recs []plantRecord
	if json.Unmarshal(b, &recs) != nil {
		return nil
	}
	return recs
}

func writeJournal(recs []plantRecord) {
	if len(recs) == 0 {
		_ = os.Remove(plantJournalPath())
		return
	}
	b, err := json.Marshal(recs)
	if err != nil {
		return
	}
	_ = os.WriteFile(plantJournalPath(), b, 0o644)
}

// journalPlant records a pending plant. Call BEFORE modifying the file.
func journalPlant(rec plantRecord) {
	journalMu.Lock()
	defer journalMu.Unlock()
	writeJournal(append(readJournal(), rec))
}

// journalClear drops the record for path after a successful restore.
func journalClear(path string) {
	journalMu.Lock()
	defer journalMu.Unlock()
	recs := readJournal()
	out := recs[:0]
	for _, r := range recs {
		if r.Path != path {
			out = append(out, r)
		}
	}
	writeJournal(out)
}

// healLeftoverPlants restores anything a previous killed run left behind.
// Returns a human-readable list of what it healed.
func healLeftoverPlants() []string {
	recs := readJournal()
	if len(recs) == 0 {
		return nil
	}
	var healed []string
	for _, r := range recs {
		if r.Existed {
			if err := os.WriteFile(r.Path, []byte(r.Orig), 0o644); err == nil {
				healed = append(healed, "restored "+r.Path)
			}
		} else {
			if err := os.Remove(r.Path); err == nil || os.IsNotExist(err) {
				healed = append(healed, "removed "+r.Path)
			}
		}
		if r.CreatedDir != "" {
			_ = os.Remove(r.CreatedDir)
		}
	}
	writeJournal(nil)
	return healed
}

func TestMain(m *testing.M) {
	if healed := healLeftoverPlants(); len(healed) > 0 {
		fmt.Fprintf(os.Stderr,
			"[plantguard] healed %d leftover plant(s) from a previous killed run:\n", len(healed))
		for _, h := range healed {
			fmt.Fprintf(os.Stderr, "[plantguard]   %s\n", h)
		}
	}
	code := m.Run()
	if healed := healLeftoverPlants(); len(healed) > 0 {
		fmt.Fprintf(os.Stderr,
			"[plantguard] %d plant(s) were still journalled after the run; healed:\n", len(healed))
		for _, h := range healed {
			fmt.Fprintf(os.Stderr, "[plantguard]   %s\n", h)
		}
	}
	os.Exit(code)
}
