package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// grant is one persistent permit the gate writes for an Allow-always
// decision: op on exactly path, or (dir) on path and everything below.
type grant struct {
	op   Op
	path string
	dir  bool
}

// covers reports whether g permits op on canonical.
func (g grant) covers(op Op, canonical string) bool {
	if g.op != op {
		return false
	}
	if !g.dir {
		return canonical == g.path
	}
	return canonical == g.path || isWithin(canonical, g.path)
}

// isWithin reports whether p sits strictly below dir.
func isWithin(p, dir string) bool {
	prefix := strings.TrimSuffix(dir, string(filepath.Separator)) + string(filepath.Separator)
	return strings.HasPrefix(p, prefix)
}

// isWithinOrEqual reports whether p is dir or sits below it.
func isWithinOrEqual(p, dir string) bool {
	return p == dir || isWithin(p, dir)
}

// foldPathCase is true where the default filesystems are case-insensitive
// (APFS/HFS+ on macOS, NTFS on Windows): there "~/.KENAZ/harness" names
// the same directory as "~/.kenaz/harness".
var foldPathCase = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

// lexicalWithinOrEqual is isWithinOrEqual with case folded where the
// platform's filesystems fold it.
func lexicalWithinOrEqual(p, dir string) bool {
	if !foldPathCase {
		return isWithinOrEqual(p, dir)
	}
	return isWithinOrEqual(strings.ToLower(p), strings.ToLower(dir))
}

// sameDirOrBelow reports whether p is dir, or sits below it, as the
// filesystem sees it, for the harness-state classification (E2E-3). A
// byte-prefix compare alone missed aliases of the same directory: a
// case variant on a case-insensitive volume, a path through a symlink
// (on macOS every /var path is /private/var underneath), or a hard link.
// So besides the (case-folded) lexical compare, every existing ancestor
// of p is checked with os.SameFile against dir. p itself need not exist
// (a file about to be written). Only used for state and workspace
// classification, never to widen a grant.
func sameDirOrBelow(p, dir string) bool {
	if p == "" || dir == "" {
		return false
	}
	if lexicalWithinOrEqual(p, dir) {
		return true
	}
	return sameFileAncestor(p, dir)
}

// inWorkspace is the workspace exemption's compare. It does not fold case
// lexically — the exemption is the fail-open direction, and a
// case-sensitive volume on macOS must not exempt a look-alike — but the
// os.SameFile walk still recognises a real alias (a symlinked workspace,
// or a case variant on a case-insensitive volume).
func inWorkspace(p, ws string) bool {
	if p == "" || ws == "" {
		return false
	}
	return isWithinOrEqual(p, ws) || sameFileAncestor(p, ws)
}

// sameFileAncestor reports whether p or one of its existing ancestors is
// the same directory as dir (os.SameFile). When p itself resolves through
// a symlink, the ancestors of its resolved form are walked too: os.Stat
// follows the link at p, but p's lexical parents are those of the link,
// not of its target.
func sameFileAncestor(p, dir string) bool {
	di, err := os.Stat(dir)
	if err != nil {
		return false
	}
	if sameFileAncestorOf(p, di) {
		return true
	}
	if r, err := filepath.EvalSymlinks(p); err == nil && r != filepath.Clean(p) {
		return sameFileAncestorOf(r, di)
	}
	return false
}

func sameFileAncestorOf(p string, di os.FileInfo) bool {
	for cur := filepath.Clean(p); ; {
		if fi, err := os.Stat(cur); err == nil && os.SameFile(fi, di) {
			return true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false
		}
		cur = parent
	}
}

// sameDirStrictlyBelow reports whether p sits strictly below dir as the
// filesystem sees it (see sameDirOrBelow).
func sameDirStrictlyBelow(p, dir string) bool {
	return sameDirOrBelow(p, dir) && !sameDirOrBelow(dir, p)
}

// insideState reports whether canonical is harness state under root: it
// is (or is below) root and the workspace exemption does not apply. The
// exemption is for the fallback workspace that lives INSIDE a state dir
// (<DataDir>/agent-workspace on a grant-less workbench): a path in that
// workspace is the agent's own working area. A workspace that CONTAINS a
// state dir (workspace = HOME, state = HOME/.kenaz/harness/prod) exempts
// nothing — before this, a path in <DataDir>/policy was "in the
// workspace" and lost every E2E-3 protection.
func insideState(canonical, root, ws string) bool {
	if !sameDirOrBelow(canonical, root) {
		return false
	}
	return !(ws != "" && sameDirOrBelow(ws, root) && inWorkspace(canonical, ws))
}

