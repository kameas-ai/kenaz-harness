package web

import (
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Extractor declares one CSS-selector extraction rule.
//
//   name       — the key in the output map
//   selector   — a CSS selector (goquery syntax)
//   attr       — when non-empty, extract the named HTML attribute instead
//                of the text content
//   multiple   — when true collect all matches; otherwise only the first
type Extractor struct {
	Name     string `json:"name"`
	Selector string `json:"selector"`
	Attr     string `json:"attr,omitempty"`
	Multiple bool   `json:"multiple,omitempty"`
}

// ScrapeCSS runs the supplied extractors against the HTML body and
// returns a map of name → (string | []string). Each extractor either
// returns a single string (multiple==false) or a slice (multiple==true).
//
// Unknown / unmatched selectors return an empty string / empty slice
// rather than an error, so partial extraction never aborts the step.
func ScrapeCSS(htmlBody string, extractors []Extractor) (map[string]any, error) {
	if len(extractors) == 0 {
		return map[string]any{}, nil
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlBody))
	if err != nil {
		return nil, fmt.Errorf("web: parse HTML for CSS extraction: %w", err)
	}

	out := make(map[string]any, len(extractors))
	for _, ex := range extractors {
		if ex.Name == "" {
			continue
		}
		sel := doc.Find(ex.Selector)
		if ex.Multiple {
			vals := make([]string, 0, sel.Length())
			sel.Each(func(_ int, s *goquery.Selection) {
				vals = append(vals, extractValue(s, ex.Attr))
			})
			out[ex.Name] = vals
		} else {
			out[ex.Name] = extractValue(sel.First(), ex.Attr)
		}
	}
	return out, nil
}

// extractValue returns either the attribute value (when attr is set)
// or the trimmed text content of the selection.
func extractValue(s *goquery.Selection, attr string) string {
	if attr != "" {
		v, _ := s.Attr(attr)
		return v
	}
	return strings.TrimSpace(s.Text())
}
