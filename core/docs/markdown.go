package docs

import (
	"fmt"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Markdown export (spec 092 FR-015, SC-003).
//
// The converter accepts exactly the vocabulary Sanitize emits and maps it
// onto CommonMark plus the GFM table and strikethrough extensions. The rule
// that governs every case: **nothing is lost silently.** Where Markdown has
// no equivalent, the text content is kept and a warning naming the
// construct is added to the result. Warnings are deduplicated and ordered
// by first occurrence, so identical input yields identical output.
//
// Degradation table (the published rules FR-015 asks for):
//
//	construct                          markdown                         warning
//	h1..h6, p, hr, blockquote          native                           —
//	ul, ol (incl. start), nested lists native                           —
//	strong/b, em/i, s/del/strike       **x**, *x*, ~~x~~                —
//	code, kbd, samp                    `x`                              —
//	pre > code.language-X              fenced block tagged X            —
//	  (Mermaid and KaTeX source ride this path unchanged)
//	a[href]                            [text](href)                     —
//	table                              GFM table                        colspan/rowspan, caption, block content in cells
//	img (data: URI)                    *[image: alt]*                   image omitted
//	u, mark, sub, sup, small, ins,     text only                        formatting dropped
//	  abbr, time, ruby, q
//	dl/dt/dd, details/summary,         flattened to paragraphs          structure flattened
//	  figure/figcaption

// MarkdownResult is the output of ExportMarkdown.
type MarkdownResult struct {
	Markdown string
	Warnings []string
}

// ExportMarkdown converts a document body to Markdown.
func ExportMarkdown(doc Document) (MarkdownResult, error) {
	body, err := Sanitize(doc.Body)
	if err != nil {
		return MarkdownResult{}, err
	}
	nodes, err := xhtml.ParseFragment(strings.NewReader(body), &xhtml.Node{
		Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body,
	})
	if err != nil {
		return MarkdownResult{}, fmt.Errorf("docs: parse body: %w", err)
	}
	c := &mdConverter{seen: map[string]bool{}}
	blocks := c.blocks(nodes)
	out := strings.Join(blocks, "\n\n")
	if out != "" {
		out += "\n"
	}
	return MarkdownResult{Markdown: out, Warnings: c.warnings}, nil
}

type mdConverter struct {
	warnings []string
	seen     map[string]bool
}

func (c *mdConverter) warn(msg string) {
	if !c.seen[msg] {
		c.seen[msg] = true
		c.warnings = append(c.warnings, msg)
	}
}

// blocks converts a sibling run into Markdown blocks. Consecutive inline
// and text nodes are grouped into one paragraph.
func (c *mdConverter) blocks(nodes []*xhtml.Node) []string {
	var out []string
	var run []*xhtml.Node
	flush := func() {
		if len(run) == 0 {
			return
		}
		if p := strings.TrimSpace(c.inlines(run)); p != "" {
			out = append(out, p)
		}
		run = nil
	}
	for _, n := range nodes {
		if isBlock(n) {
			flush()
			out = append(out, c.block(n)...)
			continue
		}
		run = append(run, n)
	}
	flush()
	return out
}

func children(n *xhtml.Node) []*xhtml.Node {
	var out []*xhtml.Node
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		out = append(out, ch)
	}
	return out
}

func isBlock(n *xhtml.Node) bool {
	if n.Type != xhtml.ElementNode {
		return false
	}
	switch n.Data {
	case "p", "hr", "div", "h1", "h2", "h3", "h4", "h5", "h6",
		"ul", "ol", "li", "dl", "dt", "dd", "blockquote", "pre", "figure", "figcaption",
		"section", "article", "header", "footer", "main", "aside", "nav",
		"details", "summary", "table", "caption", "thead", "tbody", "tfoot", "tr", "th", "td",
		"colgroup", "col":
		return true
	}
	return false
}

func (c *mdConverter) block(n *xhtml.Node) []string {
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(n.Data[1] - '0')
		text := strings.TrimSpace(c.inlines(children(n)))
		if text == "" {
			return nil
		}
		return []string{strings.Repeat("#", level) + " " + text}
	case "p":
		if text := strings.TrimSpace(c.inlines(children(n))); text != "" {
			return []string{text}
		}
		return nil
	case "hr":
		return []string{"---"}
	case "div", "section", "article", "header", "footer", "main", "aside", "nav":
		return c.blocks(children(n))
	case "figure", "details", "dl":
		c.warn(fmt.Sprintf("<%s> structure flattened to paragraphs", n.Data))
		return c.blocks(children(n))
	case "summary", "dt":
		if text := strings.TrimSpace(c.inlines(children(n))); text != "" {
			return []string{"**" + text + "**"}
		}
		return nil
	case "figcaption", "caption":
		if text := strings.TrimSpace(c.inlines(children(n))); text != "" {
			return []string{"*" + text + "*"}
		}
		return nil
	case "dd", "li", "td", "th", "tr", "thead", "tbody", "tfoot":
		// Only reachable when these appear outside their parent; the
		// sanitizer permits it, so flatten rather than drop.
		return c.blocks(children(n))
	case "colgroup", "col":
		return nil
	case "blockquote":
		inner := strings.Join(c.blocks(children(n)), "\n\n")
		if inner == "" {
			return nil
		}
		return []string{prefixLines(inner, "> ", ">")}
	case "pre":
		return []string{c.pre(n)}
	case "ul", "ol":
		return []string{c.list(n, "")}
	case "table":
		return c.table(n)
	}
	return nil
}

