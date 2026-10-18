package mlsidecar

import (
	"os"
	"testing"
	"time"
)

// TestLease_AcquireRenewRelease covers the basic file-lease lifecycle:
// write, read back, renew (mtime advances), release (file gone).
func TestLease_AcquireRenewRelease(t *testing.T) {
	l := NewLayout(t.TempDir())
	if err := AcquireOrRenewLease(l, "harness", 4242, "0.84.0"); err != nil {
		t.Fatalf("AcquireOrRenewLease: %v", err)
	}
	info, ok, err := ReadLease(l, "harness")
	if err != nil || !ok {
		t.Fatalf("ReadLease: ok=%v err=%v", ok, err)
	}
	if info.PID != 4242 || info.ClientVersion != "0.84.0" || info.Client != "harness" {
		t.Errorf("got %+v", info)
	}

	first := info.ModTime
	time.Sleep(10 * time.Millisecond)
	if err := RenewLease(l, "harness", 4242, "0.84.0"); err != nil {
		t.Fatalf("RenewLease: %v", err)
	}
	info2, ok, err := ReadLease(l, "harness")
	if err != nil || !ok {
		t.Fatalf("ReadLease after renew: ok=%v err=%v", ok, err)
	}
	if !info2.ModTime.After(first) {
		t.Errorf("expected mtime to advance on renew: first=%v second=%v", first, info2.ModTime)
	}

	if err := ReleaseLease(l, "harness"); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	if _, ok, err := ReadLease(l, "harness"); err != nil || ok {
		t.Fatalf("expected lease gone after release: ok=%v err=%v", ok, err)
	}
	// Releasing an already-released lease is a no-op, not an error.
	if err := ReleaseLease(l, "harness"); err != nil {
		t.Fatalf("ReleaseLease (idempotent): %v", err)
	}
}

// TestLeaseInfo_IsStale pins the 90s staleness bound (design §3.7 R4).
func TestLeaseInfo_IsStale(t *testing.T) {
	now := time.Now()
	fresh := LeaseInfo{ModTime: now.Add(-30 * time.Second)}
	if fresh.IsStale(now) {
		t.Error("30s-old lease must not be stale (staleness bound is 90s)")
	}
	stale := LeaseInfo{ModTime: now.Add(-91 * time.Second)}
	if !stale.IsStale(now) {
		t.Error("91s-old lease must be stale")
	}
	boundary := LeaseInfo{ModTime: now.Add(-90 * time.Second)}
	if boundary.IsStale(now) {
		t.Error("exactly-90s-old lease must not yet be stale (> not >=)")
	}
}

// TestSweepStaleLeases_DiscardsStaleAndDeadPidLeases is the "lease
// heartbeat + staleness" proof: a fresh, live-pid lease survives a
// sweep; a stale-by-mtime lease and a live-mtime-but-dead-pid lease are
// both discarded (design §3.7 R5: "a crashed client never pins the
// sidecar").
func TestSweepStaleLeases_DiscardsStaleAndDeadPidLeases(t *testing.T) {
	l := NewLayout(t.TempDir())

	if err := AcquireOrRenewLease(l, "fresh-alive", os.Getpid(), "1.0.0"); err != nil {
		t.Fatalf("acquire fresh-alive: %v", err)
	}
	if err := AcquireOrRenewLease(l, "stale-alive", os.Getpid(), "1.0.0"); err != nil {
		t.Fatalf("acquire stale-alive: %v", err)
	}
	if err := AcquireOrRenewLease(l, "fresh-dead", 999999, "1.0.0"); err != nil {
		t.Fatalf("acquire fresh-dead: %v", err)
	}
	// Backdate stale-alive's mtime past the staleness bound.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(l.LeaseFile("stale-alive"), old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	isAlive := func(pid int) bool { return pid == os.Getpid() } // only "our own" pid counts as alive
	removed, err := SweepStaleLeases(l, time.Now(), isAlive)
	if err != nil {
		t.Fatalf("SweepStaleLeases: %v", err)
	}

	removedSet := map[string]bool{}
	for _, c := range removed {
		removedSet[c] = true
	}
	if removedSet["fresh-alive"] {
		t.Error("fresh-alive (fresh mtime, live pid) must survive the sweep")
	}
	if !removedSet["stale-alive"] {
		t.Error("stale-alive (stale mtime) must be discarded")
	}
	if !removedSet["fresh-dead"] {
		t.Error("fresh-dead (dead pid) must be discarded even with a fresh mtime")
	}
	if _, ok, _ := ReadLease(l, "fresh-alive"); !ok {
		t.Error("fresh-alive lease file should still exist on disk")
	}
	if _, ok, _ := ReadLease(l, "stale-alive"); ok {
		t.Error("stale-alive lease file should have been removed")
	}
}

