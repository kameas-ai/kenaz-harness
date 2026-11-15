package toolloop

import "testing"

func TestResultFlagsError(t *testing.T) {
	for in, want := range map[string]bool{
		`{"is_error":true,"error":"write_file: mkdir: permission denied"}`: true,
		`  {"is_error": true}`:                  true,
		`{"is_error":false,"path":"/x"}`:        false,
		`{"written":3,"path":"/x"}`:             false,
		`[{"is_error":true}]`:                   false,
		`plain text mentioning "is_error":true`: false,
		``:                                      false,
		`{"is_error":"yes"}`:                    false,
	} {
		if got := ResultFlagsError([]byte(in)); got != want {
			t.Errorf("ResultFlagsError(%q) = %v, want %v", in, got, want)
		}
	}
}
