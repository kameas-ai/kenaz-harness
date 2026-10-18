package documents

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/docs"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// Options wires New. Every field is required.
type Options struct {
	// Store is the document store; it owns visibility and sanitization.
	Store *docs.Service
	// SessionExists returns nil when sessionID names an existing session.
	SessionExists func(ctx context.Context, sessionID string) error
	// WorkspaceDir resolves the agent workspace site builds write under.
	WorkspaceDir func() string
}

// New constructs the surface.
func New(opts Options) DocumentsAPI {
	if opts.Store == nil || opts.SessionExists == nil || opts.WorkspaceDir == nil {
		panic("documents.New: Store, SessionExists and WorkspaceDir are required")
	}
	return &impl{opts: opts}
}

type impl struct {
	opts Options
}

// messages are the fixed, content-free texts for each wire code.
var messages = map[string]string{
	docs.CodeNotFound:         "no document with that id is visible in this session",
	docs.CodeInvalidTitle:     fmt.Sprintf("title must be 1-%d characters with no control characters", docs.MaxTitleRunes),
	docs.CodeEmptyBody:        "the body is empty after removing scripts, embeds and external resources",
	docs.CodeNoSession:        "no session with that id exists",
	docs.CodeVersionConflict:  "this document changed since you opened it; reload to see the current version",
	docs.CodeContentTooLarge:  fmt.Sprintf("the body exceeds %d bytes", docs.MaxBodyBytes),
	docs.CodeContentTooDeep:   fmt.Sprintf("the body nests elements deeper than %d levels", docs.MaxNestingDepth),
	docs.CodeContentNotUTF8:   "the body is not valid UTF-8",
	docs.CodeInvalidSiteSlug:  "site name must be lowercase letters, digits and single hyphens, at most 40 characters",
	docs.CodeNoSiteDocuments:  "select at least one document",
	docs.CodeTooManyDocuments: fmt.Sprintf("a site may contain at most %d documents", docs.MaxSiteDocuments),
	docs.CodeExportTargetBusy: "a different folder already uses that site name in the exports directory; pick another name",
	CodeBadParams:             "the request was malformed",
	CodeUnavailable:           "the document store is not available in this build",
	docs.CodeInternal:         "the document store could not complete the request",
}

// fail converts any error into the contract's *Error, logging only the code.
func fail(op string, err error) error {
	code := ErrorCode(err)
	if code == "" {
		code = docs.SiteErrorCode(err)
	}
	msg, ok := messages[code]
	if !ok {
		code, msg = docs.CodeInternal, messages[docs.CodeInternal]
	}
	logging.L().Warn("documents.rpc_failed", "op", op, "code", code)
	return &Error{Code: code, Message: msg}
}

func badParams(what string) error {
	return &Error{Code: CodeBadParams, Message: messages[CodeBadParams] + ": " + what}
}

// session enforces contract §2.2: the session must exist.
func (a *impl) session(ctx context.Context, op, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fail(op, docs.ErrNoSession)
	}
	if err := a.opts.SessionExists(ctx, sessionID); err != nil {
		return fail(op, docs.ErrNoSession)
	}
	return nil
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func summary(d docs.Document) DocumentSummary {
	return DocumentSummary{
		ID: d.ID, Title: d.Title, Scope: string(d.Scope), Version: d.Version,
		ContentSHA256: d.ContentSHA256, ByteSize: len(d.Body),
		CreatedAt: stamp(d.CreatedAt), UpdatedAt: stamp(d.UpdatedAt),
	}
}

func full(d docs.Document) Document {
	s := summary(d)
	return Document{
		ID: s.ID, Title: s.Title, Scope: s.Scope, Version: s.Version,
		ContentSHA256: s.ContentSHA256, ByteSize: s.ByteSize,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Body: d.Body,
	}
}

func userProv(sessionID string) docs.Provenance {
	return docs.Provenance{AuthorKind: docs.AuthorUserEdit, SessionID: sessionID}
}

func (a *impl) List(ctx context.Context, sessionID string) ([]DocumentSummary, error) {
	if err := a.session(ctx, "list", sessionID); err != nil {
		return nil, err
	}
	ds, err := a.opts.Store.ListVisible(ctx, sessionID)
	if err != nil {
		return nil, fail("list", err)
	}
	out := make([]DocumentSummary, 0, len(ds))
	for _, d := range ds {
		out = append(out, summary(d))
	}
	return out, nil
}

