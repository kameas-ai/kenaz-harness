// Package docs owns the Kenaz document body: the HTML sanitizer that every
// write path must pass through, the bounds that body must respect, and (in
// later tasks) the Markdown and self-contained-HTML exporters.
//
// # Which layer is authoritative
//
// Two sanitizers exist in the product. They are not redundant and they are
// not interchangeable:
//
//   - This package (Go, server-side) is the **store invariant**. Nothing
//     reaches a doc-kind unit body without passing Sanitize — agent tool
//     calls, editor saves, pastes, future imports. If a byte is in the
//     store, this package put it there. Every read path, every exporter,
//     and every future publish path may assume its input is already in
//     this package's canonical form, which is what makes the sanitizer /
//     editor round-trip a non-problem rather than a managed risk.
//   - dompurify in the harness Vue app is **defense in depth at render
//     time**. It protects against a body that reached the client by some
//     path this package did not gate (a bug, a migration, a hand-edited
//     database) and against the renderer's own DOM assembly. It is never
//     the invariant, must never be relied on to make a stored body safe,
//     and its rule set does not need to match this one.
//
// Neither layer is the whole story: rendering surfaces additionally frame
// document HTML in a sandboxed iframe without allow-same-origin or
// allow-scripts (FR-006), and exporters strip scripts unconditionally
// regardless of any in-app toggle. Sanitization is one of three
// independent defenses, not the only one.
//
// # Threat model
//
// The author is assumed hostile, not careless. Document bodies are
// model-generated (a prompt-injected model is an attacker with a keyboard),
// pasted from arbitrary web pages, and — once §XIII publication lands —
// served to third parties who never chose to trust the author. So the
// sanitizer's job is not "tidy up sloppy markup"; it is to make a
// deliberately malicious body inert.
//
// # The rule set
//
// Sanitize enforces, in one pass over the token stream:
//
//   - No active content. <script> and <style> are dropped along with their
//     contents; so are <iframe>, <object>, <embed>, <applet>, <template>,
//     <svg>, <math>, <xmp>, <plaintext>, <noscript>, <noembed>, <noframes>
//     and <title>. No on* handler survives, because no attribute survives
//     that is not on the allowlist below.
//   - No external references. Only three URL forms are permitted anywhere:
//     an absolute https: URL, a mailto: URL, and a data: URI carrying a
//     raster image. Relative URLs are refused outright, which is stricter
//     than it may look — see allowRelativeURLs below.
//   - No CSS fetches or CSS-based exfiltration. <style> elements are gone
//     and the style attribute is filtered to a fixed list of properties
//     whose value grammars cannot express url(), @import, expression(), or
//     a vendor binding. There is no property on that list through which a
//     document can cause a network request.
//   - No comments, no doctype. Both are dropped, which removes the
//     comment-boundary confusion that several mutation-XSS payloads rely on.
//   - Structure and prose are preserved: headings, paragraphs, lists,
//     tables, code blocks, blockquotes, inline emphasis, https links, and
//     inline raster images. Mermaid and KaTeX survive as *source* inside
//     <pre><code class="language-mermaid"> — the renderer processes them at
//     display time; the store never holds an executable form.
//
// Sanitize is idempotent by construction and by test: Sanitize(Sanitize(x))
// equals Sanitize(x) for every input (SC-002). Idempotence is the property
// that matters most here, because a sanitizer whose output a second pass
// would treat differently is a sanitizer whose output a *browser* may treat
// differently than the sanitizer did.
//
// # What is deliberately lost
//
// Two omissions are worth knowing about, both recorded as follow-ups rather
// than oversights:
//
//   - Fragment links (href="#section") and id/name attributes are not
//     allowed, so a document cannot carry its own table of contents in P1.
//     Permitting fragments means permitting the empty URL scheme, and
//     bluemonday's scheme allowlist cannot distinguish "#section" from
//     "/etc/passwd" or "logo.png" — both arrive as relative URLs. A
//     relative reference is harmless inside an opaque-origin srcdoc frame
//     but resolves against file:// in an exported .html opened from disk,
//     which would break the exporter's zero-external-references guarantee.
//     Anchors return when there is a fragment-only mechanism to allow them
//     with.
//   - Presentational CSS beyond the property allowlist is dropped rather
//     than rewritten. Model output tends to be style-heavy, so expect
//     visible flattening; the renderer's own prose stylesheet, not the
//     document, is where documents get their look.
//
// # On the dependency
//
// The sanitizer is github.com/microcosm-cc/bluemonday behind this package's
// own API. Rationale, the hand-rolled alternative, and the charter carve-out
// are in docs/adr/0005-bluemonday-html-sanitizer-carveout.md and in
// .specify/decisions/ADR-document-substrate.md (R-1). bluemonday's types do
// not appear in any exported signature here, so the seam stays replaceable.
package docs

