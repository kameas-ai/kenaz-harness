package bundle

type Lockfile struct {
	SchemaVersion int            `json:"schema_version"`
	Root          string         `json:"root"`
	Bundles       []LockedBundle `json:"bundles"`
}

type LockedBundle struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Digest   string   `json:"digest"`
	Source   Source   `json:"source"`
	Requires []string `json:"requires,omitempty"`
}

type Source struct {
	Kind string `json:"kind"`
	URL  string `json:"url,omitempty"`
	Ref  string `json:"ref,omitempty"`
	Path string `json:"path,omitempty"`
}
