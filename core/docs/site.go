package docs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/sites"
)

// Knowledge sites: a set of documents built into a static site the customer
// owns.
//
// A knowledge site is a directory, not a service:
//
//	kameas-site.json            Fleet Sites manifest v1, type "static"
//	public/index.html           catalog of the site's documents
//	public/d/<doc-id>.html      one self-contained page per document
//	public/d/<doc-id>.md        Markdown companion (FR-015 degradation rules)
//
// plus, next to the directory, <slug>.tar.gz — the deterministic bundle
// core/sites.Pack produces, byte-for-byte the artifact the Fleet Sites
// static-deploy contract accepts.
//
// Building a site never transmits anything. It writes under the workspace's
// documents-exports directory (FR-017) and stops. That boundary is the
// point: publication to Kameas-operated or org-operated serving
// infrastructure is constitution §XIII — an explicit, per-document,
// host-side action with exact-bytes review — and it does not exist yet.
// What does exist is the part §IX.5 needs first: a site the customer can
// serve today from any static web server they control, and later hand to
// Fleet Sites unchanged, or take back out again.

// ExportsDirName is the workspace subdirectory document exports land in
// (spec 092 FR-017).
const ExportsDirName = "documents-exports"

// siteDir is the manifest's static.dir. Keeping pages out of the site root
// keeps kameas-site.json out of what a static server serves.
const siteDir = "public"

// MaxSiteDocuments bounds a single site build. The unit store's list cap
// is 200; a site is built from a list.
const MaxSiteDocuments = 200

// Site build errors.
var (
	ErrInvalidSiteSlug  = errors.New("docs: invalid site slug")
	ErrNoSiteDocuments  = errors.New("docs: no documents for site")
	ErrTooManyDocuments = errors.New("docs: too many documents for one site")
	ErrExportTargetBusy = errors.New("docs: export target exists and is not a knowledge site with this slug")
)

// Wire codes for site build errors.
const (
	CodeInvalidSiteSlug  = "invalid_site_slug"
	CodeNoSiteDocuments  = "no_documents"
	CodeTooManyDocuments = "too_many_documents"
	CodeExportTargetBusy = "export_target_in_use"
)

// SiteErrorCode maps a site build error onto its wire code, falling back
// to ServiceErrorCode.
func SiteErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrInvalidSiteSlug):
		return CodeInvalidSiteSlug
	case errors.Is(err, ErrNoSiteDocuments):
		return CodeNoSiteDocuments
	case errors.Is(err, ErrTooManyDocuments):
		return CodeTooManyDocuments
	case errors.Is(err, ErrExportTargetBusy):
		return CodeExportTargetBusy
	default:
		return ServiceErrorCode(err)
	}
}

// documentIDPattern is the unit id grammar (26-char Crockford base32
// ULID). Page filenames are document ids, so anything else is refused
// rather than escaped.
var documentIDPattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// SiteOptions configures BuildSite.
type SiteOptions struct {
	// Slug is the site name. It must satisfy the Fleet Sites slug grammar,
	// enforced by sites.Validate.
	Slug string
	// Title is the catalog heading. Empty falls back to Slug.
	Title string
	// GeneratedAt stamps every page. Callers pass a value derived from the
	// documents so an unchanged site rebuilds to an identical bundle.
	GeneratedAt time.Time
}

// DocumentWarnings are the Markdown degradation warnings for one document.
type DocumentWarnings struct {
	DocumentID string   `json:"document_id"`
	Warnings   []string `json:"warnings"`
}

// Site is a built knowledge site held in memory.
type Site struct {
	Manifest sites.Manifest
	// Files maps slash-separated paths relative to the site root to their
	// contents.
	Files    map[string][]byte
	Warnings []DocumentWarnings
}