import (
	"encoding/base64"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
)

// Sanitize returns the canonical storable form of an HTML document body.
//
// It returns an error only when the body violates a bound in limits.go; a
// body that clears CheckLimits always sanitizes. Disallowed markup is
// silently removed rather than rejected, because "the model emitted a
// <script> tag" is an expected event on this path, not a user-facing
// failure.
//
// Every write path into a doc-kind unit body MUST call this. There is no
// bypass and no "trusted" variant.
func Sanitize(body string) (string, error) {
	if err := CheckLimits(body); err != nil {
		return "", err
	}
	out := policy().Sanitize(body)
	// Escaping can grow a body past the cap even when the input fit; see
	// MaxBodyBytes. Enforcing on the output is what makes "every stored
	// body is <= MaxBodyBytes" true, and it cannot break idempotence: a
	// successful first pass returns something already under the cap, and
	// the second pass returns it unchanged.
	if len(out) > MaxBodyBytes {
		return "", ErrBodyTooLarge
	}
	return out, nil
}

// SanitizeBytes is Sanitize over a byte slice. The returned slice does not
// alias the input.
func SanitizeBytes(body []byte) ([]byte, error) {
	out, err := Sanitize(string(body))
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// policy returns the process-wide document policy. bluemonday policies are
// safe for concurrent use once built but are not safe to build
// concurrently, so it is built once.
var policy = sync.OnceValue(newPolicy)

// Element allowlists. Anything absent is stripped; whether its *contents*
// survive is a separate decision, made by the skip list in newPolicy.
var (
	// blockElements carry document structure.
	blockElements = []string{
		"p", "br", "hr", "div", "span",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"ul", "ol", "li", "dl", "dt", "dd",
		"blockquote", "pre", "figure", "figcaption",
		"section", "article", "header", "footer", "main", "aside", "nav",
		"details", "summary",
	}

	// inlineElements carry emphasis and inline semantics. All are inert
	// text-level markup; none can load a resource.
	inlineElements = []string{
		"strong", "b", "em", "i", "u", "s", "strike", "del", "ins",
		"mark", "small", "sub", "sup", "code", "kbd", "samp", "var",
		"abbr", "cite", "q", "dfn", "time", "wbr", "ruby", "rt", "rp",
	}

	// tableElements are the whole table vocabulary — documents in this
	// product are frequently mostly table.
	tableElements = []string{
		"table", "thead", "tbody", "tfoot", "tr", "th", "td",
		"caption", "colgroup", "col",
	}
)

// Attribute value grammars. Go's regexp is RE2, so every pattern here is
// linear-time on any input — no backtracking blowup is reachable.
var (
	// safeAttrText is deliberately permissive for human-readable attribute
	// text (alt, title, datetime). It is safe to be permissive because
	// bluemonday escapes attribute values on output, so "<" in a value
	// cannot close the tag; the pattern excludes angle brackets anyway as
	// belt-and-braces against a future renderer that assembles markup by
	// concatenation.
	// (1000 is RE2's maximum repeat count, and is a generous bound for a
	// tooltip or an alt text.)
	safeAttrText = regexp.MustCompile(`^[^<>]{0,1000}$`)

	// smallCount bounds colspan / rowspan / start.
	smallCount = regexp.MustCompile(`^[0-9]{1,4}$`)

	// cellScope is the th/@scope enumeration.
	cellScope = regexp.MustCompile(`(?i)^(row|col|rowgroup|colgroup)$`)
)

// newPolicy builds the document policy. Read it as the normative statement
// of the rule set; the package doc is the prose summary.
func newPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// ── URLs ──────────────────────────────────────────────────────────
	//
	// RequireParseableURLs makes bluemonday run every URL-bearing
	// attribute through net/url and the scheme allowlist below. Without
	// it the scheme allowlist is not consulted at all.
	p.RequireParseableURLs(true)
	// Relative URLs are refused. This is what makes "no output retains an
	// external reference" a provable statement rather than a hopeful one:
	// there is no attribute value left that a browser could resolve
	// against a base it picked itself. It also costs us fragment links —
	// see the package doc.
	p.AllowRelativeURLs(false)
	// https for real links; mailto because a document that names a contact
	// should link them, and a mailto URL cannot load a resource, cannot
	// navigate inside a sandboxed frame, and is already an allowlisted
	// scheme in the host's OpenExternalURL.
	//
	// Notably absent: http (downgrade + mixed content), and every scheme
	// that executes or reads locally — javascript, vbscript, file, blob,
	// filesystem, about, chrome, ms-appx, and friends. The allowlist is
	// closed, so new schemes do not need to be enumerated to be refused.
	p.AllowURLSchemeWithCustomPolicy("https", httpsURL)
	p.AllowURLSchemeWithCustomPolicy("mailto", mailtoURL)
	// data: is admitted only for raster images. See dataImageURL.
	p.AllowURLSchemeWithCustomPolicy("data", dataImageURL)
	// https is for *links*, never for auto-loading resources. The scheme
	// allowlist above is global — bluemonday's URL policies see a URL, not
	// the element it came from — so an https <img src> would otherwise
	// sail through and become a tracking beacon that reports every reader's
	// IP and read time back to the document's author. RewriteSrc is the
	// element-aware hook: it fires on src attributes only, so it is where
	// "as links only" gets enforced.
	p.RewriteSrc(rewriteToInertImage)

	// Link hygiene. rel="nofollow noreferrer" costs nothing while
	// documents are local and matters the moment a published document
	// (§XIII) is read by a third party, since a bare outbound link would
	// otherwise leak the publication URL in a Referer header.
	p.RequireNoFollowOnLinks(true)
	p.RequireNoReferrerOnLinks(true)

	// ── Elements ──────────────────────────────────────────────────────
	p.AllowElements(blockElements...)
	p.AllowElements(inlineElements...)
	p.AllowElements(tableElements...)
	// bluemonday drops an allowed element that ends up with no attributes
	// unless it is on the no-attrs list. Its default list covers almost
	// everything above; "main" is the omission.
	p.AllowNoAttrs().OnElements("main")

	// Elements whose *contents* die with them. bluemonday's defaults
	// already cover script, style, iframe, object, noscript, noembed,
	// noframes, nostyle, title, frame and frameset. The additions are the
	// containers where the browser's parsing rules differ from a
	// tokenizer's, which is the soil mutation-XSS grows in:
	//
	//   svg, math   — foreign content. Namespace switching changes how the
	//                 same bytes tokenize; dropping the subtree removes
	//                 the question rather than answering it.
	//   template    — inert in a real parser (its children go to a
	//                 separate fragment), so a browser would never render
	//                 them; dropping matches that.
	//   xmp,
	//   plaintext   — raw-text elements a naive sanitizer re-emits as live
	//                 markup.
	//   applet      — legacy active content.
	//
	// Nothing void may go on this list: the flag is set on a start tag and
	// cleared on the matching end tag, so a void element would swallow the
	// rest of the document.
	p.SkipElementsContent("svg", "math", "template", "xmp", "plaintext", "applet")
	// <frame> is void, and bluemonday ships it on the skip list, so a
	// single <frame> anywhere silently truncates everything after it.
	// Removing it from the list loses nothing — the tag itself is still
	// not on the element allowlist, so it is still stripped — and turns a
	// content-destroying input into an ignored one.
	p.AllowElementsContent("frame")

	// ── Global attributes ─────────────────────────────────────────────
	//
	// class carries the renderer's contract: "language-mermaid" on a code
	// block is how Mermaid and KaTeX source is marked. It is inert without
	// scripts, and the stylesheet a class could select is the renderer's
	// own, not the document's.
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).Globally()
	p.AllowAttrs("lang").Matching(regexp.MustCompile(`^[a-zA-Z]{1,8}(-[a-zA-Z0-9]{1,8})*$`)).Globally()
	p.AllowAttrs("dir").Matching(bluemonday.Direction).Globally()
	p.AllowAttrs("title").Matching(safeAttrText).Globally()
	//
	// Not allowed globally, on purpose: id and name. Without fragment
	// links there is nothing to point at them, and an attacker-chosen id
	// is the input to DOM clobbering (<img id="body">). If anchors return,
	// they return with an id grammar attached.
	//
	// Also not allowed: data-*. Inert, but unbounded surface for no P1
	// gain.

	// ── Links and images ──────────────────────────────────────────────
	p.AllowAttrs("href").OnElements("a")
	p.AllowAttrs("src").OnElements("img")
	p.AllowAttrs("alt").Matching(safeAttrText).OnElements("img")
	p.AllowAttrs("width", "height").Matching(bluemonday.NumberOrPercent).OnElements("img")
	// srcset, sizes, loading, referrerpolicy and crossorigin are absent by
	// omission. srcset especially: it has its own comma-and-descriptor
	// grammar that bluemonday's URL policy never inspects, which has been
	// a bypass in other sanitizers.

	// ── Tables ────────────────────────────────────────────────────────
	p.AllowAttrs("colspan", "rowspan").Matching(smallCount).OnElements("td", "th")
	p.AllowAttrs("scope").Matching(cellScope).OnElements("th")
	p.AllowAttrs("span").Matching(smallCount).OnElements("col", "colgroup")

	// ── Lists, quotes, edits ──────────────────────────────────────────
	p.AllowAttrs("start").Matching(smallCount).OnElements("ol")
	p.AllowAttrs("type").Matching(bluemonday.ListType).OnElements("ol", "ul")
	// cite goes through the same URL policy as href (bluemonday treats
	// blockquote/q/del/ins @cite as URL-bearing).
	p.AllowAttrs("cite").OnElements("blockquote", "q", "del", "ins")
	p.AllowAttrs("datetime").Matching(safeAttrText).OnElements("time", "del", "ins")

	allowStyles(p)

	return p
}

