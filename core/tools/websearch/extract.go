package websearch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	nurl "net/url"
	"strings"

	readability "github.com/go-shiori/go-readability"
)

const (
	defaultExtractMaxBytes = 10 * 1024
	truncationMarker       = "... [truncated]"
)

// Extractor wraps go-readability with a per-result byte cap and a
// recover-guarded call surface — readability has been observed to
// panic on pathological input, and a tool-loop dispatch should never
// crash the harness.
type Extractor struct {
	maxBytes int
}

// NewExtractor returns an Extractor with the default 10 KiB cap.
func NewExtractor() *Extractor {
	return &Extractor{maxBytes: defaultExtractMaxBytes}
}

// NewExtractorWithCap returns an Extractor with a custom byte cap.
// maxBytes <= 0 falls back to the default.
func NewExtractorWithCap(maxBytes int) *Extractor {
	if maxBytes <= 0 {
		maxBytes = defaultExtractMaxBytes
	}
	return &Extractor{maxBytes: maxBytes}
}

// Extract runs go-readability on the supplied HTML and returns the
// reader-view text, capped to e.maxBytes with a truncation marker.
// On a readability panic, Extract returns the raw text salvaged from
// the body (also capped) and a non-nil error so the caller can decide
// whether to surface the raw text.
func (e *Extractor) Extract(ctx context.Context, htmlBytes []byte, baseURL string) (text string, err error) {
	if len(htmlBytes) == 0 {
		return "", fmt.Errorf("websearch/extract: empty body")
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
	}

	defer func() {
		if r := recover(); r != nil {
			raw := stripTags(string(htmlBytes))
			text = e.truncate(raw)
			err = fmt.Errorf("websearch/extract: extract panicked: %v", r)
		}
	}()

	var pageURL *nurl.URL
	if baseURL != "" {
		if u, perr := nurl.Parse(baseURL); perr == nil {
			pageURL = u
		}
	}
	article, rerr := readability.FromReader(bytes.NewReader(htmlBytes), pageURL)
	if rerr != nil {
		raw := stripTags(string(htmlBytes))
		if strings.TrimSpace(raw) == "" {
			return "", fmt.Errorf("websearch/extract: readability: %w", rerr)
		}
		return e.truncate(raw), fmt.Errorf("websearch/extract: readability fallback: %w", rerr)
	}

	out := strings.TrimSpace(article.TextContent)
	if out == "" {
		out = stripTags(article.Content)
	}
	if strings.TrimSpace(out) == "" {
		return "", errors.New("websearch/extract: empty extraction")
	}
	return e.truncate(out), nil
}

func (e *Extractor) truncate(s string) string {
	if e.maxBytes <= 0 || len(s) <= e.maxBytes {
		return s
	}
	cut := e.maxBytes - len(truncationMarker)
	if cut < 0 {
		cut = 0
	}
	return s[:cut] + truncationMarker
}

// stripTags is a deliberately-tiny HTML tag stripper used only as a
// last-resort fallback when go-readability returns nothing useful.
// It is not a substitute for proper extraction.
func stripTags(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	depth := 0
	for _, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
