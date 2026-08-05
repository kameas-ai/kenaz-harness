// Package workspace is the single source of truth for the harness's
// agent-workspace directory (spec 089).
//
// Before this package, every consumer derived the workspace independently
// as <DataDir>/agent-workspace (bash sandbox root, chat env-context line,
// filesystem-recipe defaults) and NOTHING created it in served mode — a
// fresh workbench advertised a directory whose first `ls` failed, and the
// agent fell into a permission-request loop the operator witnessed
// (2026-08-04). Meanwhile the granted /workspace virtio-fs mount went
// unused.
//
// Resolve implements the spec-089 grounding rules (plan.md D2):
//
//	override set + usable            → the granted workspace (Source "granted")
//	override set + ro mountpoint     → kept, flagged ReadOnly (SC-4)
//	override set + unusable stub     → <DataDir>/agent-workspace (Source "fallback")
//	no override                      → <DataDir>/agent-workspace (Source "default")
//
// The distinction between "ro mountpoint" and "unusable stub" matters: on a
// grant-less workbench boot, /workspace exists as an unmounted tmpfiles
// stub owned by another user — falling back there is correct (SC-3). On an
// ro dir grant the SAME unwritable stat picture is a real, mounted grant —
// keeping it is correct (SC-4). A mountpoint check (st_dev vs parent)
// separates the two.
package workspace

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// MarkerName is dropped at the root of a HARNESS-OWNED workspace the first
// time Ensure creates it. The marker lets a future "Reset workspace" path
// identify which directory is harness-owned without misidentifying a
// user's pre-existing folder. Never written into an override (granted)
// directory — that is the user's tree, not ours (plan.md D3).
const MarkerName = ".kenaz-workspace"

// dirName is the conventional subdirectory of DataDir.
const dirName = "agent-workspace"

// Source classifies where the resolved workspace came from.
type Source string

const (
	// SourceGranted: the override (e.g. the workbench's /workspace mount)
	// is in use.
	SourceGranted Source = "granted"
	// SourceDefault: no override was configured; the harness-private
	// <DataDir>/agent-workspace is in use.
	SourceDefault Source = "default"
	// SourceFallback: an override was configured but unusable; the
	// harness-private workspace is in use instead.
	SourceFallback Source = "fallback"
)

// Resolution is the resolved agent workspace.
type Resolution struct {
	// Dir is the absolute workspace path every consumer must use.
	Dir string
	// Source says how Dir was chosen.
	Source Source
	// ReadOnly is true when Dir is a mounted grant the service user
	// cannot write (an ro dir grant). Reads work; writes will fail at the
	// filesystem with the mount's own error.
	ReadOnly bool
	// FallbackReason is a short operator-facing reason when Source is
	// SourceFallback ("" otherwise). Contains the rejected path and the
	// probe outcome — paths only, never file contents.
	FallbackReason string
}

// Note renders the honest one-line description of the workspace for
// user-facing surfaces (the chat env-context line — spec 089 FR-4).
// Empty for the default source: callers keep their generic pre-089
// "sandboxed agent workspace" wording there.
func (r Resolution) Note() string {
	switch r.Source {
	case SourceGranted:
		if r.ReadOnly {
			return "the user's granted workspace, shared with the host (read-only)."
		}
		return "the user's granted workspace, shared with the host."
	case SourceFallback:
		return "a sandboxed agent workspace, not the user's project (the configured workspace was unavailable)."
	default:
		return ""
	}
}

// DefaultDir returns the harness-private workspace path for dataDir,
// without touching the filesystem.
func DefaultDir(dataDir string) string {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		abs = dataDir
	}
	return filepath.Join(abs, dirName)
}

// Resolve picks the agent workspace per the spec-089 rules above. It never
// fails: every outcome lands on a usable directory choice (creation of the
// default path is Ensure's job, typically at core.Start).
//
// Resolve probes override with read-only stats plus one transient
// write-probe file (created and removed); it does not create directories.
func Resolve(override, dataDir string) Resolution {
	if override == "" {
		return Resolution{Dir: DefaultDir(dataDir), Source: SourceDefault}
	}

	fallback := func(reason string) Resolution {
		return Resolution{
			Dir:            DefaultDir(dataDir),
			Source:         SourceFallback,
			FallbackReason: fmt.Sprintf("%s: %s", override, reason),
		}
	}

	abs, err := filepath.Abs(override)
	if err != nil {
		return fallback("not an absolute path")
	}
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fallback("does not exist")
		}
		return fallback("not statable: " + err.Error())
	}
	if !fi.IsDir() {
		return fallback("not a directory")
	}
	if !listable(abs) {
		return fallback("not readable by the harness user")
	}
	if writable(abs) {
		return Resolution{Dir: abs, Source: SourceGranted}
	}
	// Unwritable. A mounted grant (ro bind) is kept read-only; an
	// unmounted stub (no grant behind it) is not a workspace at all.
	if isMountpoint(abs) {
		return Resolution{Dir: abs, Source: SourceGranted, ReadOnly: true}
	}
	return fallback("not writable and not a mounted grant")
}

// Ensure creates <dataDir>/agent-workspace if it doesn't already exist and
// drops the MarkerName file at its root the first time. Idempotent:
// subsequent calls leave the marker untouched and return the existing
// directory. dataDir must be non-empty.
//
// (Moved verbatim from tools.EnsureWorkspace — spec 089 T101/T104 — so the
// mkdir+marker logic exists exactly once.)
func Ensure(dataDir string) (string, error) {
	if dataDir == "" {
		return "", errors.New("workspace: Ensure: dataDir is empty")
	}
	ws := DefaultDir(dataDir)
	created := false
	if _, err := os.Stat(ws); err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("workspace: Ensure: stat %q: %w", ws, err)
		}
		if err := os.MkdirAll(ws, 0o700); err != nil {
			return "", fmt.Errorf("workspace: Ensure: mkdir %q: %w", ws, err)
		}
		created = true
	}
	if created {
		marker := filepath.Join(ws, MarkerName)
		if err := os.WriteFile(marker, []byte("kenaz harness agent workspace\n"), 0o600); err != nil {
			return "", fmt.Errorf("workspace: Ensure: write marker %q: %w", marker, err)
		}
	}
	return ws, nil
}

// listable reports whether the directory can be opened and read by the
// current user (one Readdirnames probe; an empty directory counts as
// readable — io.EOF is success).
func listable(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	_, err = f.Readdirnames(1)
	return err == nil || errors.Is(err, io.EOF)
}

// writable reports whether the current user can create a file in dir,
// via one transient probe file (created then removed).
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".kenaz-wsprobe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// statDevFn is the device-number probe, overridable in tests (bind mounts
// need root; tests fake the parent/child device split instead).
var statDevFn = func(path string) (uint64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}

// isMountpoint reports whether dir sits on a different device than its
// parent — true for a virtio-fs bind mount at /workspace, false for a
// plain (stub) directory. Unknown ⇒ false (treated as stub; the safe
// direction is the private fallback).
func isMountpoint(dir string) bool {
	self, ok := statDevFn(dir)
	if !ok {
		return false
	}
	parent, ok := statDevFn(filepath.Dir(dir))
	if !ok {
		return false
	}
	return self != parent
}
