package fleet

// bundle_v2_cache_test.go — signed-bundle-bytes-01SBBYT01 WP02: the disk
// cache holds the envelope bytes as received and is re-verified on Start.
// Real files in a real temp dir throughout (CLAUDE.md blind spot #2).

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// startOffline starts p with no client (the first poll is ErrFleetDisabled),
// so Status reflects only what Start restored from disk.
func startOffline(t *testing.T, p *ConfigPoller) ConfigPollStatus {
	t.Helper()
	p.client = nil
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	p.Start(ctx)
	st := p.Status()
	cancel()
	p.Stop()
	return st
}

func writeCache(t *testing.T, dataDir string, body []byte, id int64) {
	t.Helper()
	if err := saveBundleCache(dataDir, body); err != nil {
		t.Fatal(err)
	}
	if err := saveBundleState(dataDir, id, "stale-checksum"); err != nil {
		t.Fatal(err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Persist the envelope, restart, re-verify, restore.
func TestConfigCache_V2RoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	envelope := readV2Fixture(t, "b_unknown_fields.json")
	fake := &fakeEnvelopeFleet{envelope: envelope}
	p := newV2Poller(t, fake, &fakeApplier{}, dataDir)
	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	onDisk, err := os.ReadFile(bundleCachePath(dataDir))
	if err != nil {
		t.Fatalf("bundle.json not written: %v", err)
	}
	if !bytes.Equal(onDisk, envelope) {
		t.Fatal("bundle.json is not the envelope bytes as received")
	}
	wantChecksum := p.Status().BundleChecksum

	// Relaunch: no network; the cache alone restores the state.
	p2 := newV2Poller(t, fake, &fakeApplier{}, dataDir)
	p2.SetBuildVersion("") // same (empty) build as the apply → no F1 re-fetch
	st := startOffline(t, p2)
	if st.LastAppliedID != 11 || st.Source != "cache" || st.BundleChecksum != wantChecksum {
		t.Fatalf("restored status = %+v, want id 11 / cache / %s", st, wantChecksum)
	}
}

// A tampered cache fails re-verification and is discarded: nothing restored,
// and the derived state files are gone so the next launch restores nothing
// either.
func TestConfigCache_TamperedEnvelopeDiscarded(t *testing.T) {
	for _, name := range []string{"c_tampered_payload.json", "d_key_id_mismatch.json", "e_unknown_key.json", "g_v1_signature_as_v2.json"} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			writeCache(t, dataDir, readV2Fixture(t, name), 10)
			p := newV2Poller(t, &fakeEnvelopeFleet{}, &fakeApplier{}, dataDir)
			st := startOffline(t, p)
			if st.LastAppliedID != 0 || st.Source != "default-deny" || st.BundleChecksum != "" {
				t.Fatalf("status = %+v, want nothing restored", st)
			}
			for _, path := range []string{bundleCachePath(dataDir), bundleIDPath(dataDir), bundleChecksumPath(dataDir)} {
				if fileExists(path) {
					t.Errorf("%s survived a failed re-verification", filepath.Base(path))
				}
			}
		})
	}
}

// A byte flipped on disk after a real pull is caught too.
func TestConfigCache_ByteFlipOnDiskDiscarded(t *testing.T) {
	dataDir := t.TempDir()
	p := newV2Poller(t, &fakeEnvelopeFleet{envelope: readV2Fixture(t, "a_valid.json")}, &fakeApplier{}, dataDir)
	if err := p.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(bundleCachePath(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	// Flip a character inside the signature value.
	i := bytes.Index(raw, []byte(`"signature":"`)) + len(`"signature":"`) + 4
	if raw[i] == 'A' {
		raw[i] = 'B'
	} else {
		raw[i] = 'A'
	}
	if err := os.WriteFile(bundleCachePath(dataDir), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st := startOffline(t, newV2Poller(t, &fakeEnvelopeFleet{}, &fakeApplier{}, dataDir))
	if st.LastAppliedID != 0 || st.Source != "default-deny" {
		t.Fatalf("status = %+v, want the flipped cache discarded", st)
	}
}

// An old-format (legacy) cache is read through the legacy decoder once,
// then replaced by the envelope on the next successful v2 pull.
func TestConfigCache_LegacyCacheReadOnceThenReplaced(t *testing.T) {
	dataDir := t.TempDir()
	legacy := readV2Fixture(t, "f_legacy.json") // bundle 14
	writeCache(t, dataDir, legacy, 14)

	h := readV2Fixture(t, "h_min_client_versions.json") // bundle 15
	fake := &fakeEnvelopeFleet{envelope: h}
	p := newV2Poller(t, fake, &fakeApplier{}, dataDir)
	st := startOffline(t, p)
	if st.LastAppliedID != 14 || st.Source != "cache" || st.BundleChecksum != hexChecksumOf(legacy) {
		t.Fatalf("legacy cache restore = %+v", st)
	}

	p2 := newV2Poller(t, fake, &fakeApplier{}, dataDir)
	if err := p2.poll(context.Background()); err != nil {
		t.Fatalf("v2 pull after legacy cache: %v", err)
	}
	onDisk, _ := os.ReadFile(bundleCachePath(dataDir))
	if !bytes.Equal(onDisk, h) {
		t.Fatal("legacy bundle.json was not replaced by the v2 envelope")
	}
}

// A legacy cache that does not verify on the legacy path is discarded.
func TestConfigCache_LegacyCacheFailingVerifyDiscarded(t *testing.T) {
	dataDir := t.TempDir()
	writeCache(t, dataDir, readV2Fixture(t, "b_unknown_fields_legacy.json"), 11)
	st := startOffline(t, newV2Poller(t, &fakeEnvelopeFleet{}, &fakeApplier{}, dataDir))
	if st.LastAppliedID != 0 || fileExists(bundleCachePath(dataDir)) {
		t.Fatalf("status = %+v; unverifiable legacy cache must be discarded", st)
	}
}

// Garbage on disk is a corrupt cache.
func TestConfigCache_GarbageDiscarded(t *testing.T) {
	dataDir := t.TempDir()
	writeCache(t, dataDir, []byte("not json"), 3)
	id, cs, vb, err := restoreBundleCache(dataDir, v2FixtureKeys())
	if err != nil || id != 0 || cs != "" || vb != nil {
		t.Fatalf("restore = %d %q %v %v, want nothing", id, cs, vb, err)
	}
	if fileExists(bundleIDPath(dataDir)) {
		t.Error("bundle_id.txt survived a corrupt cache")
	}
}

// A partial apply writes no cache (only a fully applied bundle is cached).
func TestConfigCache_PartialApplyWritesNoCache(t *testing.T) {
	dataDir := t.TempDir()
	applier := &fakeApplier{errFn: func(*Bundle) error { return errors.New("section failed") }}
	p := newV2Poller(t, &fakeEnvelopeFleet{envelope: readV2Fixture(t, "a_valid.json")}, applier, dataDir)
	_ = p.poll(context.Background())
	if fileExists(bundleCachePath(dataDir)) {
		t.Fatal("bundle.json written for a partially applied bundle")
	}
}
