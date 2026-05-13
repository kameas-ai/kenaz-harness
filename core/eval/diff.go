package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// DiffMode selects the comparison algorithm.
type DiffMode string

const (
	// DiffModeBytes performs an exact byte-level comparison of the text
	// content in assistant messages. A score of 1.0 means identical; 0.0
	// means completely different.
	DiffModeBytes DiffMode = "bytes"

	// DiffModeEmbedding computes cosine similarity between the text content.
	// This mode requires an external embedding; in this implementation it
	// falls back to a normalized edit-distance (Jaro-Winkler approximation)
	// so the package has no network dependency.
	DiffModeEmbedding DiffMode = "embedding"
)

// DiffEntry is a per-message comparison result.
type DiffEntry struct {
	SeqNo    int64    `json:"seq_no"`
	Kind     EntryKind `json:"kind"`
	// Score is the similarity score in [0.0, 1.0]: 1.0 = identical, 0.0 = fully different.
	Score    float64  `json:"score"`
	// BaselineText is the text extracted from the baseline entry (truncated for readability).
	BaselineText string `json:"baseline_text,omitempty"`
	// ReplayText is the text extracted from the replay entry.
	ReplayText string `json:"replay_text,omitempty"`
	// Changed is true when Score < 1.0.
	Changed bool `json:"changed"`
}

// DiffSummary is the full diff_vs_baseline.json written to the run directory.
type DiffSummary struct {
	RunID        string      `json:"run_id"`
	BaselineFile string      `json:"baseline_file"`
	ReplayFile   string      `json:"replay_file"`
	ComputedAt   time.Time   `json:"computed_at"`
	Mode         DiffMode    `json:"mode"`
	Entries      []DiffEntry `json:"entries"`
	// OverallScore is the average score across all assistant-message entries.
	OverallScore float64 `json:"overall_score"`
	// ChangedCount is the number of entries where Score < 1.0.
	ChangedCount int `json:"changed_count"`
	// TotalCompared is the number of entries included in the comparison.
	TotalCompared int `json:"total_compared"`
}

