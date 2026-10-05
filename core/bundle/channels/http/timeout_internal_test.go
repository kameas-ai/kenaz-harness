package http

// engine-publication-01ENPUB01 WP-H4 (survey gap 9): the http_mirror
// channel must not cap a healthy large download at a fixed wall-clock
// time. The knobs are shrunk via newChannel so the tests stay fast — the
// shape (slow-but-progressing body outlives the old whole-request bound;
// a stalled body or a silent server still fails) is what is pinned.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bundle "github.com/kameas-ai/kenaz-harness/core/bundle"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

func shrunk() timeouts {
	return timeouts{
		probe:  150 * time.Millisecond,
		header: 150 * time.Millisecond,
		stall:  200 * time.Millisecond,
		dial:   time.Second,
		tls:    time.Second,
	}
}

// slowBody streams chunks x interval with a flush per chunk, so the total
// transfer time is chunks*interval while no single gap exceeds interval.
func slowBody(chunks int, interval time.Duration, chunk []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fl := w.(http.Flusher)
		for i := 0; i < chunks; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			fl.Flush()
			select {
			case <-time.After(interval):
			case <-r.Context().Done():
				return
			}
		}
	}
}

func TestFetch_SlowButProgressingBodyOutlivesTheOldWholeRequestCap(t *testing.T) {
	chunk := bytes.Repeat([]byte("x"), 4096)
	const chunks = 20
	interval := 40 * time.Millisecond // 20*40ms = 800ms total, > every knob above
	srv := httptest.NewServer(slowBody(chunks, interval, chunk))
	defer srv.Close()

	// Control: the PRE-WP-H4 client shape (Client.Timeout covering the
	// body) with the same bound as our probe knob dies mid-body — proving
	// this body genuinely outlasts a whole-request cap.
	old := &http.Client{Timeout: shrunk().probe}
	if resp, err := old.Get(srv.URL + "/engine.dmg"); err == nil {
		_, rerr := new(bytes.Buffer).ReadFrom(resp.Body)
		_ = resp.Body.Close()
		if rerr == nil {
			t.Fatal("control: a whole-request Client.Timeout read the slow body to completion; the test is not exercising the bug")
		}
	}

	ch := newChannel(srv.URL, nil, secrets.NoopResolver{}, shrunk())
	var sink bytes.Buffer
	start := time.Now()
	res, err := ch.Fetch(context.Background(), channels.ArtifactCoord{Path: "engine.dmg"}, &sink)
	if err != nil {
		t.Fatalf("slow-but-progressing fetch failed after %s: %v", time.Since(start), err)
	}
	if res.Bytes != int64(chunks*len(chunk)) || sink.Len() != chunks*len(chunk) {
		t.Fatalf("got %d bytes (sink %d), want %d", res.Bytes, sink.Len(), chunks*len(chunk))
	}
	if elapsed := time.Since(start); elapsed < shrunk().stall*2 {
		t.Fatalf("fetch took %s; the body was meant to outlast every knob", elapsed)
	}
}

func TestFetch_StalledBodyFails(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		select { // then nothing, until the client gives up
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	ch := newChannel(srv.URL, nil, secrets.NoopResolver{}, shrunk())
	start := time.Now()
	_, err := ch.Fetch(context.Background(), channels.ArtifactCoord{Path: "engine.dmg"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no data for") {
		t.Fatalf("stalled fetch err = %v, want a stall error", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("stall detection took %s", elapsed)
	}
}

func TestFetch_SilentServerFailsAtHeaderTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	ch := newChannel(srv.URL, nil, secrets.NoopResolver{}, shrunk())
	start := time.Now()
	if _, err := ch.Fetch(context.Background(), channels.ArtifactCoord{Path: "engine.dmg"}, &bytes.Buffer{}); err == nil {
		t.Fatal("a server that never sends headers must fail the fetch")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("header timeout took %s", elapsed)
	}
}

func TestFetch_CallerContextStillBoundsTheDownload(t *testing.T) {
	srv := httptest.NewServer(slowBody(100, 40*time.Millisecond, []byte("x")))
	defer srv.Close()
	ch := newChannel(srv.URL, nil, secrets.NoopResolver{}, shrunk())
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := ch.Fetch(ctx, channels.ArtifactCoord{Path: "engine.dmg"}, &bytes.Buffer{}); err == nil {
		t.Fatal("caller ctx deadline did not abort a progressing download")
	}
}

func TestReachable_KeepsItsWholeRequestBound(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	ch := newChannel(srv.URL, nil, secrets.NoopResolver{}, timeouts{
		probe: 100 * time.Millisecond, header: time.Hour, stall: time.Hour, dial: time.Second, tls: time.Second,
	})
	start := time.Now()
	if err := ch.Reachable(context.Background()); !errors.Is(err, bundle.ErrChannelUnreachable) {
		t.Fatalf("hanging HEAD err = %v, want ErrChannelUnreachable", err)
	}
	if _, err := ch.LookupSignatures(context.Background(), channels.ArtifactCoord{Path: "engine.dmg"}); err == nil {
		t.Fatal("hanging signature probe must fail")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("probe bound took %s", elapsed)
	}
}

func TestFactory_ProductionDefaults(t *testing.T) {
	c, err := Factory(channels.ChannelSpec{Kind: Kind, URL: "https://downloads.kameas.ai/kenaz-ml/1.0.0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	hc := c.(*httpChannel)
	if hc.client.Timeout != 0 {
		t.Fatalf("Client.Timeout = %s; it must be 0 (it would cap the body read)", hc.client.Timeout)
	}
	if hc.timeout != defaultTimeouts() || hc.timeout.stall != 60*time.Second || hc.timeout.probe != 60*time.Second {
		t.Fatalf("timeouts = %+v", hc.timeout)
	}
	tr := hc.client.Transport.(*http.Transport)
	if tr.ResponseHeaderTimeout != defaultHeaderTimeout || tr.TLSHandshakeTimeout != defaultTLSTimeout || tr.Proxy == nil {
		t.Fatalf("transport = header %s tls %s proxy-set %v", tr.ResponseHeaderTimeout, tr.TLSHandshakeTimeout, tr.Proxy != nil)
	}
}
