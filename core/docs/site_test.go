package docs_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/docs"
	"github.com/kameas-ai/kenaz-harness/core/sites"
)

func TestExportMarkdown_SupportedConstructsAndWarnings(t *testing.T) {
	t.Parallel()
	body := `<h2>Heading</h2>
<p>Some <strong>bold</strong>, <em>em</em>, <del>gone</del>, <code>a*b</code> and <a href="https://example.com/p">a link</a>.</p>
<ul><li>one</li><li>two<ul><li>nested</li></ul></li></ul>
<ol start="3"><li>three</li><li>four</li></ol>
<blockquote><p>quoted</p></blockquote>
<pre><code class="language-mermaid">graph TD
  A--&gt;B</code></pre>
<table><thead><tr><th>k</th><th>v</th></tr></thead><tbody><tr><td>a|b</td><td colspan="2">1</td></tr></tbody></table>
<p>x<sup>2</sup> <img src="data:image/png;base64,iVBORw0KGgo=" alt="chart"></p>
<hr>`
	res, err := docs.ExportMarkdown(docs.Document{Body: body})
	if err != nil {
		t.Fatalf("ExportMarkdown: %v", err)
	}
	for _, want := range []string{
		"## Heading",
		"**bold**", "*em*", "~~gone~~", "`a*b`", "[a link](<https://example.com/p>)",
		"- one\n- two\n  - nested",
		"3. three\n4. four",
		"> quoted",
		"```mermaid\ngraph TD\n  A-->B\n```",
		"| k | v |\n| --- | --- |\n| a\\|b | 1 |",
		"*[image: chart]*",
		"---",
	} {
		if !strings.Contains(res.Markdown, want) {
			t.Errorf("markdown missing %q:\n%s", want, res.Markdown)
		}
	}
	if strings.Contains(res.Markdown, "base64") {
		t.Error("data URI leaked into markdown")
	}
	joined := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"colspan/rowspan", "<sup> formatting dropped", "embedded image omitted"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q: %v", want, res.Warnings)
		}
	}

	again, _ := docs.ExportMarkdown(docs.Document{Body: body})
	if again.Markdown != res.Markdown || strings.Join(again.Warnings, "|") != strings.Join(res.Warnings, "|") {
		t.Error("markdown export is not deterministic")
	}
}

func siteDocs() []docs.Document {
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	return []docs.Document{
		{ID: "01K0000000000000000000000B", Title: "Zeta <runbook>", Version: 2, UpdatedAt: at,
			Body: `<h1>Zeta</h1><p>Deploy.</p>`, ContentSHA256: "abc"},
		{ID: "01K0000000000000000000000A", Title: "Alpha", Version: 0, UpdatedAt: at,
			Body: hostileBody, ContentSHA256: "def"},
	}
}

var (
	srcAttr  = regexp.MustCompile(`\ssrc="([^"]*)"`)
	hrefAttr = regexp.MustCompile(`\shref="([^"]*)"`)
)

func TestBuildSite_ContentsAreInertAndSelfContained(t *testing.T) {
	t.Parallel()
	gen := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	site, err := docs.BuildSite(docs.SiteOptions{Slug: "eng-handbook", Title: "Engineering handbook", GeneratedAt: gen}, siteDocs())
	if err != nil {
		t.Fatalf("BuildSite: %v", err)
	}

	var names []string
	for n := range site.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	want := []string{
		"kameas-site.json",
		"public/d/01K0000000000000000000000A.html", "public/d/01K0000000000000000000000A.md",
		"public/d/01K0000000000000000000000B.html", "public/d/01K0000000000000000000000B.md",
		"public/index.html",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v", names)
	}

	m, err := sites.ParseBytes(site.Files["kameas-site.json"])
	if err != nil || m.Name != "eng-handbook" || m.Type != sites.KindStatic || m.Static == nil || m.Static.Dir != "public" {
		t.Fatalf("manifest = %+v, %v", m, err)
	}

	// Generated navigation is the only place relative links may appear;
	// document bodies may only carry absolute https/mailto links and data:
	// images.
	allowedRelative := map[string]bool{"../index.html": true}
	for _, id := range []string{"01K0000000000000000000000A", "01K0000000000000000000000B"} {
		allowedRelative["d/"+id+".html"] = true
		allowedRelative["d/"+id+".md"] = true
	}
	for name, content := range site.Files {
		if !strings.HasSuffix(name, ".html") {
			continue
		}
		page := string(content)
		if !strings.Contains(page, `http-equiv="Content-Security-Policy" content="default-src 'none'`) {
			t.Errorf("%s: missing CSP meta", name)
		}
		for _, bad := range []string{"<script", "onclick", "javascript:", "<iframe", "<link", "tracker.example"} {
			if strings.Contains(strings.ToLower(page), bad) {
				t.Errorf("%s retains %q", name, bad)
			}
		}
		for _, sm := range srcAttr.FindAllStringSubmatch(page, -1) {
			if !strings.HasPrefix(sm[1], "data:image/") {
				t.Errorf("%s: non-data src %q", name, sm[1])
			}
		}
		for _, hm := range hrefAttr.FindAllStringSubmatch(page, -1) {
			h := hm[1]
			if !strings.HasPrefix(h, "https://") && !strings.HasPrefix(h, "mailto:") && !allowedRelative[h] {
				t.Errorf("%s: disallowed href %q", name, h)
			}
		}
	}

	index := string(site.Files["public/index.html"])
	if !strings.Contains(index, "Zeta &lt;runbook&gt;") || strings.Contains(index, "Zeta <runbook>") {
		t.Error("catalog title not escaped")
	}
	if strings.Index(index, "Alpha") > strings.Index(index, "Zeta") {
		t.Error("catalog not ordered by title")
	}
	page := string(site.Files["public/d/01K0000000000000000000000B.html"])
	if !strings.Contains(page, "document-id: 01K0000000000000000000000B; version: 2") {
		t.Error("page manifest comment missing")
	}
	if strings.Contains(strings.SplitN(page, "\n", 3)[1], "Zeta") {
		t.Error("manifest comment must not carry the title")
	}
}