// BuildSite renders documents into a knowledge site.
func BuildSite(opts SiteOptions, documents []Document) (Site, error) {
	manifest := sites.Manifest{
		Version: 1,
		Name:    opts.Slug,
		Type:    sites.KindStatic,
		Static:  &sites.StaticSection{Dir: siteDir},
	}
	if err := sites.Validate(manifest); err != nil {
		return Site{}, fmt.Errorf("%w: %v", ErrInvalidSiteSlug, err)
	}
	if len(documents) == 0 {
		return Site{}, ErrNoSiteDocuments
	}
	if len(documents) > MaxSiteDocuments {
		return Site{}, ErrTooManyDocuments
	}
	title, err := normaliseTitle(opts.Title)
	if err != nil {
		title = opts.Slug
	}

	ordered := append([]Document(nil), documents...)
	sort.SliceStable(ordered, func(i, j int) bool {
		ti, tj := strings.ToLower(ordered[i].Title), strings.ToLower(ordered[j].Title)
		if ti != tj {
			return ti < tj
		}
		return ordered[i].ID < ordered[j].ID
	})

	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Site{}, fmt.Errorf("docs: marshal site manifest: %w", err)
	}
	site := Site{
		Manifest: manifest,
		Files:    map[string][]byte{"kameas-site.json": append(manifestJSON, '\n')},
	}

	nav := `<nav><a href="../index.html">&larr; ` + html.EscapeString(title) + `</a></nav>`
	var catalog strings.Builder
	seen := map[string]bool{}
	for _, d := range ordered {
		if !documentIDPattern.MatchString(d.ID) || seen[d.ID] {
			return Site{}, fmt.Errorf("%w: invalid or duplicate document id", ErrDocumentNotFound)
		}
		seen[d.ID] = true

		page, err := documentPage(d, opts.GeneratedAt, nav)
		if err != nil {
			return Site{}, err
		}
		md, err := ExportMarkdown(d)
		if err != nil {
			return Site{}, err
		}
		site.Files[path.Join(siteDir, "d", d.ID+".html")] = page
		site.Files[path.Join(siteDir, "d", d.ID+".md")] = []byte(md.Markdown)
		if len(md.Warnings) > 0 {
			site.Warnings = append(site.Warnings, DocumentWarnings{DocumentID: d.ID, Warnings: md.Warnings})
		}

		fmt.Fprintf(&catalog, "<li><a href=\"d/%s.html\">%s</a><div class=\"meta\">Version %d &middot; updated %s &middot; <a href=\"d/%s.md\">Markdown</a></div></li>\n",
			d.ID, html.EscapeString(d.Title), d.Version, d.UpdatedAt.UTC().Format("2006-01-02"), d.ID)
	}

	noun := "documents"
	if len(ordered) == 1 {
		noun = "document"
	}
	index := fmt.Sprintf("<h1>%s</h1>\n<p class=\"meta\">%d %s &middot; generated %s</p>\n<ul class=\"catalog\">\n%s</ul>",
		html.EscapeString(title), len(ordered), noun, opts.GeneratedAt.UTC().Format("2006-01-02"), catalog.String())
	comment := fmt.Sprintf("<!-- generator: %s; knowledge-site: %s; documents: %d; generated-at: %s -->",
		generator, opts.Slug, len(ordered), opts.GeneratedAt.UTC().Format(time.RFC3339))
	site.Files[path.Join(siteDir, "index.html")] = []byte(renderPage(title, comment, "", index))
	return site, nil
}

// SiteWritePaths lists every path WriteSite creates or replaces for slug,
// so a caller can put each through a write policy before writing.
func SiteWritePaths(workspace, slug string) []string {
	root := filepath.Join(workspace, ExportsDirName)
	return []string{filepath.Join(root, slug), filepath.Join(root, slug+".tar.gz")}
}

// WrittenSite reports where WriteSite put a site.
type WrittenSite struct {
	// Dir is the site directory.
	Dir string
	// Bundle is the deterministic tarball next to Dir.
	Bundle string
	// BundleSHA256 is the hex sha256 of Bundle.
	BundleSHA256 string
}

