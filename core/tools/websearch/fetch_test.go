package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetcher_RoundTrip(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "kenaz-harness/") {
			t.Errorf("missing UA: %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>hi</body></html>"))
	}))
	defer srv.Close()

	f, err := NewFetcher(FetcherOpts{Transport: srv.Client().Transport})
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	body, ct, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(string(body), "hi") {
		t.Errorf("body = %q", string(body))
	}
	if !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
}

func TestFetcher_RedirectCap(t *testing.T) {
	t.Parallel()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		http.Redirect(w, r, fmt.Sprintf("/%d", n), http.StatusFound)
	}))
	defer srv.Close()

	f, err := NewFetcher(FetcherOpts{
		Transport:    srv.Client().Transport,
		MaxRedirects: 3,
	})
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	_, _, err = f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected redirect-cap error")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("error not redirect-related: %v", err)
	}
}

func TestFetcher_OversizeBody(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("a", 64<<10) // 64 KiB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()

	f, err := NewFetcher(FetcherOpts{
		Transport:    srv.Client().Transport,
		MaxBodyBytes: 1024, // 1 KiB cap
	})
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	body, _, err := f.Fetch(context.Background(), srv.URL)
	if !errors.Is(err, ErrBodyTruncated) {
		t.Fatalf("expected ErrBodyTruncated, got %v", err)
	}
	if len(body) != 1024 {
		t.Errorf("expected 1024 bytes, got %d", len(body))
	}
}

func TestFetcher_Timeout(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	f, err := NewFetcher(FetcherOpts{
		Transport: srv.Client().Transport,
		Timeout:   20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	_, _, err = f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestFetcher_BadProxyURL(t *testing.T) {
	t.Parallel()
	_, err := NewFetcher(FetcherOpts{ProxyURL: "ht!tp://[::badproxy]"})
	if err == nil {
		t.Fatal("expected proxy-parse error")
	}
}

func TestFetcher_ProxyRoundTripped(t *testing.T) {
	t.Parallel()
	// httptest server stands in as a proxy; assert it gets the
	// outbound request.
	var got int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&got, 1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>via proxy</html>"))
	}))
	defer proxy.Close()

	f, err := NewFetcher(FetcherOpts{ProxyURL: proxy.URL})
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	body, _, err := f.Fetch(context.Background(), "http://upstream.invalid/path")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if atomic.LoadInt32(&got) == 0 {
		t.Fatal("proxy was not contacted")
	}
	if !strings.Contains(string(body), "via proxy") {
		t.Errorf("body did not come from proxy: %q", string(body))
	}
}

func TestFetcher_EmptyURL(t *testing.T) {
	t.Parallel()
	f, err := NewFetcher(FetcherOpts{})
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	_, _, err = f.Fetch(context.Background(), "")
	if err == nil {
		t.Fatal("expected error on empty URL")
	}
}

func TestFetcher_ErrorStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	f, err := NewFetcher(FetcherOpts{Transport: srv.Client().Transport})
	if err != nil {
		t.Fatalf("NewFetcher: %v", err)
	}
	_, _, err = f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error on 404")
	}
}
