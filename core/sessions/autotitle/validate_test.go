package autotitle

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	ellipsis := "…"

	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr error
	}{
		// Happy-path ----------------------------------------------------------------
		{
			name: "simple title returned as-is",
			raw:  "Learning Rust",
			want: "Learning Rust",
		},
		{
			name: "leading and trailing whitespace trimmed",
			raw:  "  Learning Rust  ",
			want: "Learning Rust",
		},

		// Outer quote stripping -----------------------------------------------------
		{
			name: "double-quoted title stripped",
			raw:  `"Learning Rust"`,
			want: "Learning Rust",
		},
		{
			name: "single-quoted title stripped",
			raw:  `'Learning Rust'`,
			want: "Learning Rust",
		},
		{
			name: "mismatched quotes not stripped",
			raw:  `"Learning Rust'`,
			want: `"Learning Rust'`,
		},
		{
			name: "inner quotes preserved",
			raw:  `"It's about Rust"`,
			want: "It's about Rust",
		},

		// Title: prefix stripping ---------------------------------------------------
		{
			name: "Title: prefix stripped",
			raw:  "Title: Learning Rust",
			want: "Learning Rust",
		},
		{
			name: "TITLE: uppercase prefix stripped",
			raw:  "TITLE: Learning Rust",
			want: "Learning Rust",
		},
		{
			name: "title:no-space prefix stripped",
			raw:  "title:Learning Rust",
			want: "Learning Rust",
		},

		// First-line-only -----------------------------------------------------------
		{
			name: "multi-line takes first non-empty line",
			raw:  "Learning Rust\nSecond line ignored",
			want: "Learning Rust",
		},
		{
			name: "leading blank lines skipped to find first non-empty line",
			raw:  "\nLearning Rust\nSecond line",
			want: "Learning Rust",
		},
		{
			name: "CRLF line endings handled",
			raw:  "Learning Rust\r\nSecond line",
			want: "Learning Rust",
		},

		// Control character replacement ---------------------------------------------
		{
			name: "tab replaced with space",
			raw:  "Learning\tRust",
			want: "Learning Rust",
		},
		{
			name: "bell char replaced with space",
			raw:  "Learning\x07Rust",
			want: "Learning Rust",
		},

		// Length edge cases ---------------------------------------------------------
		{
			name: "exactly 50 runes passes unchanged",
			raw:  strings.Repeat("a", 50),
			want: strings.Repeat("a", 50),
		},
		{
			name: "51 runes truncated to 49 + ellipsis",
			raw:  strings.Repeat("a", 51),
			want: strings.Repeat("a", 49) + ellipsis,
		},
		{
			name: "100 runes truncated to 49 + ellipsis",
			raw:  strings.Repeat("x", 100),
			want: strings.Repeat("x", 49) + ellipsis,
		},
		{
			name: "multibyte runes counted by rune not byte for truncation",
			// 51 Japanese runes → truncated to 49 + ellipsis
			raw:  strings.Repeat("日", 51),
			want: strings.Repeat("日", 49) + ellipsis,
		},

		// ErrTitleTooShort ----------------------------------------------------------
		{
			name:    "empty string",
			raw:     "",
			wantErr: ErrTitleTooShort,
		},
		{
			name:    "one rune",
			raw:     "X",
			wantErr: ErrTitleTooShort,
		},
		{
			name:    "two runes",
			raw:     "ok",
			wantErr: ErrTitleTooShort,
		},
		{
			name: "three runes passes",
			raw:  "Foo",
			want: "Foo",
		},
		{
			name:    "only-whitespace becomes empty after trim",
			raw:     "   ",
			wantErr: ErrTitleTooShort,
		},
		{
			name:    "outer quotes strip to empty → too short",
			raw:     `""`,
			wantErr: ErrTitleTooShort,
		},

		// ErrModelRefused -----------------------------------------------------------
		{
			name:    "Sorry prefix refused",
			raw:     "Sorry, I can't help with that.",
			wantErr: ErrModelRefused,
		},
		{
			name:    "I can't prefix refused",
			raw:     "I can't generate a title for this.",
			wantErr: ErrModelRefused,
		},
		{
			name:    "I cannot prefix refused",
			raw:     "I cannot produce a title.",
			wantErr: ErrModelRefused,
		},
		{
			name:    "SORRY uppercase refused",
			raw:     "SORRY, not able to help.",
			wantErr: ErrModelRefused,
		},
		{
			name: "Sorry in the middle is fine",
			raw:  "Not sorry about this title",
			want: "Not sorry about this title",
		},

		// Combined cases ------------------------------------------------------------
		{
			name: "quoted + Title: prefix + whitespace all stripped",
			raw:  `"Title: Learning Rust "`,
			want: "Learning Rust",
		},
		{
			name: "multiline + Title: prefix takes first line after prefix strip",
			raw:  "Title: Learning Rust\nExtra line",
			want: "Learning Rust",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Sanitize(tc.raw)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("Sanitize(%q) error = %v, want %v", tc.raw, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Errorf("Sanitize(%q) unexpected error: %v", tc.raw, err)
				return
			}
			if got != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestSanitize_RuneCount verifies the rune-length invariants precisely.
func TestSanitize_RuneCount(t *testing.T) {
	// 50-rune output must be returned unchanged (at the boundary).
	input50 := strings.Repeat("a", 50)
	got, err := Sanitize(input50)
	if err != nil {
		t.Fatalf("50-rune input: unexpected error: %v", err)
	}
	if got != input50 {
		t.Errorf("50-rune input: got %q, want unchanged", got)
	}

	// 51-rune output must be truncated.
	input51 := strings.Repeat("b", 51)
	got51, err51 := Sanitize(input51)
	if err51 != nil {
		t.Fatalf("51-rune input: unexpected error: %v", err51)
	}
	runes := []rune(got51)
	// Result must be 50 runes total (49 body + 1 for the ellipsis character).
	if len(runes) != 50 {
		t.Errorf("51-rune input: got %d runes, want 50", len(runes))
	}
	if runes[len(runes)-1] != '…' {
		t.Errorf("51-rune input: last rune = %q, want '…'", string(runes[len(runes)-1]))
	}
}
