package toolloop

import (
	"bytes"
	"encoding/json"
)

// ResultFlagsError reports whether a tool's successful dispatch (Go
// err == nil) nonetheless carries a top-level `"is_error": true` — the
// in-band failure convention the builtin fs, todo and web_fetch tools
// use for "the tool ran and the operation failed".
//
// The chat kernel adapter used to map every nil-error dispatch to
// IsError=false, so a kenaz__write_file whose mkdir failed with
// EACCES rendered as a successful tool call: the associate saw "OK",
// the model read the error text, and the two told different stories
// about where the files went (fleet e2e 2026-11-07, E2E-2).
func ResultFlagsError(out []byte) bool {
	t := bytes.TrimSpace(out)
	if len(t) == 0 || t[0] != '{' {
		return false
	}
	var r struct {
		IsError bool `json:"is_error"`
	}
	if err := json.Unmarshal(t, &r); err != nil {
		return false
	}
	return r.IsError
}