// TestAcquireSpawnLock_SerializesAndBreaksOnDeadHolder covers design
// §3.7 R5: a live holder blocks a second acquisition; a dead holder's
// lock is broken and re-acquired.
func TestAcquireSpawnLock_SerializesAndBreaksOnDeadHolder(t *testing.T) {
	l := NewLayout(t.TempDir())
	alwaysAlive := func(int) bool { return true }
	neverAlive := func(int) bool { return false }

	lock1, err := AcquireSpawnLock(l, os.Getpid(), alwaysAlive)
	if err != nil {
		t.Fatalf("first AcquireSpawnLock: %v", err)
	}
	if _, err := AcquireSpawnLock(l, os.Getpid()+1, alwaysAlive); err != ErrSpawnInProgress {
		t.Fatalf("second AcquireSpawnLock while held by a live pid: err=%v, want ErrSpawnInProgress", err)
	}
	if err := lock1.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// Releasing again (or a nil lock) must be a harmless no-op.
	if err := lock1.Release(); err != nil {
		t.Fatalf("Release (idempotent): %v", err)
	}
	var nilLock *SpawnLock
	if err := nilLock.Release(); err != nil {
		t.Fatalf("Release on nil *SpawnLock: %v", err)
	}

	// A lock left by a now-dead holder must be broken and re-acquired,
	// not permanently block spawning.
	lock2, err := AcquireSpawnLock(l, os.Getpid(), neverAlive)
	if err != nil {
		t.Fatalf("AcquireSpawnLock after clean release: %v", err)
	}
	_ = lock2.Release()

	if _, err := os.OpenFile(l.SpawnLockFile(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err != nil {
		t.Fatalf("seed a foreign lock file: %v", err)
	}
	if err := os.WriteFile(l.SpawnLockFile(), []byte("123456789"), 0o644); err != nil {
		t.Fatalf("write foreign lock content: %v", err)
	}
	lock3, err := AcquireSpawnLock(l, os.Getpid(), neverAlive)
	if err != nil {
		t.Fatalf("AcquireSpawnLock should break a dead holder's lock: %v", err)
	}
	_ = lock3.Release()
}

// TestLocalToken_RoundTrip covers the shutdown-authorization token file.
func TestLocalToken_RoundTrip(t *testing.T) {
	l := NewLayout(t.TempDir())
	if _, ok, err := ReadLocalToken(l); err != nil || ok {
		t.Fatalf("expected no token on a fresh root: ok=%v err=%v", ok, err)
	}
	tok, err := WriteLocalToken(l)
	if err != nil {
		t.Fatalf("WriteLocalToken: %v", err)
	}
	if len(tok) == 0 {
		t.Fatal("expected a non-empty generated token")
	}
	got, ok, err := ReadLocalToken(l)
	if err != nil || !ok {
		t.Fatalf("ReadLocalToken: ok=%v err=%v", ok, err)
	}
	if got != tok {
		t.Errorf("got %q, want %q", got, tok)
	}
	info, err := os.Stat(l.TokenFile())
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("token file mode %v is not user-only", info.Mode())
	}
}
