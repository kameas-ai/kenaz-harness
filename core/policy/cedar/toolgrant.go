package cedar

// Durable "always allow" grants for the confirm-each flow
// (confirm-each-enforcement-01PMAG05 WP03).
//
// The confirmation modal offers two ways to carry an answer forward
// (owner decision 2): "allow for this session", which lives in process
// memory and dies with the process, and a visually-separated "always
// allow", which must survive a restart AND be revocable.
//
// "Revocable" is why this writes a Cedar snippet rather than inventing a
// third permission store. The permissions view already enumerates
// <DataDir>/policy/<family>_allow_*.cedar as user-revocable grants and
// already knows the "tool" family (ListGrants / RevokeGrant, see
// core/rpc/views/permissions/impl.go). Writing into that convention
// means the grant shows up in the Settings list the day it is written
// and RevokeGrant deletes it — no new UI, no second source of truth,
// and no way for a persisted grant to become invisible.
//
// The grant is also the LOOKUP: HasToolAllowGrant asks whether the file
// exists. That is deliberate — it makes revocation immediate and total.
// Delete the file and the next call prompts again, with no cache to
// invalidate and no process to restart.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrInvalidToolGrant is returned when server/tool cannot produce a safe
// filename — an empty tool name, or names that sanitise to nothing.
var ErrInvalidToolGrant = errors.New("cedar: invalid (server, tool) for a tool-allow grant")

// toolAllowPrefix is the filename prefix the permissions view's
// familyFromFilename maps to FamilyTool. Changing it orphans every
// previously-written grant from the Settings list, so don't.
const toolAllowPrefix = "tool_allow_"

// ToolAllowGrantFilename returns the canonical grant filename for
// (server, tool): "tool_allow_<server>__<tool>.cedar", sanitised.
//
// The double underscore mirrors the harness's "<server>__<tool>" tool
// naming so a human reading the Settings grants list can tell what a
// grant covers without opening the file. An empty server becomes
// "builtin", matching ToolUID.
func ToolAllowGrantFilename(server, tool string) (string, error) {
	if strings.TrimSpace(tool) == "" {
		return "", ErrInvalidToolGrant
	}
	if strings.TrimSpace(server) == "" {
		server = "builtin"
	}
	s := sanitizeToolIDForFilename(server)
	t := sanitizeToolIDForFilename(tool)
	// sanitize maps every disallowed rune to '_', so a name made only of
	// disallowed runes becomes all-underscore rather than empty. Guard
	// the degenerate "nothing meaningful survived" case anyway.
	if strings.Trim(s, "_") == "" || strings.Trim(t, "_") == "" {
		return "", ErrInvalidToolGrant
	}
	return toolAllowPrefix + s + "__" + t + ".cedar", nil
}

// sanitizeToolIDForFilename is sanitizeMCPIDForFilename minus the dot.
//
// The MCP variant keeps "." because recipe ids look like reverse-DNS
// names. Tool grants cannot: the permissions view's isPolicyFilename
// REJECTS any filename containing "..", so a server named "../../etc"
// would sanitise to "tool_allow_.._.._etc__passwd.cedar" — a file that
// exists on disk, grants a permission, and can never be listed or
// revoked from Settings. An unrevocable grant is worse than no grant.
//
// Everything outside [a-z0-9-] becomes "_", which cannot produce a "."
// at all, so the guard can never fire on a name we wrote.
func sanitizeToolIDForFilename(id string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

// toolGrantPath resolves the on-disk path for a grant, or "" when
// dataDir is empty.
func toolGrantPath(dataDir, filename string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, PolicyDir, filename)
}

// HasToolAllowGrant reports whether a durable allow grant exists for
// (server, tool).
//
// The lookup reads the file and compares the resource id embedded in the
// permit body against ToolUID(server, tool). It does NOT stop at "a file
// with the right name exists", and that distinction is the whole point:
//
// The filename is sanitised (everything outside [a-z0-9-] becomes "_"),
// so it is many-to-one. A grant the user approved for the server
// "foo.bar" and one they never approved for "foo_bar" land on the same
// filename, tool_allow_foo_bar__t.cedar. A stat-based lookup therefore
// answered "yes, approved" for a server the user had never seen — the
// rule BODY was always exact, so Cedar itself would have refused, but
// nothing on the confirm path consults Cedar. The lookup was the lie.
//
// Comparing the body closes it without a filename migration: grants
// already on disk carry their exact resource id and keep working.
//
// Failure is closed in every direction. A missing file, an unreadable
// one, a directory in its place, a body we cannot parse, or a body whose
// resource id names a different tool all report false — which prompts.
// An unreadable policy directory must never read as permission.
//
// Collision note: two servers that sanitise alike share a filename, so
// the second WriteGrant overwrites the first. The loser then reads as
// ungranted and prompts again, which is the safe direction; it is an
// annoyance (a grant that quietly stops sticking), not a hole. Fixing it
// properly means disambiguating the filename, which is a migration.
func HasToolAllowGrant(dataDir, server, tool string) bool {
	if dataDir == "" {
		return false
	}
	filename, err := ToolAllowGrantFilename(server, tool)
	if err != nil {
		return false
	}
	want, err := toolGrantResourceID(server, tool)
	if err != nil {
		return false
	}
	got, ok := readToolGrantResourceID(toolGrantPath(dataDir, filename))
	return ok && got == want
}

