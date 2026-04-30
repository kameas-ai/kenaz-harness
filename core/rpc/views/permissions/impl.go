package permissions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
)

// Engine is the small subset of *cedar.Engine the view needs. The
// interface lets tests drive RevokeGrant without booting a real
// engine.
type Engine interface {
	Reload(ctx context.Context) error
}

// Compile-time witness: *cedar.Engine satisfies Engine.
var _ Engine = (*cedar.Engine)(nil)

// API is the concrete PermissionsAPI implementation.
//
// dataDir is the harness data root; the view resolves grants by
// scanning <dataDir>/policy/. registry is the process-singleton cedar
// prompt registry that owns the in-flight pending map and the
// Allow-once transient cache. engine is the cedar engine used to hot-
// reload after a revoke; nil engine is tolerated (revoke skips the
// reload) so the test harness path stays simple.
//
// All three may be nil; the view degrades to a friendly error per
// method so the chassis still boots when none of these are wired.
type API struct {
	dataDir  string
	registry *cedar.Registry
	engine   Engine
}

// Config configures the API at construction time.
type Config struct {
	DataDir  string
	Registry *cedar.Registry
	Engine   Engine
}

// New constructs the view-scoped API.
func New(cfg Config) *API {
	return &API{
		dataDir:  cfg.DataDir,
		registry: cfg.Registry,
		engine:   cfg.Engine,
	}
}

// ErrRegistryUnavailable is returned by Resolve / ListPending when the
// registry has not been wired. The frontend renders this as
// "permissions subsystem not ready".
var ErrRegistryUnavailable = errors.New("permissions: registry not wired")

// ErrInvalidGrantID is returned when a grant id is malformed
// (non-allowlisted characters, path traversal). The error is checked
// before any filesystem operation.
var ErrInvalidGrantID = errors.New("permissions: invalid grant id")

// Resolve implements PermissionsAPI.Resolve. Routes the user's
// decision into the cedar prompt registry. Errors:
//
//   - ErrRegistryUnavailable when the registry is not wired.
//   - cedar.ErrUnknownRequest when the request id has already resolved.
func (a *API) Resolve(_ context.Context, requestID string, decision Decision) error {
	if a == nil || a.registry == nil {
		return ErrRegistryUnavailable
	}
	return a.registry.Resolve(requestID, decision)
}

// ListPending implements PermissionsAPI.ListPending.
func (a *API) ListPending(_ context.Context) ([]PendingRequest, error) {
	if a == nil || a.registry == nil {
		return []PendingRequest{}, nil
	}
	return a.registry.ListPending(), nil
}