func prefixLines(s, prefix, emptyPrefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = emptyPrefix
		} else {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

func (c *mdConverter) pre(n *xhtml.Node) string {
	lang := ""
	src := textContent(n)
	if code := firstElementChild(n, "code"); code != nil {
		for _, cls := range strings.Fields(attr(code, "class")) {
			if strings.HasPrefix(cls, "language-") {
				lang = strings.TrimPrefix(cls, "language-")
				break
			}
		}
	}
	src = strings.TrimSuffix(src, "\n")
	fence := "```"
	for strings.Contains(src, fence) {
		fence += "`"
	}
	return fence + lang + "\n" + src + "\n" + fence
}

func (c *mdConverter) list(n *xhtml.Node, indent string) string {
	ordered := n.Data == "ol"
	num := 1
	if ordered {
		if s, err := strconv.Atoi(attr(n, "start")); err == nil && s > 0 {
			num = s
		}
	}
	var lines []string
	for _, li := range children(n) {
		if li.Type != xhtml.ElementNode || li.Data != "li" {
			if li.Type == xhtml.ElementNode || strings.TrimSpace(textContent(li)) != "" {
				c.warn(fmt.Sprintf("<%s> directly inside a list flattened", li.Data))
				if t := strings.TrimSpace(c.inlines([]*xhtml.Node{li})); t != "" {
					lines = append(lines, indent+"- "+t)
				}
			}
			continue
		}
		marker := "- "
		if ordered {
			marker = strconv.Itoa(num) + ". "
			num++
		}
		cont := indent + strings.Repeat(" ", len(marker))
		var itemLines []string
		var run []*xhtml.Node
		flush := func() {
			if t := strings.TrimSpace(c.inlines(run)); t != "" {
				itemLines = append(itemLines, t)
			}
			run = nil
		}
		for _, ch := range children(li) {
			switch {
			case ch.Type == xhtml.ElementNode && (ch.Data == "ul" || ch.Data == "ol"):
				flush()
				itemLines = append(itemLines, "\x00"+c.list(ch, cont))
			case isBlock(ch):
				flush()
				for _, b := range c.block(ch) {
					itemLines = append(itemLines, b)
				}
			default:
				run = append(run, ch)
			}
		}
		flush()
		first := true
		for _, il := range itemLines {
			if strings.HasPrefix(il, "\x00") {
				lines = append(lines, strings.TrimPrefix(il, "\x00"))
				continue
			}
			for _, l := range strings.Split(il, "\n") {
				if first {
					lines = append(lines, indent+marker+l)
					first = false
				} else {
					lines = append(lines, cont+l)
				}
			}
		}
		if first {
			lines = append(lines, indent+strings.TrimRight(marker, " "))
		}
	}
	return strings.Join(lines, "\n")
}

func (c *mdConverter) table(n *xhtml.Node) []string {
	var out []string
	var rows [][]string
	var headerRow = -1
	var walk func(*xhtml.Node)
	walk = func(x *xhtml.Node) {
		for _, ch := range children(x) {
			if ch.Type != xhtml.ElementNode {
				continue
			}
			switch ch.Data {
			case "caption":
				out = append(out, c.block(ch)...)
				c.warn("table <caption> moved above the table")
			case "thead", "tbody", "tfoot":
				walk(ch)
			case "tr":
				var cells []string
				allHeader := true
				for _, cell := range children(ch) {
					if cell.Type != xhtml.ElementNode || (cell.Data != "td" && cell.Data != "th") {
						continue
					}
					if cell.Data != "th" {
						allHeader = false
					}
					if attr(cell, "colspan") != "" || attr(cell, "rowspan") != "" {
						c.warn("table cell colspan/rowspan dropped")
					}
					cells = append(cells, c.cell(cell))
				}
				if len(cells) == 0 {
					continue
				}
				if headerRow < 0 && allHeader && len(rows) == 0 {
					headerRow = 0
				}
				rows = append(rows, cells)
			}
		}
	}
	walk(n)
	if len(rows) == 0 {
		return out
	}
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}
	line := func(cells []string) string {
		padded := make([]string, width)
		copy(padded, cells)
		return "| " + strings.Join(padded, " | ") + " |"
	}
	var t []string
	body := rows
	if headerRow == 0 {
		t = append(t, line(rows[0]))
		body = rows[1:]
	} else {
		// GFM requires a header row; an empty one keeps every data row as data.
		t = append(t, line(nil))
	}
	sep := make([]string, width)
	for i := range sep {
		sep[i] = "---"
	}
	t = append(t, "| "+strings.Join(sep, " | ")+" |")
	for _, r := range body {
		t = append(t, line(r))
	}
	return append(out, strings.Join(t, "\n"))
}

