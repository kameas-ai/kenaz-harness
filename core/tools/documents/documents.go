// Package documents implements the document built-in tools (spec 092
// FR-010) and the knowledge-site builder tool:
//
//	kenaz__save_document         create a sanitized HTML document
//	kenaz__update_document       write the next version of a document
//	kenaz__build_knowledge_site  build documents into a static site in the workspace
//
// All three are thin: argument parsing, the enable gate, session
// resolution, and result shaping. The rules — sanitize on write,
// classification frozen at personal, per-session visibility, the site
// format — live in core/docs, which is the only writer of document bodies.
//
// # Gates
//
// save_document shares the save_artifact Settings dial: it is the same
// "save a deliverable" intent into the same local store family, and a
// second dial for it would be a second knob with one behaviour.
// update_document and build_knowledge_site share the filesystem-write dial
// that gates kenaz__write_file. update_document is there because the
// repo's convention puts tools that change existing content behind that
// dial, and spec 092 plan R-4 (whether document updates may bypass it) is
// an unratified decision this package does not get to make.
// build_knowledge_site is there because it writes files into the
// workspace; it is also asked through the Cedar filesystem write gate.
//
// # What these tools never do
//
// Nothing here transmits a document anywhere. Publication is constitution
// §XIII: a host-side, per-document, explicitly reviewed user action. No
// agent tool may publish, so the site builder stops at the workspace and
// says so in its result.
//
// # Privacy
//
// Log lines carry ids, versions, sizes, counts and error codes. Never a
// title, a body, or a filesystem path (FR-032).
package documents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/docs"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// Tool names. The "kenaz__" prefix is reserved for built-ins.
const (
	NameSave      = "kenaz__save_document"
	NameUpdate    = "kenaz__update_document"
	NameBuildSite = "kenaz__build_knowledge_site"
)

// Store is the slice of *docs.Service the tools consume.
type Store interface {
	Save(ctx context.Context, sessionID, title, body string, prov docs.Provenance) (docs.Document, error)
	Update(ctx context.Context, sessionID, id string, baseVersion int, body string, prov docs.Provenance) (docs.Document, error)
	Get(ctx context.Context, sessionID, id string) (docs.Document, error)
	ListVisible(ctx context.Context, sessionID string) ([]docs.Document, error)
}

// Options configures the tools. Store is required.
type Options struct {
	Store Store
	// Enabled is consulted on every Call, as defence in depth against a
	// stale model-side catalog after the user flips the dial. nil means
	// always enabled.
	Enabled func() bool
	// WorkspaceDir resolves the agent workspace the site builder writes
	// under. Required by NewBuildSite.
	WorkspaceDir func() string
	// AuthorizeWrite is the Cedar filesystem write gate, asked about every
	// path the site builder is about to write — the same decision
	// kenaz__write_file gets, so a user's write-deny policy binds here too.
	// A non-nil error denies. Required by NewBuildSite.
	AuthorizeWrite func(ctx context.Context, path string) error
}

type base struct {
	store   Store
	enabled func() bool
	logger  *slog.Logger
}

func newBase(opts Options, who string) base {
	if opts.Store == nil {
		panic("documents." + who + ": nil Store")
	}
	b := base{store: opts.Store, enabled: opts.Enabled, logger: slog.Default()}
	if b.enabled == nil {
		b.enabled = func() bool { return true }
	}
	return b
}

// Error kinds returned to the model in addition to the core/docs codes.
const (
	errKindDisabled    = "disabled"
	errKindInvalidArgs = "invalid_args"
	errKindWriteDenied = "write_denied"
)

type errorResult struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// marshalErr returns a structured tool-level failure with no Go error, so
// the kernel shows it to the model as a normal result it can react to.
func marshalErr(kind, message string) (json.RawMessage, error) {
	return json.Marshal(errorResult{Error: kind, Message: message})
}

