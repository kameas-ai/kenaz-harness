package settings

// wp19_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-14 / WP19. FR-029 (the one load-bearing edit in this WP) and
// AC-048's "retracted claim greps to zero" half for the Go side.
//
// AC-049 (SaveMonthlyCostNotifyUSD(-5) returns nil and persists 0) is
// already covered by the pre-existing TestSaveMonthlyCostNotifyUSD in
// impl_test.go ("Negative normalised to zero") — that test already IS
// the falsifiability proof: if Save were changed to reject negatives
// with an error (matching the OLD, false doc), it would fail. Not
// duplicated here.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWP19_AC048_RejectsNegativeClaimNarrowed is the Go-doc half of
// AC-048: neither of the two retracted "Save rejects negative ..."
// phrasings survives anywhere under core/. The frontend half (types.ts
// / user-facing copy) is covered by a matching frontend test; grep run
// manually during development confirmed zero hits there too.
//
// Mutation: restore either retracted phrase in
// core/rpc/views/settings/api.go. Must fail.
func TestWP19_AC048_RejectsNegativeClaimNarrowed(t *testing.T) {
	claims := []string{
		strings.Join([]string{"Save", "rejects", "negative", "or"}, " "),
		strings.Join([]string{"Save", "rejects", "negative", "values", "and", "values", "above"}, " "),
	}
	root := filepath.Join("..", "..", "..", "..") // core/rpc/views/settings -> repo root
	selfPath, _ := filepath.Abs("wp19_test.go")
	var hits []string
	err := filepath.WalkDir(filepath.Join(root, "core"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if abs, aerr := filepath.Abs(path); aerr == nil && abs == selfPath {
			return nil // this file legitimately names the retracted claims in prose
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		normalized := strings.Join(strings.Fields(string(data)), " ")
		for _, c := range claims {
			if strings.Contains(normalized, c) {
				hits = append(hits, path+": "+c)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk core/: %v", err)
	}
	if len(hits) > 0 {
		t.Fatalf("retracted claim(s) still present: %v", hits)
	}
}
