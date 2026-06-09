package http_loopback

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/acp"
)

func TestIsLoopbackHost(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"127.0.0.1":       true,
		"127.1.2.3":       true,
		"::1":             true,
		"localhost":       true,
		"localhost.":      true,
		"LOCALHOST":       true,
		"":                false,
		"0.0.0.0":         false,
		"8.8.8.8":         false,
		"93.184.216.34":   false,
		"2001:db8::1":     false,
		"example.com":     false,
		"api.openai.com":  false,
	}
	for host, want := range cases {
		got := IsLoopbackHost(host)
		if got != want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestIsLoopbackEndpoint(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"http://127.0.0.1:8080":    true,
		"http://localhost:9100":    true,
		"http://[::1]:9100/x":      true,
		"http://example.com":       false,
		"http://0.0.0.0:9100":      false,
		"127.0.0.1:9100":           true,
		"8.8.8.8:53":               false,
		"":                         false,
	}
	for endpoint, want := range cases {
		got := IsLoopbackEndpoint(endpoint)
		if got != want {
			t.Errorf("IsLoopbackEndpoint(%q) = %v, want %v", endpoint, got, want)
		}
	}
}

func TestListenAndDialLoopback(t *testing.T) {
	t.Parallel()
	ln, err := Listen(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen err = %v", err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 32)
		n, _ := c.Read(buf)
		_, _ = c.Write(buf[:n])
		_ = c.Close()
	}()

	url := "http://" + ln.Addr().String()
	c, err := Dial(context.Background(), url)
	if err != nil {
		t.Fatalf("Dial err = %v", err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 32)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Errorf("echo = %q, want hello", buf[:n])
	}
	wg.Wait()
}

func TestListenRefusesWildcard(t *testing.T) {
	t.Parallel()
	cases := []string{
		"0.0.0.0:0",
		"[::]:0",
		":9100",
	}
	for _, c := range cases {
		if _, err := Listen(context.Background(), c); !errors.Is(err, acp.ErrTransportRefused) {
			t.Errorf("Listen(%q) err = %v, want ErrTransportRefused", c, err)
		}
	}
}

func TestListenRefusesNonLoopback(t *testing.T) {
	t.Parallel()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skip("interface enumeration unavailable")
	}
	var nonLoopback string
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.To4() == nil {
			continue
		}
		nonLoopback = ipNet.IP.String()
		break
	}
	if nonLoopback == "" {
		t.Skip("no non-loopback interface available")
	}
	addr := net.JoinHostPort(nonLoopback, "0")
	if _, err := Listen(context.Background(), addr); !errors.Is(err, acp.ErrTransportRefused) {
		t.Errorf("Listen(%q) err = %v, want ErrTransportRefused", addr, err)
	}
}

func TestDialRefusesNonLoopback(t *testing.T) {
	t.Parallel()
	cases := []string{
		"http://93.184.216.34:80",
		"http://example.com:80",
		"8.8.8.8:53",
		"http://0.0.0.0:9100",
	}
	for _, c := range cases {
		_, err := Dial(context.Background(), c)
		if !errors.Is(err, acp.ErrTransportRefused) {
			t.Errorf("Dial(%q) err = %v, want ErrTransportRefused", c, err)
		}
	}
}

func TestDialerEnvelopeIntegration(t *testing.T) {
	t.Parallel()
	ln, err := Listen(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		_, _ = c.Write(buf[:n])
		_ = c.Close()
	}()

	d := Dialer{}
	if d.Kind() != Kind {
		t.Errorf("Kind = %s, want %s", d.Kind(), Kind)
	}
	conn, err := d.Dial(context.Background(), acp.PeerProfile{
		PeerID:      "p",
		Transport:   acp.TransportLoopback,
		EndpointURL: "http://" + ln.Addr().String(),
	})
	if err != nil {
		t.Fatalf("Dialer.Dial err = %v", err)
	}
	defer conn.Close()
	resp, err := conn.Send(context.Background(), acp.KindRequest, []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("Send err = %v", err)
	}
	if string(resp) != `{"x":1}` {
		t.Errorf("echo = %q", resp)
	}
	wg.Wait()
}

func TestEphemeralPortExposed(t *testing.T) {
	t.Parallel()
	ln, err := Listen(context.Background(), "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().String()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("Addr = %q, want 127.0.0.1:<port>", addr)
	}
}