// dataImageURL decides whether a data: URI may stay.
//
// This is deliberately *not* bluemonday's AllowDataURIImages helper. That
// helper's accepted-mime pattern includes image/svg+xml — its doc comment
// says otherwise, so this is a live discrepancy in the library, not a
// deprecated behavior. SVG is a scripting host: harmless in an <img>
// (browsers load images in a non-scripted, no-external-fetch mode) but a
// full document when reached any other way, and bluemonday's scheme
// policies are global, so admitting the scheme for <img src> admits it for
// <a href> too. Raster only, then.
func dataImageURL(u *url.URL) bool {
	// A query or fragment on a data: URI means the payload was not what it
	// appeared to be.
	if u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	rest, ok := trimDataImagePrefix(u.Opaque)
	if !ok {
		return false
	}
	// Must decode. A data: URI that is not valid base64 is a parser
	// disagreement waiting to happen.
	_, err := base64.StdEncoding.DecodeString(rest)
	return err == nil
}

// httpsURL accepts only a hierarchical https URL with a real host.
//
// The extra conditions are not decoration. net/url happily parses
// "https:javascript:alert(1)" into scheme "https" with opaque
// "javascript:alert(1)", and while browsers do not execute that, letting it
// through leaves a href that reads like a scheme bypass in every future
// security review. Requiring Host non-empty and Opaque empty means an
// accepted href is always of the shape https://host/path. Embedded
// credentials are refused because a document is not a place to carry them.
func httpsURL(u *url.URL) bool {
	return u.Opaque == "" && u.Host != "" && u.User == nil
}

