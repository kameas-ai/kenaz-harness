package bundle

import "context"

type Resolver interface {
	Resolve(ctx context.Context, set []Dependency) (*Lockfile, error)
	Materialize(ctx context.Context, lock *Lockfile) (*Materialized, error)
}

type Materialized struct {
	Bundles      []ResolvedBundle
	ContextOrder []string
	Skills       map[string]ResolvedSkill
	Agents       map[string]ResolvedAgent
	MCP          map[string]ResolvedMCP
}

type ResolvedBundle struct {
	Manifest Manifest
	Root     string
	Digest   string
}

type ResolvedSkill struct {
	BundleName string
	Name       string
	Path       string
}

type ResolvedAgent struct {
	BundleName string
	Name       string
	Path       string
}

type ResolvedMCP struct {
	BundleName string
	Name       string
	Spec       map[string]any
}