// docErr turns a core/docs error into a model-facing result. Messages are
// fixed per code so no document content can ride an error back out.
func (b base) docErr(tool string, err error) (json.RawMessage, error) {
	code := docs.SiteErrorCode(err)
	b.logger.Warn("documents.call_failed", "tool", tool, "code", code)
	msg, ok := errorMessages[code]
	if !ok {
		msg = "the document store could not complete the request"
	}
	return marshalErr(code, msg)
}

var errorMessages = map[string]string{
	docs.CodeNotFound:         "no document with that id is visible in this session",
	docs.CodeInvalidTitle:     fmt.Sprintf("title must be 1-%d characters with no control characters", docs.MaxTitleRunes),
	docs.CodeEmptyBody:        "the body is empty after removing scripts, embeds and external resources",
	docs.CodeNoSession:        "no session id in context — tool cannot run",
	docs.CodeVersionConflict:  "the document changed since base_version; fetch the current version and retry",
	docs.CodeContentTooLarge:  fmt.Sprintf("the body exceeds %d bytes; move large images out or split the document", docs.MaxBodyBytes),
	docs.CodeContentTooDeep:   fmt.Sprintf("the body nests elements deeper than %d levels", docs.MaxNestingDepth),
	docs.CodeContentNotUTF8:   "the body is not valid UTF-8",
	docs.CodeInvalidSiteSlug:  "slug must be lowercase letters, digits and single hyphens (max 40), with no '--'",
	docs.CodeNoSiteDocuments:  "there are no documents to build a site from",
	docs.CodeTooManyDocuments: fmt.Sprintf("a site may contain at most %d documents", docs.MaxSiteDocuments),
	docs.CodeExportTargetBusy: "the export directory for that slug is used by something else; pick another slug",
}

func parseArgs(argsJSON json.RawMessage, v any) error {
	if len(argsJSON) == 0 {
		return errors.New("empty args")
	}
	if err := json.Unmarshal(argsJSON, v); err != nil {
		return fmt.Errorf("parse args: %v", err)
	}
	return nil
}

// documentResult is what the model sees after a save or update.
type documentResult struct {
	DocumentID    string `json:"document_id"`
	Version       int    `json:"version"`
	ContentSHA256 string `json:"content_sha256"`
	Size          int    `json:"size"`
	// Sanitized is true when the stored body differs from what was sent,
	// so the model knows scripts, embeds or external references were
	// removed and can tell the user.
	Sanitized bool `json:"sanitized"`
}

func resultFor(d docs.Document, sent string) (json.RawMessage, error) {
	return json.Marshal(documentResult{
		DocumentID:    d.ID,
		Version:       d.Version,
		ContentSHA256: d.ContentSHA256,
		Size:          len(d.Body),
		Sanitized:     d.Body != sent,
	})
}

// ── kenaz__save_document ─────────────────────────────────────────────────

const saveSchema = `{
  "type": "object",
  "properties": {
    "title": {"type": "string", "description": "Document title shown in lists and as the page title."},
    "html": {"type": "string", "description": "The document body as HTML: headings, paragraphs, lists, tables, code blocks (Mermaid as <pre><code class=\"language-mermaid\">), https links, and images only as data: URIs. Scripts, embeds, forms and external resources are removed on save."}
  },
  "required": ["title", "html"]
}`

// SaveTool implements kenaz__save_document.
type SaveTool struct{ base }

// NewSave constructs the save tool.
func NewSave(opts Options) *SaveTool { return &SaveTool{newBase(opts, "NewSave")} }

// Name returns the tool identifier.
func (t *SaveTool) Name() string { return NameSave }

// Description returns the model-facing description.
func (t *SaveTool) Description() string {
	return "Create a rich HTML document in the user's Documents — runbooks, briefs, reports, " +
		"postmortems, knowledge-base pages. Prefer this over kenaz__save_artifact when the " +
		"deliverable is a readable document rather than a file. Returns a document id; the body " +
		"is sanitized on save and stays on this machine."
}

// InputSchema returns the argument schema.
func (t *SaveTool) InputSchema() json.RawMessage { return json.RawMessage(saveSchema) }

