package bash

import (
	"errors"
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
		err  error
	}{
		{
			name: "simple two tokens",
			in:   "ls -la",
			want: []string{"ls", "-la"},
		},
		{
			name: "double quoted phrase preserves spaces",
			in:   `echo "hello world"`,
			want: []string{"echo", "hello world"},
		},
		{
			// Inside single quotes a backslash is a literal byte.
			// Input bytes: echo 'don\\t' (one backslash + t inside
			// the quoted segment). Expected token: don\t (literal
			// backslash + t).
			name: "single quote keeps backslash literal",
			in:   `echo 'don\t'`,
			want: []string{"echo", `don\t`},
		},
		{
			// Outside quotes a backslash escapes the next byte.
			// Input bytes: echo \$HOME → expected token "$HOME".
			name: "outside-quote backslash escapes dollar",
			in:   `echo \$HOME`,
			want: []string{"echo", "$HOME"},
		},
		{
			// Pipes / redirects pass through as ordinary tokens.
			// They will fail the allowlist downstream because "|"
			// is not in the allowed-program list.
			name: "pipe stays as a token",
			in:   "echo hi | grep h",
			want: []string{"echo", "hi", "|", "grep", "h"},
		},
		{
			name: "redirect stays as a token",
			in:   "a > b",
			want: []string{"a", ">", "b"},
		},
		{
			name: "unterminated single quote",
			in:   "'unterminated",
			err:  ErrUnterminatedQuote,
		},
		{
			name: "unterminated double quote",
			in:   `"oops`,
			err:  ErrUnterminatedQuote,
		},
		{
			name: "empty input",
			in:   "",
			want: nil,
		},
		{
			name: "whitespace-only input",
			in:   "   \t  ",
			want: nil,
		},
		{
			name: "unicode token preserved",
			in:   "echo héllo",
			want: []string{"echo", "héllo"},
		},
		{
			name: "double quote escape sequences",
			in:   `echo "a\"b\\c\$d"`,
			want: []string{"echo", `a"b\c$d`},
		},
		{
			// Backslash before an ordinary char inside double
			// quotes is preserved literally — POSIX rule.
			name: "double quote backslash before ordinary char",
			in:   `echo "x\yz"`,
			want: []string{"echo", `x\yz`},
		},
		{
			name: "tab separates tokens",
			in:   "a\tb\tc",
			want: []string{"a", "b", "c"},
		},
		{
			// Adjacent quoted + unquoted segments concatenate
			// into a single token (POSIX shell semantics).
			name: "adjacent quoted segments concat",
			in:   `pre"mid"post`,
			want: []string{"premidpost"},
		},
		{
			// A bare backslash at end of input collapses to a
			// literal backslash (not an error — same as bash's
			// behaviour for an unterminated escape).
			name: "trailing bare backslash",
			in:   `foo\`,
			want: []string{`foo\`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err = %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("argv = %#v, want %#v", got, tc.want)
			}
		})
	}
}
