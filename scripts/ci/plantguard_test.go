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
	// Planted is the content plant() WROTE. Healing restores only when
	// the file still holds exactly this.
	//
	// The first version of this file omitted it and healed
	// unconditionally, which made the cure worse than the disease: a
	// journal left by a killed run would revert whatever the file
	// contained later — a developer's edits, a git checkout, a rebase
	// result — to stale content, silently. Proved by the review of
	// PR #306 (finding E1), which also showed it would RECREATE a file
	// that had since been deleted. Restoring is only safe when the thing
	// being restored is provably still the plant.
	Planted string `json:"planted"`
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
		cur, err := os.ReadFile(r.Path)
		if err != nil {
			// Gone. If it was ours to remove, the job is already done;
			// if it was a real file, DO NOT recreate it from stale
			// content — someone deleted it deliberately.
			if r.Existed {
				healed = append(healed, "SKIPPED (file no longer exists, not recreating): "+r.Path)
			}
			continue
		}
		if string(cur) != r.Planted {
			// The file changed since we planted. Anything we write now
			// would destroy that change.
			healed = append(healed, "SKIPPED (content changed since planting, left alone): "+r.Path)
			continue
		}
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
	// A heal at startup means a previous run was killed mid-plant. Report
	// it and EXIT NON-ZERO without running the suite.
	//
	// Printing alone is not enough: `go test` swallows a passing
	// package's stderr, so the message was invisible in exactly the case
	// that matters — a normal, non-verbose run (finding E1, review of
	// PR #306). A silent repair of the working tree is the same class of
	// problem as the silent corruption it replaced.
	if healed := healLeftoverPlants(); len(healed) > 0 {
		fmt.Fprintf(os.Stderr,
			"[plantguard] a previous run was killed mid-plant; %d record(s) processed:\n", len(healed))
		for _, h := range healed {
			fmt.Fprintf(os.Stderr, "[plantguard]   %s\n", h)
		}
		fmt.Fprintf(os.Stderr,
			"[plantguard] the working tree has been touched — re-run to execute the suite.\n")
		os.Exit(1)
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
