package agentgraph

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed library/*.yaml
var libraryFS embed.FS

// bundledLibrary is the in-memory map of `id → yaml-source` populated
// from the embedded library directory. A package-init pass parses the
// IDs out of each YAML so callers can list/load by id.
var bundledLibrary = map[string]string{}

func init() {
	entries, err := fs.ReadDir(libraryFS, "library")
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".yaml") {
			continue
		}
		data, err := fs.ReadFile(libraryFS, "library/"+e.Name())
		if err != nil {
			continue
		}
		id := scanGraphID(string(data))
		if id == "" {
			continue
		}
		bundledLibrary[id] = string(data)
	}
}

// scanGraphID is a thin YAML scan that pulls the top-level `id:` value
// out of a graph file without round-tripping through agentgraph's typed
// loader. Keeps init cheap; full parsing happens lazily in loadGraph.
func scanGraphID(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "id:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		val = strings.Trim(val, `"'`)
		if val != "" {
			return val
		}
	}
	return ""
}