// Call creates a document.
func (t *SaveTool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	if !t.enabled() {
		return marshalErr(errKindDisabled, "save_document is disabled in Settings")
	}
	var args struct {
		Title string `json:"title"`
		HTML  string `json:"html"`
	}
	if err := parseArgs(argsJSON, &args); err != nil {
		return marshalErr(errKindInvalidArgs, err.Error())
	}
	session := toolloop.SessionIDFromContext(ctx)
	d, err := t.store.Save(ctx, session, args.Title, args.HTML, docs.Provenance{
		AuthorKind: docs.AuthorModelOutput, SessionID: session, Tool: NameSave,
	})
	if err != nil {
		return t.docErr(NameSave, err)
	}
	t.logger.Info("documents.saved", "document_id", d.ID, "version", d.Version, "size", len(d.Body))
	return resultFor(d, args.HTML)
}

// ── kenaz__update_document ───────────────────────────────────────────────

const updateSchema = `{
  "type": "object",
  "properties": {
    "document_id": {"type": "string", "description": "Id returned by kenaz__save_document."},
    "html": {"type": "string", "description": "The complete new body (not a diff). Same HTML rules as kenaz__save_document."},
    "base_version": {"type": "integer", "description": "The version this edit is based on. The update is refused if the document has changed since."}
  },
  "required": ["document_id", "html", "base_version"]
}`

// UpdateTool implements kenaz__update_document.
type UpdateTool struct{ base }

// NewUpdate constructs the update tool.
func NewUpdate(opts Options) *UpdateTool { return &UpdateTool{newBase(opts, "NewUpdate")} }

// Name returns the tool identifier.
func (t *UpdateTool) Name() string { return NameUpdate }

// Description returns the model-facing description.
func (t *UpdateTool) Description() string {
	return "Replace the body of an existing document with a new version. Earlier versions are kept. " +
		"Pass the version you last saw as base_version; a stale base is refused rather than overwritten."
}

// InputSchema returns the argument schema.
func (t *UpdateTool) InputSchema() json.RawMessage { return json.RawMessage(updateSchema) }

// Call writes the next version of a document.
func (t *UpdateTool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	if !t.enabled() {
		return marshalErr(errKindDisabled, "update_document is disabled in Settings (filesystem write tools)")
	}
	var args struct {
		DocumentID  string `json:"document_id"`
		HTML        string `json:"html"`
		BaseVersion *int   `json:"base_version"`
	}
	if err := parseArgs(argsJSON, &args); err != nil {
		return marshalErr(errKindInvalidArgs, err.Error())
	}
	if args.BaseVersion == nil || *args.BaseVersion < 0 {
		return marshalErr(errKindInvalidArgs, "base_version is required and must be >= 0")
	}
	session := toolloop.SessionIDFromContext(ctx)
	d, err := t.store.Update(ctx, session, strings.TrimSpace(args.DocumentID), *args.BaseVersion, args.HTML, docs.Provenance{
		AuthorKind: docs.AuthorModelOutput, SessionID: session, Tool: NameUpdate,
	})
	if err != nil {
		return t.docErr(NameUpdate, err)
	}
	t.logger.Info("documents.updated", "document_id", d.ID, "version", d.Version, "size", len(d.Body))
	return resultFor(d, args.HTML)
}

// ── kenaz__build_knowledge_site ──────────────────────────────────────────

const buildSiteSchema = `{
  "type": "object",
  "properties": {
    "slug": {"type": "string", "description": "Site name: lowercase letters, digits and single hyphens, at most 40 characters. Also the export directory name."},
    "title": {"type": "string", "description": "Heading for the site's catalog page. Defaults to the slug."},
    "document_ids": {"type": "array", "items": {"type": "string"}, "description": "Documents to include. Omit to include every document visible in this session."}
  },
  "required": ["slug"]
}`

// BuildSiteTool implements kenaz__build_knowledge_site.
type BuildSiteTool struct {
	base
	workspace func() string
	authorize func(ctx context.Context, path string) error
}