// readToolGrantResourceID reads a grant file and returns the resource id
// its permit names. ok=false for any file that is missing, unreadable, a
// directory, or not shaped like a grant we wrote.
func readToolGrantResourceID(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return "", false
	}
	body, err := os.ReadFile(path) // #nosec G304 — path is built from a sanitised filename under PolicyDir
	if err != nil {
		return "", false
	}
	return parseToolGrantResourceID(string(body))
}

// parseToolGrantResourceID pulls the id out of `resource == Tool::"<id>"`.
//
// Deliberately a narrow literal match rather than a Cedar parse: this
// only ever needs to read back snippets WriteToolAllowGrant produced, and
// a permissive parser here would be a way for a hand-edited policy file
// to widen a grant the confirm path consults. Anything that does not
// match the exact shape we write reports not-a-grant, and not-a-grant
// prompts.
func parseToolGrantResourceID(body string) (string, bool) {
	const marker = `resource == ` + EntityTypeTool + `::"`
	i := strings.Index(body, marker)
	if i < 0 {
		return "", false
	}
	rest := body[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j <= 0 {
		// j == 0 is an empty id, which is not a grant for anything.
		return "", false
	}
	return rest[:j], true
}

// toolGrantResourceID is the exact Cedar entity id a grant for
// (server, tool) names — the unsanitised "<server>__<tool>", via ToolUID
// so the written body and the lookup can never drift apart.
//
// It rejects ids carrying a quote, a backslash, or a newline. Those
// cannot appear in a snippet we write, because the snippet interpolates
// the id into a quoted Cedar string: a tool named `x"); permit(principal,
// action, resource); //` would otherwise write a policy granting
// everything to everyone. The names come from MCP servers, which are
// third-party, so this is reachable input and not a theoretical one.
func toolGrantResourceID(server, tool string) (string, error) {
	id := uidID(ToolUID(server, tool))
	if id == "" || strings.ContainsAny(id, "\"\\\n\r") {
		return "", ErrInvalidToolGrant
	}
	return id, nil
}

// WriteToolAllowGrant persists an allow grant for (server, tool) at
// <dataDir>/policy/tool_allow_<server>__<tool>.cedar and reloads the
// engine so the permit is live in this process.
//
// Returns the grant filename — the id the Settings list round-trips to
// RevokeGrant — alongside any error. Idempotent: writing twice
// rewrites the same file with the same body.
//
// engine may be nil (pre-boot / test paths): the file is still written,
// which is what makes the grant durable; only the in-process reload is
// skipped. A reload failure is returned but the file stays — the grant
// is on disk and takes effect at the next load either way.
func WriteToolAllowGrant(ctx context.Context, dataDir string, engine *Engine, server, tool string) (string, error) {
	if dataDir == "" {
		return "", errors.New("cedar: data dir not configured; cannot persist a tool grant")
	}
	filename, err := ToolAllowGrantFilename(server, tool)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(dataDir, PolicyDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	resourceID, err := toolGrantResourceID(server, tool)
	if err != nil {
		return "", err
	}
	body := "// Written by the confirm-each \"always allow\" control\n" +
		"// (confirm-each-enforcement-01PMAG05 WP03). Revoke from\n" +
		"// Settings → Permissions, or delete this file.\n" +
		"permit(\n" +
		"  principal,\n" +
		"  action == Action::\"" + ActionUseTool + "\",\n" +
		"  resource == " + EntityTypeTool + "::\"" + resourceID + "\"\n" +
		");\n"
	if err := os.WriteFile(toolGrantPath(dataDir, filename), []byte(body), 0o644); err != nil {
		return "", err
	}
	if engine != nil {
		if err := engine.Reload(ctx); err != nil {
			// The file landed; report the reload failure so the caller
			// can log it, but the grant is real and durable.
			return filename, err
		}
	}
	return filename, nil
}

// RemoveToolAllowGrant deletes a durable grant. Present so tests and the
// session-teardown path can revoke without reaching through the
// permissions view; the user-facing revoke stays PermissionsAPI.RevokeGrant.
// A missing file is not an error — revoking twice is idempotent.
//
// The body is checked before the delete, for the same reason the lookup
// checks it: the filename is many-to-one, so removing by name alone could
// delete a grant belonging to a different server that happens to sanitise
// alike. Revoking something that is not ours is a no-op, not a deletion.
func RemoveToolAllowGrant(ctx context.Context, dataDir string, engine *Engine, server, tool string) error {
	if dataDir == "" {
		return nil
	}
	filename, err := ToolAllowGrantFilename(server, tool)
	if err != nil {
		return err
	}
	if !HasToolAllowGrant(dataDir, server, tool) {
		// Either nothing is there or what is there belongs to someone
		// else. Both are "this grant does not exist", which is the state
		// the caller asked for.
		return nil
	}
	if err := os.Remove(toolGrantPath(dataDir, filename)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if engine != nil {
		return engine.Reload(ctx)
	}
	return nil
}

// uidID extracts the string id from a cedar EntityUID for embedding in a
// policy body. Kept local so the snippet text is built from the same
// helper that builds the runtime entity, not a hand-repeated format.
func uidID(uid interface{ String() string }) string {
	// EntityUID.String() renders as `Tool::"<id>"`; take what is between
	// the quotes so the caller can re-quote it in the permit body.
	s := uid.String()
	first := strings.Index(s, `"`)
	last := strings.LastIndex(s, `"`)
	if first < 0 || last <= first {
		return s
	}
	return s[first+1 : last]
}
