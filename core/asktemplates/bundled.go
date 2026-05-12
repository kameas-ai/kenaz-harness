// Package asktemplates — bundled.go embeds the ship-default templates.
package asktemplates

import (
	"embed"
	"io/fs"
)

// bundledTemplates holds the YAML files under bundled/.
//
//go:embed bundled/*.yaml
var bundledTemplates embed.FS

// BundledFS is the ReadFileFS for the embedded template directory.
// It is rooted at "bundled/" so callers use bare filenames (e.g.
// "confirm-deploy.yaml").
var BundledFS fs.ReadFileFS = &subFS{bundledTemplates, "bundled"}

// subFS wraps embed.FS and re-roots it at a sub-directory so callers
// use bare filenames.
type subFS struct {
	embed.FS
	root string
}

// Open implements fs.FS by prepending the root prefix.
func (s *subFS) Open(name string) (fs.File, error) {
	if name == "." {
		return s.FS.Open(s.root)
	}
	return s.FS.Open(s.root + "/" + name)
}

// ReadFile implements fs.ReadFileFS.
func (s *subFS) ReadFile(name string) ([]byte, error) {
	return s.FS.ReadFile(s.root + "/" + name)
}

// ReadDir implements fs.ReadDirFS — lists the sub-directory contents.
func (s *subFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == "." {
		return s.FS.ReadDir(s.root)
	}
	return s.FS.ReadDir(s.root + "/" + name)
}
