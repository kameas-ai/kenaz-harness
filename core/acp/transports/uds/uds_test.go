//go:build darwin || linux || freebsd

package uds

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"kaneaz-harness/core/acp"
)

// Use a short socket path because the kernel limits sun_path to ~104
// chars on darwin and 108 on linux; t.TempDir() can exceed that.
func tempSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "acp-uds-")
	if err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func TestUDSListenAndDial(t *testing.T) {
	t.Parallel()
	path := tempSocket(t)
	ln, err := Listen(context.Background(), path)
	if err != nil {
		t.Fatalf("Listen err = %v", err)
	}
	defer ln.Close()

	// Permissions check: socket file must be 0600.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket perm = %o, want 0600", perm)
	}

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

	c, err := Dial(context.Background(), path)
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

func TestUDSStaleSocketCleanup(t *testing.T) {
	t.Parallel()
	path := tempSocket(t)
	// Pre-create a stale socket file.
	tmpLn, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("pre-create listen: %v", err)
	}
	_ = tmpLn.Close()
	// File should still exist.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale socket missing: %v", err)
	}
	// Listen should remove the stale socket and rebind.
	ln, err := Listen(context.Background(), path)
	if err != nil {
		t.Fatalf("Listen err = %v", err)
	}
	_ = ln.Close()
}

func TestUDSRefuseOverwriteRegularFile(t *testing.T) {
	t.Parallel()
	path := tempSocket(t)
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := Listen(context.Background(), path); err == nil {
		t.Error("expected refusal to overwrite a non-socket file")
	}
}

func TestUDSRefusalOnTCPDial(t *testing.T) {
	t.Parallel()
	// A loopback TCP listener on a random port. Dial-via-UDS at that
	// path must fail; we are only verifying that the UDS dialer
	// refuses non-UDS endpoints, not that arbitrary TCP listeners
	// emit useful errors.
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tcp listen: %v", err)
	}
	defer tcp.Close()

	path := tempSocket(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = Dial(ctx, path)
	if !errors.Is(err, acp.ErrTransportRefused) {
		t.Errorf("Dial of non-existent socket should refuse with ErrTransportRefused, got %v", err)
	}
}

func TestUDSDialerEnvelopeIntegration(t *testing.T) {
	t.Parallel()
	path := tempSocket(t)
	ln, err := Listen(context.Background(), path)
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
		Transport:   acp.TransportUDS,
		EndpointURL: "unix://" + path,
	})
	if err != nil {
		t.Fatalf("Dialer.Dial err = %v", err)
	}
	defer conn.Close()
	resp, err := conn.Send(context.Background(), acp.KindRequest, []byte(`{"hi":1}`))
	if err != nil {
		t.Fatalf("Send err = %v", err)
	}
	if string(resp) != `{"hi":1}` {
		t.Errorf("Send echo = %q, want %q", resp, `{"hi":1}`)
	}
	wg.Wait()
}

func TestUDSEmptyPath(t *testing.T) {
	t.Parallel()
	if _, err := Listen(context.Background(), ""); err == nil {
		t.Error("expected error on empty socket path")
	}
	if _, err := Dial(context.Background(), ""); !errors.Is(err, acp.ErrTransportRefused) {
		t.Errorf("Dial empty path err = %v, want ErrTransportRefused", err)
	}
}