// mailtoURL accepts mailto:addressee, and nothing with an authority
// component.
func mailtoURL(u *url.URL) bool {
	return u.Opaque != "" && u.Host == "" && u.User == nil
}

// StrippedImageSrc is what an image's src becomes when the document tried
// to load it over the network.
//
// It is a well-formed data: URI — so it survives a second sanitize pass
// unchanged, which is what keeps Sanitize idempotent — whose payload is
// deliberately *not* a decodable image (one 0x00 byte). Browsers therefore
// fail the load and fall back to the img's alt text, which the policy
// preserves. That is the honest outcome: the reader sees that an image was
// meant to be here and what it was of, no request leaves the machine, and
// nothing is silently blank.
//
// Exporters should treat this value as a sentinel and emit an explicit
// warning for it (SC-003: never silent loss).
const StrippedImageSrc = "data:image/png;base64,AA=="

// rewriteToInertImage is the src rewriter installed by newPolicy. By the
// time it runs, bluemonday has already validated the URL against the scheme
// allowlist, so the value is https, mailto, or a raster data: URI. Only the
// last of those may stay.
func rewriteToInertImage(u *url.URL) {
	if u.Scheme == "data" && dataImageURL(u) {
		return
	}
	*u = url.URL{Scheme: "data", Opaque: strings.TrimPrefix(StrippedImageSrc, "data:")}
}

// dataImageMimes is the closed set of raster image types permitted in a
// data: URI. Deliberately excludes image/svg+xml.
var dataImageMimes = []string{
	"image/gif;base64,",
	"image/jpeg;base64,",
	"image/png;base64,",
	"image/webp;base64,",
}

func trimDataImagePrefix(opaque string) (string, bool) {
	lower := strings.ToLower(opaque)
	for _, m := range dataImageMimes {
		if strings.HasPrefix(lower, m) {
			return opaque[len(m):], true
		}
	}
	return "", false
}

