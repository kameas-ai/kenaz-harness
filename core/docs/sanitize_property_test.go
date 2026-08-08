package docs

import (
	"math/rand"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// This file holds the properties Sanitize must satisfy for *every* input,
// checked over a generated corpus rather than a hand-picked table.
//
// The headline property is idempotence — Sanitize(Sanitize(x)) ==
// Sanitize(x), SC-002. It is the one worth generating inputs for, because
// the interesting failures are not "a payload got through" (a table catches
// those) but "the sanitizer produced markup that a second parse reads
// differently than the first". That is the same disagreement a browser can
// have with the sanitizer, which is what mutation XSS is. A sanitizer whose
// output is a fixed point of itself has no room for that disagreement.
//
// Three further properties ride along on the same corpus, since generating
// it is the expensive part:
//
//   - Closure: the output contains only allowlisted elements and
//     attributes. This subsumes "no on* handler survives" without
//     special-casing the string "on".
//   - Inertness: no comments, no doctype, no active-content element.
//   - No external references: every URL-bearing attribute value is an
//     https link, a mailto link, or an inline raster image.

// ── the corpus ────────────────────────────────────────────────────────────

// corpusFragments are recombined into documents. The bank mixes benign
// prose, every hostile construct from the adversarial table, and — most
// usefully for idempotence — malformed, unbalanced, and half-escaped markup,
// which is where a re-parse is most likely to land somewhere new.
var corpusFragments = []string{
	// Benign structure.
	`<h1>Deployment runbook</h1>`,
	`<h2>Prerequisites</h2>`,
	`<p>Roll the canary before the fleet.</p>`,
	`<ul><li>one</li><li>two</li></ul>`,
	`<ol start="2"><li>step</li></ol>`,
	`<blockquote>Quoted guidance.</blockquote>`,
	`<pre><code class="language-bash">kubectl get pods</code></pre>`,
	`<pre><code class="language-mermaid">graph TD; A-->B;</code></pre>`,
	`<table><thead><tr><th scope="col">Region</th><th>Rev</th></tr></thead><tbody><tr><td colspan="2">EMEA</td></tr></tbody></table>`,
	`<p>Inline <strong>bold</strong>, <em>italic</em>, <code>code</code>, <sub>sub</sub>, <sup>sup</sup>.</p>`,
	`<details><summary>More</summary><p>Detail.</p></details>`,
	`<figure><img src="data:image/png;base64,iVBORw0KGgo=" alt="chart"><figcaption>Fig 1</figcaption></figure>`,
	`<hr>`,
	`<main><section><article><p>nested</p></article></section></main>`,
	`<time datetime="2026-08-08">today</time>`,
	`<dl><dt>term</dt><dd>definition</dd></dl>`,

	// Text that stresses escaping.
	`a < b && c > d "q" 'r'`,
	`&lt;script&gt;alert(1)&lt;/script&gt;`,
	`&amp;lt;&amp;amp;&#39;&#34;`,
	`&notanentity; &#x41; &#65; &nbsp;`,
	"emoji 🜂 combining é and RTL ‮override‬",
	`$$\frac{a}{b}$$ and \(x^2\)`,
	`]]> --> --!> <!-- `,

	// URLs, allowed and not.
	`<a href="https://docs.example.com/a?b=1#c">link</a>`,
	`<a href="mailto:ops@example.com?subject=hi">mail</a>`,
	`<a href="http://insecure.example.com/">http</a>`,
	`<a href="//protocol-relative.example.com/">rel</a>`,
	`<a href="/absolute/path">path</a>`,
	`<a href="relative.html">rel2</a>`,
	`<a href="#fragment">frag</a>`,
	`<a href="javascript:alert(1)">js</a>`,
	`<a href="JaVaScRiPt:alert(1)">js2</a>`,
	`<a href="jav&#x09;ascript:alert(1)">js3</a>`,
	`<a href="https:javascript:alert(1)">js4</a>`,
	`<a href="vbscript:msgbox(1)">vbs</a>`,
	`<a href="data:text/html;base64,PHNjcmlwdD4=">dataurl</a>`,
	`<a href="file:///etc/passwd">file</a>`,
	`<a href="https://user:pass@x.example.com/">creds</a>`,
	`<a href="https://x.example.com/&quot;onmouseover=alert(1)">quote</a>`,

	// Images.
	`<img src="data:image/png;base64,iVBORw0KGgo=" alt="ok" width="120" height="40">`,
	`<img src="data:image/gif;base64,R0lGODdh" alt="gif">`,
	`<img src="data:image/webp;base64,UklGRg==" alt="webp">`,
	`<img src="data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=" alt="svg">`,
	`<img src="data:image/png;base64,!!!notbase64!!!" alt="bad">`,
	`<img src="https://tracker.example.com/pixel.png" alt="beacon">`,
	`<img srcset="javascript:alert(1) 1x, https://tracker.example.com/x.png 2x" src="data:image/png;base64,iVBORw0KGgo=">`,
	`<img src="` + StrippedImageSrc + `" alt="already stripped">`,

	// Handlers and disallowed attributes.
	`<p onclick="alert(1)" onmouseover="x()" ONERROR="y()">handlers</p>`,
	`<p id="body" name="cookie" data-x="1" tabindex="3" contenteditable="true">attrs</p>`,
	`<a href="https://x.example.com/" target="_blank" rel="opener" ping="https://tracker.example.com/">target</a>`,

	// CSS.
	`<p style="color:red;background-color:#ffdddd;text-align:center">styled</p>`,
	`<p style="background-image:url(https://tracker.example.com/x)">bgimg</p>`,
	`<p style="background:red url(https://tracker.example.com/y) no-repeat">bgshort</p>`,
	`<p style="color:expression(alert(1));-moz-binding:url(x);behavior:url(y)">legacy</p>`,
	`<p style="position:absolute;top:0;left:0;z-index:9999">position</p>`,
	`<p style="font-family:'Helvetica Neue', Arial, sans-serif">font</p>`,
	`<p style="margin:0 auto;padding:4px 8px 4px 8px;border:1px solid #ccc">box</p>`,
	`<p style="width:calc(100% - 10px)">calc</p>`,
	`<p style="">empty</p>`,
	`<p style="color:red;;;">trailing</p>`,
	`<p style="COLOR:RED;Text-Align:CENTER">upper</p>`,
	`<p style="-webkit-text-align:center">vendor</p>`,
	`<style>@import url("https://tracker.example.com/x.css");p{color:red}</style>`,

	// Active content and plugins.
	`<script>alert(1)</script>`,
	`<script src="https://tracker.example.com/x.js"></script>`,
	`<script>`,
	`</script>`,
	`<iframe src="https://tracker.example.com/" sandbox=""></iframe>`,
	`<iframe srcdoc="<script>alert(1)</script>"></iframe>`,
	`<object data="x.swf"><param name="p" value="v"></object>`,
	`<embed src="x.swf">`,
	`<applet code="Z"><p>inner</p></applet>`,
	`<form action="https://tracker.example.com/"><input name="s"><button formaction="javascript:alert(1)">go</button></form>`,
	`<base href="https://tracker.example.com/">`,
	`<meta http-equiv="refresh" content="0;url=https://tracker.example.com/">`,
	`<link rel="stylesheet" href="https://tracker.example.com/x.css">`,
	`<!DOCTYPE html>`,
	`<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01//EN" "<script>alert(1)</script>">`,

	// Foreign content and parser-quirk containers — the mXSS soil.
	`<svg><script>alert(1)</script><circle r="1"/></svg>`,
	`<svg><foreignObject><p onclick="alert(1)">hi</p></foreignObject></svg>`,
	`<svg><style><!--</style><img src=x onerror=alert(1)>`,
	`<math><mtext><table><mglyph><style><img src=x onerror=alert(1)></style></mtext></math>`,
	`<math><annotation-xml encoding="text/html"><p onclick="x">a</p></annotation-xml></math>`,
	`<template><img src=x onerror="alert(1)"><p>hidden</p></template>`,
	`<noscript><p title="</noscript><img src=x onerror=alert(1)>">`,
	`<noembed><img src=x onerror=alert(1)></noembed>`,
	`<noframes><img src=x onerror=alert(1)></noframes>`,
	`<xmp><img src=x onerror=alert(1)></xmp>`,
	`<plaintext><img src=x onerror=alert(1)>`,
	`<textarea><img src=x onerror=alert(1)></textarea>`,
	`<title><img src=x onerror=alert(1)></title>`,
	`<frame src="x">`,
	`<frameset><frame src="x"></frameset>`,

	// Comments.
	`<!-- ordinary comment -->`,
	`<!--><script>alert(1)</script-->`,
	`<!--[if IE]><script>alert(1)</script><![endif]-->`,
	`<div><![CDATA[<img src=x onerror=alert(1)>]]></div>`,

	// Malformed and unbalanced — the highest-yield inputs for idempotence.
	`<p><b>unclosed bold`,
	`</p></div></b>`,
	`<p<b>malformed name</b>`,
	`<div class=unquoted id=also>x</div>`,
	`<img src=x alt=y`,
	`<a href=https://x.example.com/ >bare</a>`,
	`<p title="unterminated>text</p>`,
	`<table><td>orphan cell</td></table>`,
	`<ul><p>list-model violation</p></ul>`,
	`<b><i>overlapping</b></i>`,
	`<p>` + strings.Repeat("<span>", 12) + "deep" + strings.Repeat("</span>", 12) + `</p>`,
	`<` + `p>leading angle`,
	`< p>space after angle`,
	`<p/>self closing`,
	`<div //>weird</div>`,
	"\x00null byte",
	"<p>\r\n\ttabs and newlines\r</p>",
}

// generateCorpus builds a deterministic corpus by recombining fragments.
// Deterministic because a security property test that fails one run in ten
// is a test people learn to re-run rather than read.
func generateCorpus(t *testing.T, docs int) []string {
	t.Helper()
	rng := rand.New(rand.NewSource(0x092D0C5))
	out := make([]string, 0, docs+len(corpusFragments))

	// Every fragment on its own, so a fragment that breaks a property is
	// reported in isolation rather than buried in a composite.
	out = append(out, corpusFragments...)

	for range docs {
		var b strings.Builder
		for n := rng.Intn(5) + 1; n > 0; n-- {
			f := corpusFragments[rng.Intn(len(corpusFragments))]
			switch rng.Intn(8) {
			case 0:
				// Truncate mid-fragment: manufactures unterminated tags,
				// half-written entities and split attribute values.
				if len(f) > 1 {
					f = f[:rng.Intn(len(f)-1)+1]
				}
			case 1:
				// Wrap in an allowed container, changing the parse context.
				f = "<div>" + f + "</div>"
			case 2:
				// Wrap in a container whose contents are skipped, to check
				// the skip flag clears correctly.
				f = "<template>" + f + "</template>"
			case 3:
				// Wrap in a table, whose parse rules relocate stray content.
				f = "<table><tr><td>" + f + "</td></tr></table>"
			}
			b.WriteString(f)
		}
		out = append(out, b.String())
	}
	return out
}

// ── the properties ────────────────────────────────────────────────────────

// TestSanitize_Idempotent is SC-002's property. It is the reason this file
// exists.
func TestSanitize_Idempotent(t *testing.T) {
	for i, in := range generateCorpus(t, 4000) {
		once, err := Sanitize(in)
		if err != nil {
			// A bounds failure is a legitimate outcome; an unclassified
			// one is not.
			if code := ErrorCode(err); code == CodeInternal {
				t.Fatalf("corpus[%d]: unclassified error %v for input %q", i, err, in)
			}
			continue
		}
		twice, err := Sanitize(once)
		if err != nil {
			t.Fatalf("corpus[%d]: sanitized output failed to re-sanitize: %v\nin:   %q\nonce: %q", i, err, in, once)
		}
		if twice != once {
			t.Fatalf("corpus[%d]: Sanitize is not idempotent\nin:    %q\nonce:  %q\ntwice: %q", i, in, once, twice)
		}
		// A third pass costs nothing and would catch a two-cycle
		// oscillation that the second pass alone cannot see.
		thrice, err := Sanitize(twice)
		if err != nil || thrice != once {
			t.Fatalf("corpus[%d]: Sanitize oscillates\nin:     %q\nonce:   %q\nthrice: %q (err %v)", i, in, once, thrice, err)
		}
	}
}

// TestSanitize_CorpusIsInert asserts closure and inertness over the same
// corpus: only allowlisted elements and attributes survive, and no comment
// or doctype does.
func TestSanitize_CorpusIsInert(t *testing.T) {
	for i, in := range generateCorpus(t, 4000) {
		out, err := Sanitize(in)
		if err != nil {
			continue
		}
		if len(out) > MaxBodyBytes {
			t.Fatalf("corpus[%d]: output exceeds MaxBodyBytes", i)
		}
		assertInert(t, out)
		assertNoExternalRefs(t, out)
		if t.Failed() {
			t.Fatalf("corpus[%d] failed the inertness properties\nin:  %q\nout: %q", i, in, out)
		}
	}
}

// FuzzSanitize hands the properties to the fuzzing engine. Under a plain
// `go test` run this exercises the seed corpus only, which is cheap enough
// to belong in CI; `go test -fuzz=FuzzSanitize ./core/docs/` explores past
// it, and any crasher it finds lands in testdata/ as a permanent regression.
func FuzzSanitize(f *testing.F) {
	for _, seed := range corpusFragments {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		once, err := Sanitize(in)
		if err != nil {
			if code := ErrorCode(err); code == CodeInternal {
				t.Fatalf("unclassified error %v", err)
			}
			return
		}
		twice, err := Sanitize(once)
		if err != nil {
			t.Fatalf("re-sanitize failed: %v (once=%q)", err, once)
		}
		if twice != once {
			t.Fatalf("not idempotent\nonce:  %q\ntwice: %q", once, twice)
		}
		assertInert(t, once)
		assertNoExternalRefs(t, once)
	})
}

