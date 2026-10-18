package mlsidecar

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeFakeOnedir lays down the minimum a kameas-ml onedir needs to seed:
// a launcher, an _internal/ payload and a VERSION file.
func writeFakeOnedir(t *testing.T, dir, version, payload string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "_internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		EngineExecutableName(""):                 "#!/bin/sh\necho " + version + "\n",
		"VERSION":                                version + "\n",
		filepath.Join("_internal", "payload.so"): payload,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSeedDeveloperBuild_InstallsAnAdoptableRecordInTheDevRoot(t *testing.T) {
	src := filepath.Join(t.TempDir(), "dist", "kameas-ml")
	writeFakeOnedir(t, src, "0.1.0", "payload-a")
	layout := NewLayout(t.TempDir())
	layout.DeveloperBuilds = true

	res, err := SeedDeveloperBuild(layout, SeedRequest{Onedir: src, Note: "unit test"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if res.AlreadyCurrent {
		t.Fatal("first seed reported AlreadyCurrent")
	}
	if res.Record.Provenance != ProvenanceDeveloperBuild || res.Record.Verified {
		t.Fatalf("record must be an unverified developer build, got %+v", res.Record)
	}
	// The ordinary adoption rule accepts it in this root, by the same
	// on-disk re-verification a released engine gets.
	rec, err := VerifyInstalled(layout, "0.1.0", nil)
	if err != nil {
		t.Fatalf("VerifyInstalled: %v", err)
	}
	if !recordAdoptable(layout, rec) {
		t.Fatal("record is not adoptable in the dev root")
	}
	cur, err := layout.CurrentVersionDir()
	if err != nil || filepath.Base(cur) != "0.1.0" {
		t.Fatalf("current -> %q, %v", cur, err)
	}
	if _, err := os.Stat(layout.VersionDir("0.1.0") + ".seeding"); !os.IsNotExist(err) {
		t.Fatal("staging directory left behind")
	}

	// Same bytes again: nothing changes.
	again, err := SeedDeveloperBuild(layout, SeedRequest{Onedir: src})
	if err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if !again.AlreadyCurrent {
		t.Fatal("re-seeding identical bytes must report AlreadyCurrent")
	}

	// Changed bytes under the same version label: re-seeded, new digest.
	writeFakeOnedir(t, src, "0.1.0", "payload-b")
	changed, err := SeedDeveloperBuild(layout, SeedRequest{Onedir: src})
	if err != nil {
		t.Fatalf("re-seed changed: %v", err)
	}
	if changed.AlreadyCurrent || digestsEqual(changed.Record.TreeSHA256, res.Record.TreeSHA256) {
		t.Fatal("a changed onedir must produce a new record")
	}
	if _, err := VerifyInstalled(layout, "0.1.0", nil); err != nil {
		t.Fatalf("VerifyInstalled after re-seed: %v", err)
	}
}

// Pin (fail-closed outside dev): the identical install.json on a root
// that does not accept developer builds is refused everywhere a record is
// keyed — VerifyInstalled, recordAdoptable and Manager.Installed — and
// seeding into such a root is refused up front.
func TestSeedDeveloperBuild_RefusedOutsideTheDevRoot(t *testing.T) {
	src := filepath.Join(t.TempDir(), "kameas-ml")
	writeFakeOnedir(t, src, "0.1.0", "payload")

	prod := NewLayout(t.TempDir())
	if _, err := SeedDeveloperBuild(prod, SeedRequest{Onedir: src}); !errors.Is(err, ErrDeveloperBuildsRefused) {
		t.Fatalf("seeding a non-dev root must be refused, got %v", err)
	}

	// Plant the record a dev seed would have written, then read it as prod.
	dev := NewLayout(t.TempDir())
	dev.DeveloperBuilds = true
	if _, err := SeedDeveloperBuild(dev, SeedRequest{Onedir: src}); err != nil {
		t.Fatal(err)
	}
	asProd := NewLayout(dev.Root) // same bytes on disk, DeveloperBuilds unset
	if _, err := VerifyInstalled(asProd, "0.1.0", nil); err == nil {
		t.Fatal("VerifyInstalled accepted a developer build on a non-dev root")
	}
	rec, ok, err := ReadInstallJSON(asProd)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if recordAdoptable(asProd, rec) {
		t.Fatal("recordAdoptable accepted a developer build on a non-dev root")
	}
	m := NewManager(asProd, nil, nil, "harness", "test")
	if _, installed := m.Installed(); installed {
		t.Fatal("Manager.Installed reported a developer build on a non-dev root")
	}
	// ...and the same root with the flag set does accept it, so the pin is
	// about the flag, not about the bytes.
	if _, err := VerifyInstalled(dev, "0.1.0", nil); err != nil {
		t.Fatalf("dev root must accept its own seed: %v", err)
	}
}

func TestSeedDeveloperBuild_RefusesAnIncompleteOnedir(t *testing.T) {
	layout := NewLayout(t.TempDir())
	layout.DeveloperBuilds = true
	bare := t.TempDir()
	if err := os.WriteFile(filepath.Join(bare, EngineExecutableName("")), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := SeedDeveloperBuild(layout, SeedRequest{Onedir: bare}); err == nil {
		t.Fatal("an onedir without _internal/ must be refused")
	}
	if _, err := os.Stat(layout.InstallJSONPath()); !os.IsNotExist(err) {
		t.Fatal("a refused seed must leave no install.json")
	}
}

func TestSeedDeveloperBuild_FailsHonestlyWhileAnotherClientHoldsTheSpawnLock(t *testing.T) {
	src := filepath.Join(t.TempDir(), "kameas-ml")
	writeFakeOnedir(t, src, "0.1.0", "payload")
	layout := NewLayout(t.TempDir())
	layout.DeveloperBuilds = true
	if err := layout.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	held, err := AcquireSpawnLock(layout, os.Getpid(), processAlive)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	oldAttempts, oldPause := flipLockAttempts, flipLockPause
	flipLockAttempts, flipLockPause = 2, 0
	defer func() { flipLockAttempts, flipLockPause = oldAttempts, oldPause }()

	_, err = SeedDeveloperBuild(layout, SeedRequest{Onedir: src})
	if !errors.Is(err, ErrSpawnInProgress) {
		t.Fatalf("expected ErrSpawnInProgress while the lock is held, got %v", err)
	}
	if _, serr := os.Stat(layout.InstallJSONPath()); !os.IsNotExist(serr) {
		t.Fatal("a refused flip must leave no install.json")
	}
	if _, serr := os.Stat(layout.VersionDir("0.1.0") + ".seeding"); !os.IsNotExist(serr) {
		t.Fatal("a refused flip must leave no staging directory")
	}
}