// Diff compares a baseline capture file against a replay trace file and
// writes diff_vs_baseline.json to runDir. Only KindMessage entries with
// role "assistant" are compared.
//
// The diff is currently byte/similarity-only (no LLM-as-judge). The
// DiffModeEmbedding path uses a pure-Go Jaro-Winkler approximation so
// the package requires no network access (spec §3 non-goal: no scoring).
func Diff(runDir, baselinePath, replayTracePath string, mode DiffMode) (*DiffSummary, error) {
	baselineMsgs, err := extractAssistantMessages(baselinePath)
	if err != nil {
		return nil, fmt.Errorf("eval/diff: read baseline: %w", err)
	}
	replayMsgs, err := extractAssistantMessages(replayTracePath)
	if err != nil {
		return nil, fmt.Errorf("eval/diff: read replay: %w", err)
	}

	summary := &DiffSummary{
		RunID:        filepath.Base(runDir),
		BaselineFile: baselinePath,
		ReplayFile:   replayTracePath,
		ComputedAt:   time.Now().UTC(),
		Mode:         mode,
	}

	n := len(baselineMsgs)
	if len(replayMsgs) < n {
		n = len(replayMsgs)
	}

	var scoreSum float64
	for i := 0; i < n; i++ {
		b := baselineMsgs[i]
		r := replayMsgs[i]
		score := compareTexts(b.text, r.text, mode)
		entry := DiffEntry{
			SeqNo:        b.seqNo,
			Kind:         KindMessage,
			Score:        score,
			BaselineText: truncate(b.text, 200),
			ReplayText:   truncate(r.text, 200),
			Changed:      score < 1.0,
		}
		summary.Entries = append(summary.Entries, entry)
		scoreSum += score
		if entry.Changed {
			summary.ChangedCount++
		}
	}
	summary.TotalCompared = n
	if n > 0 {
		summary.OverallScore = scoreSum / float64(n)
	}

	// Write diff_vs_baseline.json
	outPath := filepath.Join(runDir, "diff_vs_baseline.json")
	f, err := os.Create(outPath)
	if err != nil {
		return summary, fmt.Errorf("eval/diff: create output: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(summary); err != nil {
		_ = f.Close()
		return summary, err
	}
	return summary, f.Close()
}

// assistantMsg is the internal type used during diff.
type assistantMsg struct {
	seqNo int64
	text  string
}

// extractAssistantMessages reads a capture (or trace) JSONL file and returns
// the text of all KindMessage entries with role "assistant", in order.
func extractAssistantMessages(path string) ([]assistantMsg, error) {
	var msgs []assistantMsg
	err := ReadCapture(path, func(e CaptureEntry) error {
		if e.Kind != KindMessage {
			return nil
		}
		var me MessageEntry
		if err := json.Unmarshal(e.Payload, &me); err != nil {
			return nil
		}
		if me.Role != "assistant" {
			return nil
		}
		// Extract text from content blocks.
		var text strings.Builder
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(me.Content, &blocks); err == nil {
			for _, b := range blocks {
				if b.Type == "text" || b.Type == "" {
					text.WriteString(b.Text)
				}
			}
		}
		msgs = append(msgs, assistantMsg{seqNo: e.SeqNo, text: text.String()})
		return nil
	})
	return msgs, err
}

// compareTexts returns a similarity score in [0.0, 1.0] between two strings.
func compareTexts(a, b string, mode DiffMode) float64 {
	if a == b {
		return 1.0
	}
	switch mode {
	case DiffModeEmbedding:
		return jaroWinkler(a, b)
	default: // DiffModeBytes
		return byteSimScore(a, b)
	}
}

// byteSimScore returns a simple longest-common-subsequence length ratio.
// This is O(n*m) but bounded by the 200-character truncation in practice.
func byteSimScore(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 && lb == 0 {
		return 1.0
	}
	if la == 0 || lb == 0 {
		return 0.0
	}
	// LCS length
	prev := make([]int, lb+1)
	for i := 1; i <= la; i++ {
		curr := make([]int, lb+1)
		for j := 1; j <= lb; j++ {
			if ra[i-1] == rb[j-1] {
				curr[j] = prev[j-1] + 1
			} else {
				if prev[j] > curr[j-1] {
					curr[j] = prev[j]
				} else {
					curr[j] = curr[j-1]
				}
			}
		}
		prev = curr
	}
	lcs := float64(prev[lb])
	max := float64(la)
	if float64(lb) > max {
		max = float64(lb)
	}
	return lcs / max
}

// jaroWinkler computes the Jaro-Winkler similarity between two strings.
// Returns a value in [0.0, 1.0].
func jaroWinkler(s1, s2 string) float64 {
	r1 := []rune(s1)
	r2 := []rune(s2)
	l1, l2 := len(r1), len(r2)
	if l1 == 0 && l2 == 0 {
		return 1.0
	}
	if l1 == 0 || l2 == 0 {
		return 0.0
	}

	matchWindow := int(math.Max(float64(l1), float64(l2)))/2 - 1
	if matchWindow < 0 {
		matchWindow = 0
	}

	s1Matches := make([]bool, l1)
	s2Matches := make([]bool, l2)
	matchCount := 0
	transpositions := 0

	for i := 0; i < l1; i++ {
		lo := i - matchWindow
		if lo < 0 {
			lo = 0
		}
		hi := i + matchWindow + 1
		if hi > l2 {
			hi = l2
		}
		for j := lo; j < hi; j++ {
			if s2Matches[j] || r1[i] != r2[j] {
				continue
			}
			s1Matches[i] = true
			s2Matches[j] = true
			matchCount++
			break
		}
	}

	if matchCount == 0 {
		return 0.0
	}

	k := 0
	for i := 0; i < l1; i++ {
		if !s1Matches[i] {
			continue
		}
		for !s2Matches[k] {
			k++
		}
		if r1[i] != r2[k] {
			transpositions++
		}
		k++
	}

	jaro := (float64(matchCount)/float64(l1) +
		float64(matchCount)/float64(l2) +
		float64(matchCount-transpositions/2)/float64(matchCount)) / 3.0

	// Winkler prefix boost (max 4 chars).
	prefixLen := 0
	for i := 0; i < 4 && i < l1 && i < l2; i++ {
		if r1[i] == r2[i] {
			prefixLen++
		} else {
			break
		}
	}
	return jaro + float64(prefixLen)*0.1*(1.0-jaro)
}

// truncate returns at most maxRunes runes from s, appending "…" if truncated.
func truncate(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes]) + "…"
}
