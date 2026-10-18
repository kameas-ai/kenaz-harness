package mlproducer

// reads.go — agent reads as `file` events (ml-producer-01MLPRD01 WP07
// item 1; spec §12 A-15).
//
// A successful read tool call ships one `file` event {task, path, file}
// per file it touched, IN ADDITION to the call's own agent.tool row (the
// agent.tool row keeps the tool name, outcome and duration, which a
// `file` event cannot carry; the `file` event is what the engine features
// on). Where the paths come from:
//
//   - kenaz__read_file: the `path` argument, resolved against the
//     workspace exactly as writes are. Its result ({content, byte_size,
//     truncated}) names no path, and a successful read proves the
//     argument named a readable file.
//   - kenaz__grep: each distinct `matches[].file` of the RESULT, in result
//     order (the walk order; deterministic).
//   - kenaz__glob: each `matches[]` entry of the RESULT (files only, sorted
//     by the tool).
//   - kenaz__list_dir: each `entries[]` of the RESULT whose `type` is
//     "file" (directories and symlinks are not files), its `path` being
//     relative to the listed directory, i.e. the `path` argument resolved
//     against the workspace.
//
// A relative result path resolves against the tool's root (grep: its
// `path` argument; glob: its `base_dir`, else the workspace). The cap is
// the FIRST maxReadFiles distinct candidate paths in result order; org
// exclusions then drop any of them before hashing. Reads never count into
// tasks.files (writes only) and never change the phase rules (a read is
// already `exploring`). A failed read (outcome != ok, or a fsbuiltins
// {"is_error":true} result) ships only its agent.tool row, as before.

import (
	"encoding/json"
	"path/filepath"
)

// maxReadFiles caps the `file` events one read call can produce.
const maxReadFiles = 20

// isReadTool reports whether name is one of the reads that ship `file`
// events.
func isReadTool(name string) bool {
	switch name {
	case toolReadFile, toolGrep, toolGlob, toolListDir:
		return true
	}
	return false
}

// readCandidate is one path a read touched: abs is what ships (hashed);
// raw is the form the tool reported or was given, matched against
// exclusions as well (as writes match the raw argument too).
type readCandidate struct {
	abs string
	raw string
}

// readPaths returns up to maxReadFiles distinct files a successful read
// call touched, in deterministic (result) order. nil when the result does
// not parse or names nothing.
func readPaths(tool string, args map[string]any, result, workspace string) []readCandidate {
	var out []readCandidate
	seen := map[string]bool{}
	add := func(raw, root string) bool {
		if len(out) >= maxReadFiles {
			return false
		}
		abs := absPath(raw, root)
		if abs == "" || seen[abs] {
			return true
		}
		seen[abs] = true
		out = append(out, readCandidate{abs: abs, raw: raw})
		return len(out) < maxReadFiles
	}
	switch tool {
	case toolReadFile:
		add(stringArg(args, "path"), workspace)
	case toolGrep:
		var r struct {
			Matches []struct {
				File string `json:"file"`
			} `json:"matches"`
		}
		if json.Unmarshal([]byte(result), &r) != nil {
			return nil
		}
		root := absPath(stringArg(args, "path"), workspace)
		if root == "" {
			root = workspace
		}
		for _, m := range r.Matches {
			if !add(m.File, root) {
				break
			}
		}
	case toolGlob:
		var r struct {
			Matches []string `json:"matches"`
		}
		if json.Unmarshal([]byte(result), &r) != nil {
			return nil
		}
		root := absPath(stringArg(args, "base_dir"), workspace)
		if root == "" {
			root = workspace
		}
		for _, m := range r.Matches {
			if !add(m, root) {
				break
			}
		}
	case toolListDir:
		var r struct {
			Entries []struct {
				Type string `json:"type"`
				Path string `json:"path"`
			} `json:"entries"`
		}
		if json.Unmarshal([]byte(result), &r) != nil {
			return nil
		}
		root := absPath(stringArg(args, "path"), workspace)
		if root == "" {
			return nil
		}
		for _, e := range r.Entries {
			if e.Type != "file" || e.Path == "" {
				continue
			}
			p := e.Path
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, p)
			}
			if !add(p, root) {
				break
			}
		}
	}
	return out
}