// CSS value grammars. Each one is checked against the lowercased
// declaration value. None of them can match a url(...), an @import, an
// expression(...), or a vendor binding — that is the property being
// designed for, and the exfiltration tests assert it.
var (
	cssColor = regexp.MustCompile(`^(#[0-9a-f]{3,8}` +
		`|rgb\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*\)` +
		`|rgba\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*(0|1|0?\.\d{1,3})\s*\)` +
		`|transparent|currentcolor|[a-z]{3,24})$`)

	cssLength = regexp.MustCompile(`^(auto|0|\d{1,5}(\.\d{1,3})?(px|em|rem|%|ch|ex|pt))$`)

	// cssLengthList covers the 1-to-4-value margin/padding shorthands.
	cssLengthList = regexp.MustCompile(`^(auto|0|\d{1,5}(\.\d{1,3})?(px|em|rem|%|ch|ex|pt))` +
		`(\s+(auto|0|\d{1,5}(\.\d{1,3})?(px|em|rem|%|ch|ex|pt))){0,3}$`)

	cssBorder = regexp.MustCompile(`^(0|none|(\d{1,3}(px|em|rem|pt))` +
		`(\s+(none|hidden|solid|dashed|dotted|double|groove|ridge))?` +
		`(\s+(#[0-9a-f]{3,8}|transparent|currentcolor|[a-z]{3,24}))?)$`)

	cssLineHeight = regexp.MustCompile(`^(normal|\d{1,3}(\.\d{1,3})?(px|em|rem|%|pt)?)$`)

	// cssFontStack allows font names, quotes and commas — inert, since a
	// font-family value cannot reference a URL (that is @font-face's src,
	// and there are no stylesheets in a document).
	cssFontStack = regexp.MustCompile(`^[a-z0-9 ,'"._-]{1,200}$`)
)

// allowStyles installs the style-attribute allowlist.
//
// The posture is: a document carries structure, the renderer supplies
// looks, and the narrow slice of styling in between is the part that
// actually changes meaning — a right-aligned number column, a highlighted
// cell, a fixed-width table. Everything else goes.
//
// Two mechanics worth knowing. bluemonday only filters the style attribute
// when at least one style policy exists (otherwise it falls through to the
// attribute allowlist, where "style" is absent, and is dropped) — so this
// function is what makes style attributes possible at all. And a
// declaration whose value fails its grammar is dropped individually; if
// every declaration fails, the attribute itself is not emitted. Nothing
// here can produce a half-filtered value.
func allowStyles(p *bluemonday.Policy) {
	enum := func(prop string, vals ...string) {
		p.AllowStyles(prop).MatchingEnum(vals...).Globally()
	}
	match := func(re *regexp.Regexp, props ...string) {
		p.AllowStyles(props...).Matching(re).Globally()
	}

	enum("text-align", "left", "right", "center", "justify", "start", "end")
	enum("vertical-align", "top", "middle", "bottom", "baseline", "sub", "super")
	enum("font-style", "normal", "italic", "oblique")
	enum("font-weight", "normal", "bold", "bolder", "lighter",
		"100", "200", "300", "400", "500", "600", "700", "800", "900")
	enum("text-decoration", "none", "underline", "line-through", "overline")
	enum("text-transform", "none", "uppercase", "lowercase", "capitalize")
	enum("white-space", "normal", "nowrap", "pre", "pre-wrap", "pre-line")
	enum("border-collapse", "collapse", "separate")
	enum("overflow-x", "auto", "scroll", "hidden", "visible")
	enum("list-style-type", "none", "disc", "circle", "square", "decimal",
		"lower-alpha", "upper-alpha", "lower-roman", "upper-roman")

	match(cssColor, "color", "background-color")
	match(cssLength, "width", "height", "max-width", "min-width",
		"text-indent", "border-radius", "border-spacing")
	match(cssLengthList, "margin", "padding",
		"margin-top", "margin-right", "margin-bottom", "margin-left",
		"padding-top", "padding-right", "padding-bottom", "padding-left")
	match(cssBorder, "border", "border-top", "border-right",
		"border-bottom", "border-left")
	match(cssLineHeight, "line-height")
	match(cssFontStack, "font-family")

	// Absent on purpose, each because it either fetches or positions:
	// background and background-image (url()), list-style and
	// list-style-image (url()), border-image (url()), cursor (url()),
	// content (url() and attr()), filter, mask, clip-path, behavior,
	// -moz-binding, position, transform, animation, transition, and
	// anything else not named above. The allowlist is closed, so this
	// comment is documentation rather than enforcement.
}
