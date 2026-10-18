// Package documents is the view-scoped surface backing the Documents UI:
// list, read, create, edit, preview, and build a local knowledge site.
//
// The wire contract, including the authorization model, is
// contracts/documents-rpc.md. In short: every document call names an
// existing session, and what that session may see is decided by
// core/docs.Service — this package never widens it. Nothing here publishes
// or uploads.
package documents

import (
	"context"
	"errors"
	"fmt"
)

// DocumentSummary is a list row. Timestamps are RFC3339Nano UTC.
type DocumentSummary struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Scope         string `json:"scope"`
	Version       int    `json:"version"`
	ContentSHA256 string `json:"contentSha256"`
	ByteSize      int    `json:"byteSize"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

// Document is a full document. Body is the sanitized, stored HTML.
type Document struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Scope         string `json:"scope"`
	Version       int    `json:"version"`
	ContentSHA256 string `json:"contentSha256"`
	ByteSize      int    `json:"byteSize"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
	Body          string `json:"body"`
}

// PreviewResult is what Create/Update would store for a body.
type PreviewResult struct {
	HTML      string `json:"html"`
	Sanitized bool   `json:"sanitized"`
	ByteSize  int    `json:"byteSize"`
}

// SiteWarnings are Markdown-companion degradation warnings for one document.
type SiteWarnings struct {
	DocumentID string   `json:"documentId"`
	Warnings   []string `json:"warnings"`
}

// SiteBuildResult reports a completed local site build. Paths are absolute
// on the machine running the harness. Published is always false.
type SiteBuildResult struct {
	SiteDir      string         `json:"siteDir"`
	PublicDir    string         `json:"publicDir"`
	Bundle       string         `json:"bundle"`
	BundleSHA256 string         `json:"bundleSha256"`
	Documents    int            `json:"documents"`
	Warnings     []SiteWarnings `json:"warnings"`
	Published    bool           `json:"published"`
}

// ExportsDirResult names the directory site builds write under.
type ExportsDirResult struct {
	Dir string `json:"dir"`
}

// DocumentsAPI is the view-scoped surface. See contracts/documents-rpc.md §3.
type DocumentsAPI interface {
	List(ctx context.Context, sessionID string) ([]DocumentSummary, error)
	Get(ctx context.Context, sessionID, id string) (Document, error)
	Create(ctx context.Context, sessionID, title, body string) (Document, error)
	Update(ctx context.Context, sessionID, id string, baseVersion int, body string) (Document, error)
	Preview(ctx context.Context, body string) (PreviewResult, error)
	BuildSite(ctx context.Context, sessionID, slug, title string, documentIDs []string) (SiteBuildResult, error)
	ExportsDir(ctx context.Context) (ExportsDirResult, error)
}

// Error codes this package adds to the core/docs wire codes.
const (
	CodeBadParams   = "bad_params"
	CodeUnavailable = "unavailable"
)

// Error is the only error type the surface returns. Its string form,
// "documents: <code>: <message>", is the wire contract (§4): messages are
// fixed per code and never carry document text, so callers may show them
// verbatim and parse the code.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("documents: %s: %s", e.Code, e.Message) }

// ErrorCode extracts the wire code from an error this surface returned, or
// "" for any other error.
func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Unavailable is the surface on a chassis with no database: every call
// fails with CodeUnavailable rather than returning empty data that would
// read as "you have no documents".
func Unavailable() DocumentsAPI { return unavailable{} }

type unavailable struct{}

var errUnavailable = &Error{Code: CodeUnavailable, Message: "the document store is not available in this build"}

func (unavailable) List(context.Context, string) ([]DocumentSummary, error) {
	return nil, errUnavailable
}
func (unavailable) Get(context.Context, string, string) (Document, error) {
	return Document{}, errUnavailable
}
func (unavailable) Create(context.Context, string, string, string) (Document, error) {
	return Document{}, errUnavailable
}
func (unavailable) Update(context.Context, string, string, int, string) (Document, error) {
	return Document{}, errUnavailable
}
func (unavailable) Preview(context.Context, string) (PreviewResult, error) {
	return PreviewResult{}, errUnavailable
}
func (unavailable) BuildSite(context.Context, string, string, string, []string) (SiteBuildResult, error) {
	return SiteBuildResult{}, errUnavailable
}
func (unavailable) ExportsDir(context.Context) (ExportsDirResult, error) {
	return ExportsDirResult{}, errUnavailable
}