// NewBuildSite constructs the knowledge-site builder tool.
func NewBuildSite(opts Options) *BuildSiteTool {
	if opts.WorkspaceDir == nil {
		panic("documents.NewBuildSite: nil WorkspaceDir")
	}
	if opts.AuthorizeWrite == nil {
		panic("documents.NewBuildSite: nil AuthorizeWrite")
	}
	return &BuildSiteTool{base: newBase(opts, "NewBuildSite"), workspace: opts.WorkspaceDir, authorize: opts.AuthorizeWrite}
}

// Name returns the tool identifier.
func (t *BuildSiteTool) Name() string { return NameBuildSite }

// Description returns the model-facing description.
func (t *BuildSiteTool) Description() string {
	return "Build documents into a self-contained static knowledge site the user owns: a catalog page, " +
		"one page per document, Markdown companions, and a deterministic .tar.gz bundle, written to " +
		"documents-exports/<slug>/ in the workspace. Any static web server can serve it. This does NOT " +
		"publish or upload anything — publishing is a separate action the user takes outside the agent."
}

// InputSchema returns the argument schema.
func (t *BuildSiteTool) InputSchema() json.RawMessage { return json.RawMessage(buildSiteSchema) }

type siteResult struct {
	SiteDir      string                  `json:"site_dir"`
	Bundle       string                  `json:"bundle"`
	BundleSHA256 string                  `json:"bundle_sha256"`
	Documents    int                     `json:"documents"`
	Warnings     []docs.DocumentWarnings `json:"markdown_warnings,omitempty"`
	Published    bool                    `json:"published"`
	Note         string                  `json:"note"`
}

// Call builds and writes the site.
func (t *BuildSiteTool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	if !t.enabled() {
		return marshalErr(errKindDisabled, "build_knowledge_site is disabled in Settings (filesystem write tools)")
	}
	var args struct {
		Slug        string   `json:"slug"`
		Title       string   `json:"title"`
		DocumentIDs []string `json:"document_ids"`
	}
	if err := parseArgs(argsJSON, &args); err != nil {
		return marshalErr(errKindInvalidArgs, err.Error())
	}
	session := toolloop.SessionIDFromContext(ctx)

	var selected []docs.Document
	if len(args.DocumentIDs) == 0 {
		all, err := t.store.ListVisible(ctx, session)
		if err != nil {
			return t.docErr(NameBuildSite, err)
		}
		selected = all
	} else {
		if len(args.DocumentIDs) > docs.MaxSiteDocuments {
			return t.docErr(NameBuildSite, docs.ErrTooManyDocuments)
		}
		seen := map[string]bool{}
		for _, id := range args.DocumentIDs {
			id = strings.TrimSpace(id)
			if seen[id] {
				continue
			}
			seen[id] = true
			d, err := t.store.Get(ctx, session, id)
			if err != nil {
				return t.docErr(NameBuildSite, err)
			}
			selected = append(selected, d)
		}
	}

	site, err := docs.BuildSite(docs.SiteOptions{
		Slug:        strings.TrimSpace(args.Slug),
		Title:       args.Title,
		GeneratedAt: docs.SiteStamp(selected),
	}, selected)
	if err != nil {
		return t.docErr(NameBuildSite, err)
	}
	workspace := t.workspace()
	for _, target := range docs.SiteWritePaths(workspace, site.Manifest.Name) {
		if err := t.authorize(ctx, target); err != nil {
			t.logger.Warn("documents.call_failed", "tool", NameBuildSite, "code", errKindWriteDenied)
			return marshalErr(errKindWriteDenied, "filesystem policy denies writing the site into the workspace exports directory")
		}
	}
	written, err := docs.WriteSite(ctx, workspace, site)
	if err != nil {
		return t.docErr(NameBuildSite, err)
	}
	t.logger.Info("documents.site_built", "documents", len(selected), "bundle_sha256", written.BundleSHA256)
	return json.Marshal(siteResult{
		SiteDir:      written.Dir,
		Bundle:       written.Bundle,
		BundleSHA256: written.BundleSHA256,
		Documents:    len(selected),
		Warnings:     site.Warnings,
		Published:    false,
		Note: "Built locally only. Serve " + docs.ExportsDirName + "/" + site.Manifest.Name +
			"/public with any static web server; nothing was uploaded.",
	})
}