// ListGrants implements PermissionsAPI.ListGrants. Walks
// <dataDir>/policy/, picks up files matching `<family>_allow_*.cedar`,
// then appends transient grants from the registry. Files unrelated to
// the family naming convention are ignored — the cedar editor still
// surfaces them through `CedarPolicy.ListPolicies`, but they are not
// "user-revocable single-permission grants" so they don't belong in
// this list.
func (a *API) ListGrants(_ context.Context) ([]Grant, error) {
	if a == nil {
		return []Grant{}, nil
	}
	out := []Grant{}

	if a.dataDir != "" {
		dir := filepath.Join(a.dataDir, cedar.PolicyDir)
		entries, err := os.ReadDir(dir)
		switch {
		case err == nil:
			for _, ent := range entries {
				if ent.IsDir() {
					continue
				}
				name := ent.Name()
				if !strings.HasSuffix(strings.ToLower(name), ".cedar") {
					continue
				}
				fam := familyFromFilename(name)
				if fam == "" {
					continue
				}
				out = append(out, Grant{
					ID:         name,
					PolicyFile: name,
					Family:     fam,
				})
			}
		case errors.Is(err, os.ErrNotExist):
			// no grants yet; fall through.
		default:
			return nil, err
		}
	}

	// Append transient grants (Allow-once cache).
	if a.registry != nil {
		for _, t := range a.registry.ListTransientGrants() {
			out = append(out, Grant{
				ID:          t.ResourceKey,
				Transient:   true,
				ResourceKey: t.ResourceKey,
				Family:      familyFromResourceKey(t.ResourceKey),
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		// Persisted grants first (sorted by filename), then transient
		// (sorted by resource key).
		if out[i].Transient != out[j].Transient {
			return !out[i].Transient
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// RevokeGrant implements PermissionsAPI.RevokeGrant. Two paths:
//
//   - grantID matches a `<family>_allow_*.cedar` filename → delete the
//     file under <dataDir>/policy/ + reload the engine.
//   - grantID matches a transient resource-key → drop the
//     corresponding entry from the registry's Allow-once cache.
//
// Returns ErrInvalidGrantID for malformed input. Filesystem errors
// (file missing, permission denied) are propagated unchanged so the
// frontend can render a structured failure.
func (a *API) RevokeGrant(ctx context.Context, grantID string) error {
	if a == nil {
		return ErrRegistryUnavailable
	}
	if grantID == "" {
		return ErrInvalidGrantID
	}

	if isPolicyFilename(grantID) {
		if a.dataDir == "" {
			return errors.New("permissions: data dir not configured")
		}
		path := filepath.Join(a.dataDir, cedar.PolicyDir, grantID)
		// Defence-in-depth: re-clean and verify the result still sits
		// under the policy dir. Filename-validation catches the obvious
		// "../" cases; this guards against subtler escapes.
		clean := filepath.Clean(path)
		expectedDir := filepath.Clean(filepath.Join(a.dataDir, cedar.PolicyDir))
		if !strings.HasPrefix(clean, expectedDir+string(filepath.Separator)) && clean != expectedDir {
			return ErrInvalidGrantID
		}
		if err := os.Remove(clean); err != nil {
			return err
		}
		if a.engine != nil {
			if err := a.engine.Reload(ctx); err != nil {
				return err
			}
		}
		// Engine reload is the boundary at which transient grants
		// MUST be cleared (a stale Allow-once for a resource the user
		// just revoked must not bypass the new posture).
		if a.registry != nil {
			a.registry.ClearTransientGrants()
		}
		return nil
	}

	// Transient grant — re-clear the entire cache. The Allow-once
	// cache is per-process and intentionally coarse; revoking any
	// transient grant clears the slate so the user always sees a
	// fresh prompt for sensitive ops after revoking.
	if isTransientResourceKey(grantID) {
		if a.registry == nil {
			return ErrRegistryUnavailable
		}
		a.registry.ClearTransientGrants()
		return nil
	}
	return ErrInvalidGrantID
}

// isTransientResourceKey reports whether s is a well-formed family-
// prefixed resource key from the registry's Allow-once cache. Used by
// RevokeGrant to distinguish "this is a transient key" from "this is
// a malformed policy filename" so path-traversal-shaped inputs land
// on ErrInvalidGrantID rather than silently clearing the cache.
func isTransientResourceKey(s string) bool {
	switch {
	case strings.HasPrefix(s, "bash::"),
		strings.HasPrefix(s, "fs::"),
		strings.HasPrefix(s, "cred::"),
		strings.HasPrefix(s, "tool::"):
		return !strings.Contains(s, "..")
	}
	return false
}

// isPolicyFilename reports whether s is a single-segment path with a
// `.cedar` suffix and one of the four family prefixes. The strict
// regex-style check here is the spec's path-traversal mitigation
// (FR-009 + risk register entry "WritePolicySnippet path traversal").
func isPolicyFilename(s string) bool {
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, `/\`) {
		return false
	}
	if strings.Contains(s, "..") {
		return false
	}
	if !strings.HasSuffix(strings.ToLower(s), ".cedar") {
		return false
	}
	return familyFromFilename(s) != ""
}

// familyFromFilename returns the family discriminator for a
// `<family>_allow_*.cedar` filename, or empty when the filename does
// not match the convention. Used by ListGrants and RevokeGrant.
func familyFromFilename(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "bash_allow_"):
		return string(cedar.FamilyBash)
	case strings.HasPrefix(lower, "fs_allow_"):
		return string(cedar.FamilyFilesystem)
	case strings.HasPrefix(lower, "cred_allow_"):
		return string(cedar.FamilyCredential)
	case strings.HasPrefix(lower, "tool_allow_"):
		return string(cedar.FamilyTool)
	}
	return ""
}

// familyFromResourceKey returns the family discriminator for a
// transient resource-key. The keys are formatted as
// "<family>::<rest>"; an unrecognised prefix returns the empty string.
func familyFromResourceKey(k string) string {
	switch {
	case strings.HasPrefix(k, "bash::"):
		return string(cedar.FamilyBash)
	case strings.HasPrefix(k, "fs::"):
		return string(cedar.FamilyFilesystem)
	case strings.HasPrefix(k, "cred::"):
		return string(cedar.FamilyCredential)
	case strings.HasPrefix(k, "tool::"):
		return string(cedar.FamilyTool)
	}
	return ""
}