// ── property assertions, shared with sanitize_test.go ─────────────────────

// allowedElements is the closed set of element names that may appear in a
// sanitized body. Derived from the policy's own lists so the two cannot
// drift, plus the two elements the policy admits via their attributes.
func allowedElements() map[string]struct{} {
	set := map[string]struct{}{
		// Admitted by AllowAttrs(...).OnElements(...) rather than by
		// AllowElements, since both are meaningless without their
		// attribute.
		"a": {}, "img": {},
	}
	for _, group := range [][]string{blockElements, inlineElements, tableElements} {
		for _, el := range group {
			set[el] = struct{}{}
		}
	}
	return set
}

// allowedAttributes is the closed set of attribute names that may appear in
// a sanitized body. "rel" is here because RequireNoFollowOnLinks adds it.
var allowedAttributes = map[string]struct{}{
	"class": {}, "lang": {}, "dir": {}, "title": {}, "style": {},
	"href": {}, "rel": {},
	"src": {}, "alt": {}, "width": {}, "height": {},
	"colspan": {}, "rowspan": {}, "scope": {}, "span": {},
	"start": {}, "type": {}, "cite": {}, "datetime": {},
}

// dataImageSrcPrefixes mirrors dataImageMimes with the scheme attached.
func dataImageSrcPrefixes() []string {
	out := make([]string, 0, len(dataImageMimes))
	for _, m := range dataImageMimes {
		out = append(out, "data:"+m)
	}
	return out
}