func TestBuildSite_RejectsBadInput(t *testing.T) {
	t.Parallel()
	gen := time.Now()
	if _, err := docs.BuildSite(docs.SiteOptions{Slug: "Bad--Slug", GeneratedAt: gen}, siteDocs()); !errors.Is(err, docs.ErrInvalidSiteSlug) {
		t.Errorf("bad slug err = %v", err)
	}
	if _, err := docs.BuildSite(docs.SiteOptions{Slug: "ok", GeneratedAt: gen}, nil); !errors.Is(err, docs.ErrNoSiteDocuments) {
		t.Errorf("empty err = %v", err)
	}
	bad := siteDocs()
	bad[0].ID = "../../etc/passwd"
	if _, err := docs.BuildSite(docs.SiteOptions{Slug: "ok", GeneratedAt: gen}, bad); err == nil {
		t.Error("path-shaped document id accepted")
	}
	dup := siteDocs()
	dup[1].ID = dup[0].ID
	if _, err := docs.BuildSite(docs.SiteOptions{Slug: "ok", GeneratedAt: gen}, dup); err == nil {
		t.Error("duplicate document id accepted")
	}
	if code := docs.SiteErrorCode(docs.ErrExportTargetBusy); code != docs.CodeExportTargetBusy {
		t.Errorf("code = %q", code)
	}
}

func TestWriteSite_DeterministicBundleReplaceAndRefusal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	gen := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	build := func() docs.Site {
		s, err := docs.BuildSite(docs.SiteOptions{Slug: "kb", Title: "KB", GeneratedAt: gen}, siteDocs())
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	ws := t.TempDir()
	w1, err := docs.WriteSite(ctx, ws, build())
	if err != nil {
		t.Fatalf("WriteSite: %v", err)
	}
	if w1.Dir != filepath.Join(ws, docs.ExportsDirName, "kb") || w1.Bundle != filepath.Join(ws, docs.ExportsDirName, "kb.tar.gz") {
		t.Fatalf("paths = %+v", w1)
	}
	if _, err := os.Stat(filepath.Join(w1.Dir, "public", "index.html")); err != nil {
		t.Fatalf("index missing: %v", err)
	}

	// Rebuilding identical input replaces the previous build and yields the
	// same bundle bytes — the property a future "upload these exact bytes"
	// review depends on.
	w2, err := docs.WriteSite(ctx, ws, build())
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if w1.BundleSHA256 != w2.BundleSHA256 || w1.BundleSHA256 == "" {
		t.Errorf("bundle not deterministic: %s vs %s", w1.BundleSHA256, w2.BundleSHA256)
	}
	leftovers, _ := filepath.Glob(filepath.Join(ws, docs.ExportsDirName, ".*"))
	if len(leftovers) != 0 {
		t.Errorf("temporary build files left behind: %v", leftovers)
	}

	// The tarball is the Fleet Sites bundle shape: manifest at the root,
	// pages under static.dir.
	f, err := os.Open(w2.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var entries []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, h.Name)
	}
	joined := strings.Join(entries, ",")
	for _, want := range []string{"kameas-site.json", "public/index.html", "public/d/01K0000000000000000000000A.html"} {
		if !strings.Contains(joined, want) {
			t.Errorf("bundle missing %s: %v", want, entries)
		}
	}

	// A directory that is not a build of this site is never replaced.
	other := t.TempDir()
	occupied := filepath.Join(other, docs.ExportsDirName, "kb")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "notes.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.WriteSite(ctx, other, build()); !errors.Is(err, docs.ErrExportTargetBusy) {
		t.Errorf("occupied target err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(occupied, "notes.txt")); string(b) != "mine" {
		t.Error("user file was touched")
	}

	// A symlinked exports directory cannot redirect the write.
	outside := t.TempDir()
	linked := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(linked, docs.ExportsDirName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := docs.WriteSite(ctx, linked, build()); err == nil {
		t.Error("symlinked exports directory accepted")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("wrote through symlink: %v", entries)
	}
}