// cell renders table cell content on one line.
func (c *mdConverter) cell(n *xhtml.Node) string {
	var parts []string
	var run []*xhtml.Node
	flush := func() {
		if t := strings.TrimSpace(c.inlines(run)); t != "" {
			parts = append(parts, t)
		}
		run = nil
	}
	for _, ch := range children(n) {
		if isBlock(ch) {
			flush()
			c.warn("block content inside a table cell flattened")
			for _, b := range c.block(ch) {
				parts = append(parts, strings.ReplaceAll(b, "\n", " "))
			}
			continue
		}
		run = append(run, ch)
	}
	flush()
	return escapeCellPipes(strings.ReplaceAll(strings.Join(parts, " "), "\n", " "))
}

// escapeCellPipes escapes every pipe not already escaped. Running text
// arrives with pipes escaped by escapeMarkdown; code spans do not, and GFM
// splits cells on a bare pipe even inside a code span. A pipe is already
// escaped only when an odd number of backslashes precede it.
func escapeCellPipes(s string) string {
	var b strings.Builder
	backslashes := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '|' && backslashes%2 == 0 {
			b.WriteByte('\\')
		}
		if ch == '\\' {
			backslashes++
		} else {
			backslashes = 0
		}
		b.WriteByte(ch)
	}
	return b.String()
}

// inlines renders a run of inline nodes, collapsing whitespace the way a
// browser would.
func (c *mdConverter) inlines(nodes []*xhtml.Node) string {
	var b strings.Builder
	for _, n := range nodes {
		c.inline(&b, n)
	}
	return collapseSpaces(b.String())
}

func (c *mdConverter) inline(b *strings.Builder, n *xhtml.Node) {
	switch n.Type {
	case xhtml.TextNode:
		b.WriteString(escapeMarkdown(n.Data))
		return
	case xhtml.ElementNode:
	default:
		return
	}
	wrap := func(mark string) {
		inner := strings.TrimSpace(c.inlines(children(n)))
		if inner != "" {
			b.WriteString(mark + inner + mark)
		}
	}
	switch n.Data {
	case "strong", "b":
		wrap("**")
	case "em", "i", "cite", "dfn", "var":
		wrap("*")
	case "s", "strike", "del":
		wrap("~~")
	case "code", "kbd", "samp":
		src := textContent(n)
		tick := "`"
		for strings.Contains(src, tick) {
			tick += "`"
		}
		pad := ""
		if strings.HasPrefix(src, "`") || strings.HasSuffix(src, "`") {
			pad = " "
		}
		b.WriteString(tick + pad + src + pad + tick)
	case "a":
		text := strings.TrimSpace(c.inlines(children(n)))
		href := attr(n, "href")
		if href == "" {
			b.WriteString(text)
			return
		}
		if text == "" {
			text = escapeMarkdown(href)
		}
		b.WriteString("[" + text + "](<" + strings.ReplaceAll(href, ">", "%3E") + ">)")
	case "img":
		alt := strings.TrimSpace(attr(n, "alt"))
		if alt == "" {
			alt = "embedded image"
		}
		c.warn("embedded image omitted (Markdown export carries alt text only)")
		b.WriteString("*[image: " + escapeMarkdown(alt) + "]*")
	case "br":
		b.WriteString("\\\n")
	case "wbr":
	case "q":
		c.warn("<q> rendered as quoted text")
		b.WriteString("“" + strings.TrimSpace(c.inlines(children(n))) + "”")
	case "span":
		b.WriteString(c.inlines(children(n)))
	default:
		c.warn(fmt.Sprintf("<%s> formatting dropped", n.Data))
		b.WriteString(c.inlines(children(n)))
	}
}

// collapseSpaces collapses runs of whitespace other than the hard-break
// sequence emitted for <br>.
func collapseSpaces(s string) string {
	var b strings.Builder
	space := false
	var last byte
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\\' && i+1 < len(s) && s[i+1] == '\n' {
			b.WriteString("\\\n")
			i++
			space = false
			last = '\n'
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f' {
			space = true
			continue
		}
		if space && b.Len() > 0 && last != '\n' {
			b.WriteByte(' ')
		}
		space = false
		b.WriteByte(ch)
		last = ch
	}
	return b.String()
}

// escapeMarkdown escapes the characters that would otherwise be read as
// Markdown syntax inside running text.
func escapeMarkdown(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`,
		"[", `\[`, "]", `\]`, "<", `\<`, ">", `\>`, "#", `\#`, "|", `\|`)
	return r.Replace(s)
}

func attr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}

func firstElementChild(n *xhtml.Node, tag string) *xhtml.Node {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == xhtml.ElementNode && ch.Data == tag {
			return ch
		}
	}
	return nil
}

func textContent(n *xhtml.Node) string {
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(x *xhtml.Node) {
		if x.Type == xhtml.TextNode {
			b.WriteString(x.Data)
		}
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(n)
	return b.String()
}