// assertInert walks a sanitized body and fails on anything outside the
// allowlists, on comments, and on doctypes. Because the allowlists are
// closed sets, this single assertion covers the whole "no active content"
// family — including every on* handler, present and future — without
// enumerating attacks.
func assertInert(t *testing.T, out string) {
	t.Helper()
	els, attrs := allowedElements(), allowedAttributes
	z := html.NewTokenizer(strings.NewReader(out))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return
		case html.CommentToken:
			t.Errorf("sanitized output contains a comment: %q", z.Token().Data)
		case html.DoctypeToken:
			t.Errorf("sanitized output contains a doctype: %q", z.Token().Data)
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			tok := z.Token()
			name := strings.ToLower(tok.Data)
			if _, ok := els[name]; !ok {
				t.Errorf("sanitized output contains a disallowed element <%s>", name)
			}
			for _, a := range tok.Attr {
				key := strings.ToLower(a.Key)
				if _, ok := attrs[key]; !ok {
					t.Errorf("sanitized output contains a disallowed attribute %s=%q on <%s>", key, a.Val, name)
				}
			}
		}
	}
}

// assertNoExternalRefs is FR-005's "no external network references survive",
// stated over the output's attribute values rather than over substrings:
// links may be https or mailto, image sources must be inline raster data,
// and no style declaration may fetch.
func assertNoExternalRefs(t *testing.T, out string) {
	t.Helper()
	prefixes := dataImageSrcPrefixes()
	z := html.NewTokenizer(strings.NewReader(out))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			for _, a := range tok.Attr {
				val := strings.TrimSpace(a.Val)
				switch strings.ToLower(a.Key) {
				case "href", "cite":
					if !strings.HasPrefix(val, "https://") && !strings.HasPrefix(val, "mailto:") {
						t.Errorf("%s on <%s> is neither an https nor a mailto URL: %q", a.Key, tok.Data, val)
					}
				case "src":
					if !hasAnyPrefix(strings.ToLower(val), prefixes) {
						t.Errorf("src on <%s> is not an inline raster image: %q", tok.Data, val)
					}
				case "style":
					for _, forbidden := range []string{"url(", "@import", "expression(", "binding", "behavior"} {
						if strings.Contains(strings.ToLower(val), forbidden) {
							t.Errorf("style on <%s> contains %q: %q", tok.Data, forbidden, val)
						}
					}
				}
			}
		}
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// TestAllowlistsAreClosed is a guard on the test helpers themselves: if
// someone widens the policy in sanitize.go without widening
// allowedAttributes, the property tests would start failing for a confusing
// reason. This fails first, with a clearer message.
func TestAllowlistsAreClosed(t *testing.T) {
	// A document exercising every attribute the policy allows.
	in := `<div class="c" lang="en" dir="ltr" title="t" style="color:red">` +
		`<a href="https://x.example.com/">l</a>` +
		`<img src="data:image/png;base64,iVBORw0KGgo=" alt="a" width="1" height="1">` +
		`<table><colgroup><col span="2"></colgroup><tr><th scope="col">h</th>` +
		`<td colspan="1" rowspan="1">d</td></tr></table>` +
		`<ol start="1" type="1"><li>x</li></ol>` +
		`<blockquote cite="https://x.example.com/">q</blockquote>` +
		`<time datetime="2026-08-08">t</time></div>`
	out, err := Sanitize(in)
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	seen := map[string]struct{}{}
	z := html.NewTokenizer(strings.NewReader(out))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			for _, a := range z.Token().Attr {
				seen[strings.ToLower(a.Key)] = struct{}{}
			}
		}
	}
	// Every attribute the policy allows should be reachable by this
	// document; anything missing means the policy and this document have
	// drifted apart.
	for want := range allowedAttributes {
		if _, ok := seen[want]; !ok {
			t.Errorf("allowedAttributes lists %q but no sanitized output produces it — is the policy or this fixture stale?", want)
		}
	}
	if t.Failed() {
		t.Logf("sanitized fixture: %s", out)
	}
}