func (a *impl) Get(ctx context.Context, sessionID, id string) (Document, error) {
	if err := a.session(ctx, "get", sessionID); err != nil {
		return Document{}, err
	}
	d, err := a.opts.Store.Get(ctx, sessionID, strings.TrimSpace(id))
	if err != nil {
		return Document{}, fail("get", err)
	}
	return full(d), nil
}

func (a *impl) Create(ctx context.Context, sessionID, title, body string) (Document, error) {
	if err := a.session(ctx, "create", sessionID); err != nil {
		return Document{}, err
	}
	d, err := a.opts.Store.Save(ctx, sessionID, title, body, userProv(sessionID))
	if err != nil {
		return Document{}, fail("create", err)
	}
	return full(d), nil
}

func (a *impl) Update(ctx context.Context, sessionID, id string, baseVersion int, body string) (Document, error) {
	if baseVersion < 0 {
		// The store treats a negative base as "skip the check"; the UI must
		// never be able to ask for that.
		return Document{}, badParams("baseVersion must be >= 0")
	}
	if err := a.session(ctx, "update", sessionID); err != nil {
		return Document{}, err
	}
	d, err := a.opts.Store.Update(ctx, sessionID, strings.TrimSpace(id), baseVersion, body, userProv(sessionID))
	if err != nil {
		return Document{}, fail("update", err)
	}
	return full(d), nil
}

func (a *impl) Preview(_ context.Context, body string) (PreviewResult, error) {
	if strings.TrimSpace(body) == "" {
		return PreviewResult{}, nil
	}
	clean, err := docs.Sanitize(body)
	if err != nil {
		return PreviewResult{}, fail("preview", err)
	}
	return PreviewResult{HTML: clean, Sanitized: clean != body, ByteSize: len(clean)}, nil
}

func (a *impl) BuildSite(ctx context.Context, sessionID, slug, title string, documentIDs []string) (SiteBuildResult, error) {
	if err := a.session(ctx, "build_site", sessionID); err != nil {
		return SiteBuildResult{}, err
	}
	if len(documentIDs) > docs.MaxSiteDocuments {
		return SiteBuildResult{}, fail("build_site", docs.ErrTooManyDocuments)
	}
	workspace := a.opts.WorkspaceDir()
	if workspace == "" {
		return SiteBuildResult{}, fail("build_site", &Error{Code: CodeUnavailable})
	}

	// Contract §2.4: every document goes through the store's visibility.
	var selected []docs.Document
	if len(documentIDs) == 0 {
		ds, err := a.opts.Store.ListVisible(ctx, sessionID)
		if err != nil {
			return SiteBuildResult{}, fail("build_site", err)
		}
		selected = ds
	} else {
		seen := map[string]bool{}
		for _, id := range documentIDs {
			id = strings.TrimSpace(id)
			if seen[id] {
				continue
			}
			seen[id] = true
			d, err := a.opts.Store.Get(ctx, sessionID, id)
			if err != nil {
				return SiteBuildResult{}, fail("build_site", err)
			}
			selected = append(selected, d)
		}
	}

	site, err := docs.BuildSite(docs.SiteOptions{
		Slug:        strings.TrimSpace(slug),
		Title:       title,
		GeneratedAt: docs.SiteStamp(selected),
	}, selected)
	if err != nil {
		return SiteBuildResult{}, fail("build_site", err)
	}
	written, err := docs.WriteSite(ctx, workspace, site)
	if err != nil {
		return SiteBuildResult{}, fail("build_site", err)
	}
	warnings := make([]SiteWarnings, 0, len(site.Warnings))
	for _, w := range site.Warnings {
		warnings = append(warnings, SiteWarnings{DocumentID: w.DocumentID, Warnings: w.Warnings})
	}
	logging.L().Info("documents.site_built", "documents", len(selected), "bundle_sha256", written.BundleSHA256)
	return SiteBuildResult{
		SiteDir:      written.Dir,
		PublicDir:    filepath.Join(written.Dir, docs.SitePublicDir),
		Bundle:       written.Bundle,
		BundleSHA256: written.BundleSHA256,
		Documents:    len(selected),
		Warnings:     warnings,
		Published:    false,
	}, nil
}

func (a *impl) ExportsDir(context.Context) (ExportsDirResult, error) {
	workspace := a.opts.WorkspaceDir()
	if workspace == "" {
		return ExportsDirResult{}, fail("exports_dir", &Error{Code: CodeUnavailable})
	}
	return ExportsDirResult{Dir: filepath.Join(workspace, docs.ExportsDirName)}, nil
}