// privilege is the gate's E2E-3 classification of one canonical path.
type privilege struct {
	// inside is true when the path IS harness state: inside a StateDir
	// and not exempted as part of a workspace inside it (insideState).
	inside bool
	// dirCoversState is true when the path's parent directory contains a
	// StateDir, so a "this directory and below" grant would cover state.
	dirCoversState bool
	// root is the state directory involved, for the copy.
	root string
	// copy is the banner text for the prompt; empty when neither holds.
	copy string
}

// privilege classifies canonical against GateOptions.StateDirs.
func (g *Gate) privilege(canonical string) privilege {
	if len(g.opts.StateDirs) == 0 {
		return privilege{}
	}
	ws := g.workspace()
	for _, root := range g.opts.StateDirs {
		if insideState(canonical, root, ws) {
			return privilege{
				inside: true,
				root:   root,
				copy: "This is the harness's own state directory (" + root + "): session " +
					"databases, credential metadata and permission policies. It can be " +
					"allowed once, but will not be remembered.",
			}
		}
	}
	parent := filepath.Dir(canonical)
	for _, root := range g.opts.StateDirs {
		if sameDirOrBelow(root, parent) {
			return privilege{
				dirCoversState: true,
				root:           root,
				copy: "“This directory and below” here would include the harness's own " +
					"state directory (" + root + "), so Allow always is remembered for this " +
					"exact path only.",
			}
		}
	}
	return privilege{}
}

// StateRelation classifies canonical against the harness state
// directories: inside is true when canonical is (or is below) one of
// stateDirs, unless it is in a workspace that itself lies inside that
// state dir (the fallback workspace; see insideState — a workspace that
// contains a state dir exempts nothing); contains is true when canonical
// is a strict ancestor of one. root names the state directory involved.
// Paths are compared as the filesystem sees them (sameDirOrBelow), so a
// case variant, a symlinked alias or the real target of a symlinked state
// directory is classified the same as the spelling in stateDirs.
// Shared by the fs gate and the recipe_dir_add path
// (core/rpc/views/tools) so both draw the same line (E2E-3).
func StateRelation(canonical string, stateDirs []string, workspace string) (inside, contains bool, root string) {
	for _, r := range stateDirs {
		if insideState(canonical, r, workspace) {
			return true, false, r
		}
	}
	for _, r := range stateDirs {
		if sameDirOrBelow(canonical, r) {
			// Inside state but exempted: the fallback workspace.
			return false, false, ""
		}
	}
	for _, r := range stateDirs {
		if sameDirStrictlyBelow(r, canonical) {
			return false, true, r
		}
	}
	return false, false, ""
}

func (g *Gate) workspace() string {
	if g.opts.WorkspaceDir == nil {
		return ""
	}
	ws := g.opts.WorkspaceDir()
	if ws == "" {
		return ""
	}
	c, err := Canonicalize(ws)
	if err != nil {
		return ""
	}
	return c
}

// parentMissing reports whether canonical's parent directory does not
// exist yet.
func parentMissing(canonical string) bool {
	_, err := os.Stat(filepath.Dir(canonical))
	return errors.Is(err, os.ErrNotExist)
}

// grantsFor returns the persistent grants an Allow-always response on
// (op, canonical) produces, or none plus a reason when nothing may be
// persisted. note explains any narrowing so the decision reason (and
// the audit row) records it.
//
// This is the one place the meaning of a scope is decided: Evaluate
// persists exactly these grants, and GrantCoverage releases queued
// requests by exactly these grants, so a queued request is released
// only if the grant that was written would have permitted it anyway.
func (g *Gate) grantsFor(op Op, canonical string, resp PromptResponse) (grants []grant, note string) {
	priv := g.privilege(canonical)
	if priv.inside {
		return nil, "path is inside the harness state directory " + priv.root + " and cannot be remembered"
	}
	switch resp {
	case PromptAllowDirectory, PromptAllowDirectoryReadWrite:
		if priv.dirCoversState {
			note = "directory scope narrowed to the exact path: the directory contains the harness state directory " + priv.root
			break
		}
		dir := filepath.Dir(canonical)
		if !cedar.PersistableFamilyID(dir) {
			return nil, "directory cannot be persisted"
		}
		grants = append(grants, grant{op: op, path: dir, dir: true})
		if resp == PromptAllowDirectoryReadWrite && op == OpWrite {
			grants = append(grants, grant{op: OpRead, path: dir, dir: true})
		}
		return grants, ""
	case PromptAllowExact:
	default:
		return nil, "unknown grant scope"
	}
	if !cedar.PersistableFamilyID(canonical) {
		return nil, "path cannot be persisted"
	}
	return []grant{{op: op, path: canonical}}, note
}