// WriteSite writes site under <workspace>/documents-exports/<slug>/ and
// packs <slug>.tar.gz beside it. A previous build of the same site is
// replaced; anything else already at the target path is left alone and
// ErrExportTargetBusy is returned. Symlinked export paths are refused so a
// build cannot be redirected outside the workspace.
func WriteSite(ctx context.Context, workspace string, site Site) (WrittenSite, error) {
	if workspace == "" {
		return WrittenSite{}, errors.New("docs: no workspace directory")
	}
	slug := site.Manifest.Name
	if err := sites.Validate(site.Manifest); err != nil {
		return WrittenSite{}, fmt.Errorf("%w: %v", ErrInvalidSiteSlug, err)
	}
	paths := SiteWritePaths(workspace, slug)
	target, bundle := paths[0], paths[1]
	root := filepath.Dir(target)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return WrittenSite{}, fmt.Errorf("docs: create exports directory: %w", err)
	}
	if err := refuseSymlink(root); err != nil {
		return WrittenSite{}, err
	}

	if info, err := os.Lstat(target); err == nil {
		if !info.IsDir() || !isSiteBuild(target, slug) {
			return WrittenSite{}, ErrExportTargetBusy
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return WrittenSite{}, fmt.Errorf("docs: inspect export target: %w", err)
	}

	tmp, err := os.MkdirTemp(root, "."+slug+".build-")
	if err != nil {
		return WrittenSite{}, fmt.Errorf("docs: create build directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }

	names := make([]string, 0, len(site.Files))
	for name := range site.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			cleanup()
			return WrittenSite{}, err
		}
		dst := filepath.Join(tmp, filepath.FromSlash(name))
		if !strings.HasPrefix(dst, tmp+string(filepath.Separator)) {
			cleanup()
			return WrittenSite{}, fmt.Errorf("docs: site file escapes build directory")
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			cleanup()
			return WrittenSite{}, fmt.Errorf("docs: create site directory: %w", err)
		}
		if err := os.WriteFile(dst, site.Files[name], 0o644); err != nil {
			cleanup()
			return WrittenSite{}, fmt.Errorf("docs: write site file: %w", err)
		}
	}

	// Swap the new build in. The window in which target is absent is two
	// renames long; a reader never sees a half-written site.
	var old string
	if _, err := os.Lstat(target); err == nil {
		old = tmp + ".old"
		if err := os.Rename(target, old); err != nil {
			cleanup()
			return WrittenSite{}, fmt.Errorf("docs: move previous build aside: %w", err)
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		if old != "" {
			_ = os.Rename(old, target)
		}
		cleanup()
		return WrittenSite{}, fmt.Errorf("docs: install site: %w", err)
	}
	if old != "" {
		_ = os.RemoveAll(old)
	}

	sum, err := packBundle(ctx, target, site.Manifest, bundle)
	if err != nil {
		return WrittenSite{}, err
	}
	return WrittenSite{Dir: target, Bundle: bundle, BundleSHA256: sum}, nil
}

func packBundle(ctx context.Context, dir string, m sites.Manifest, bundle string) (string, error) {
	if info, err := os.Lstat(bundle); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("docs: refusing symlinked bundle path")
	}
	f, err := os.CreateTemp(filepath.Dir(bundle), "."+filepath.Base(bundle)+".tmp-")
	if err != nil {
		return "", fmt.Errorf("docs: create bundle: %w", err)
	}
	tmpName := f.Name()
	res, err := sites.Pack(ctx, dir, m, f, sites.PackOptions{})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("docs: pack site: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("docs: pack site: %w", err)
	}
	if err := os.Rename(tmpName, bundle); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("docs: install bundle: %w", err)
	}
	return res.SHA256Hex, nil
}

func refuseSymlink(p string) error {
	info, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("docs: inspect exports directory: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("docs: exports directory must be a real directory")
	}
	return nil
}

// isSiteBuild reports whether dir holds a previous build of slug.
func isSiteBuild(dir, slug string) bool {
	m, err := sites.Parse(dir)
	return err == nil && m.Name == slug && m.Type == sites.KindStatic &&
		m.Static != nil && m.Static.Dir == siteDir
}