// persist writes one snippet per grant, then reloads the engine so the
// grants bind in this process. Before the reload existed, an
// Allow-always (exact OR directory) wrote its file and the very next
// call prompted again: nothing re-read the policy directory until the
// next restart or an unrelated policy edit (E2E-1).
func (g *Gate) persist(ctx context.Context, grants []grant) error {
	if g.opts.PolicyDir == "" {
		return errors.New("no policy directory configured")
	}
	for _, gr := range grants {
		var name, body string
		if gr.dir {
			name, body = snippetName(gr.op, gr.path, "dir"), buildDirectorySnippet(gr.op, gr.path)
		} else {
			name, body = snippetName(gr.op, gr.path, "path"), buildExactSnippet(gr.op, gr.path)
		}
		if err := g.writePolicySnippet(name, body); err != nil {
			return err
		}
	}
	if r, ok := g.opts.Engine.(reloader); ok {
		if err := r.Reload(context.WithoutCancel(ctx)); err != nil {
			return err
		}
	}
	return nil
}

func describeGrants(grants []grant) string {
	parts := make([]string, 0, len(grants))
	for _, gr := range grants {
		if gr.dir {
			parts = append(parts, string(gr.op)+" "+gr.path+" and below")
		} else {
			parts = append(parts, string(gr.op)+" exact path")
		}
	}
	return strings.Join(parts, ", ")
}

// responseForScope maps the registry's GrantScope onto the gate's
// response vocabulary. Unknown or empty scopes are the exact path: an
// unrecognised scope never widens a grant.
func responseForScope(scope cedar.GrantScope) PromptResponse {
	switch scope {
	case cedar.ScopeDirectory:
		return PromptAllowDirectory
	case cedar.ScopeDirectoryReadWrite:
		return PromptAllowDirectoryReadWrite
	default:
		return PromptAllowExact
	}
}

// GrantCoverage is the gate's cedar.GrantCoverage for the filesystem
// family. Install it with Registry.SetGrantCoverage(cedar.FamilyFilesystem,
// gate.GrantCoverage) so a queued fs prompt is released the moment the
// person answers an earlier one with a grant that covers it.
//
// Coverage is deliberately no wider than what Evaluate persists for the
// same decision (both go through grantsFor), and never releases a
// queued request that is itself dangerous-tier or privileged: those
// were never going to be satisfied by a remembered grant, so the person
// still sees them.
func (g *Gate) GrantCoverage(granted cedar.PendingRequest, scope cedar.GrantScope) func(cedar.PendingRequest) bool {
	fsSurf := granted.Surface.FS
	if fsSurf == nil {
		return nil
	}
	op, ok := gateOp(fsSurf.Op)
	if !ok {
		return nil
	}
	if fsSurf.Dangerous && !g.opts.AllowDangerousPersist {
		return nil
	}
	grants, _ := g.grantsFor(op, fsSurf.CanonicalPath, responseForScope(scope))
	if len(grants) == 0 {
		return nil
	}
	return func(req cedar.PendingRequest) bool {
		s := req.Surface.FS
		if s == nil || s.Dangerous || req.RequestID == granted.RequestID {
			return false
		}
		qop, ok := gateOp(s.Op)
		if !ok {
			return false
		}
		if g.privilege(s.CanonicalPath).inside {
			return false
		}
		for _, gr := range grants {
			if gr.covers(qop, s.CanonicalPath) {
				return true
			}
		}
		return false
	}
}

// gateOp maps a prompt surface op string onto the gate's Op. Only read
// and write prompts come from this gate; recipe_dir_add and the rest
// are other gates' prompts and are never covered.
func gateOp(s string) (Op, bool) {
	switch Op(s) {
	case OpRead:
		return OpRead, true
	case OpWrite:
		return OpWrite, true
	}
	return "", false
}
