package trust_test

// engine-publication-01ENPUB01 WP-H2: SeedAnchor — the baked-release-key
// boot seed — yields to every existing row. Persistence assertions drive
// real sqlite (CLAUDE.md blind spot #2): "revoked stays revoked across
// boots" is meaningless against an in-memory store that forgets.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/trust"
)

func seedTestAnchor(t *testing.T, id string) trust.Anchor {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return trust.Anchor{
		AnchorID:    id,
		Kind:        trust.AnchorRawPublicKey,
		Algorithm:   trust.AlgEd25519,
		PublicKey:   trust.PublicKey{Algorithm: trust.AlgEd25519, Bytes: pub, Fingerprint: trust.ComputeFingerprint(pub)},
		InstalledBy: "system:baked",
		Metadata:    map[string]string{"origin": "baked_release_key"},
	}
}

// openSQLiteEngine opens (or reopens) the data.db in dir and returns an
// engine over a FRESH sqlite AnchorStore bound to it — one "boot".
func openSQLiteEngine(t *testing.T, dir string) (trust.TrustEngine, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := storagesqlite.Open(testStorageConfig(dir))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	store, err := trust.NewSQLiteAnchorStore(rawSQL(t, db))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := trust.NewEngineWithEmitter(trust.Config{Anchors: store}, nil, trust.NewMemoryEmitter())
	if err != nil {
		t.Fatal(err)
	}
	return eng, func() { _ = db.Close(ctx) }
}

func listByID(t *testing.T, eng trust.TrustEngine) map[string]trust.Anchor {
	t.Helper()
	as, err := eng.ListAnchors(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]trust.Anchor{}
	for _, a := range as {
		out[a.AnchorID] = a
	}
	return out
}

func mustSeed(t *testing.T, eng trust.TrustEngine, a trust.Anchor, want trust.SeedOutcome) {
	t.Helper()
	got, err := trust.SeedAnchor(context.Background(), eng, a)
	if err != nil {
		t.Fatalf("SeedAnchor: %v", err)
	}
	if got != want {
		t.Fatalf("SeedAnchor outcome = %q, want %q", got, want)
	}
}

func TestSeedAnchor_FreshStoreSeedsOnceAndPersists(t *testing.T) {
	dir := t.TempDir()
	a := seedTestAnchor(t, "kameas-ml-release-x")

	eng, closeDB := openSQLiteEngine(t, dir)
	mustSeed(t, eng, a, trust.SeedInstalled)
	mustSeed(t, eng, a, trust.SeedAlreadyPresent) // idempotent within a boot
	closeDB()

	eng2, closeDB2 := openSQLiteEngine(t, dir)
	defer closeDB2()
	got, ok := listByID(t, eng2)[a.AnchorID]
	if !ok {
		t.Fatal("seeded anchor did not survive a reopen")
	}
	if got.Metadata["origin"] != "baked_release_key" || got.InstalledBy != "system:baked" {
		t.Fatalf("seeded anchor lost its origin marking: %+v", got)
	}
	mustSeed(t, eng2, a, trust.SeedAlreadyPresent) // next boot: no rewrite
}

func TestSeedAnchor_RevokedStaysRevokedAcrossBoots(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a := seedTestAnchor(t, "kameas-ml-release-y")

	eng, closeDB := openSQLiteEngine(t, dir)
	mustSeed(t, eng, a, trust.SeedInstalled)
	if err := eng.RemoveAnchor(ctx, a.AnchorID); err != nil {
		t.Fatal(err)
	}
	closeDB()

	// Next boot re-runs the seed: the tombstone must win.
	eng2, closeDB2 := openSQLiteEngine(t, dir)
	defer closeDB2()
	mustSeed(t, eng2, a, trust.SeedRevoked)
	if _, live := listByID(t, eng2)[a.AnchorID]; live {
		t.Fatal("boot seeding resurrected a revoked anchor")
	}
	// Same key re-presented under a different id is still revoked.
	other := a
	other.AnchorID = "some-other-id"
	mustSeed(t, eng2, other, trust.SeedRevoked)
	if len(listByID(t, eng2)) != 0 {
		t.Fatal("a revoked key must not be re-seeded under any id")
	}
}

func TestSeedAnchor_OperatorAnchorsUntouched(t *testing.T) {
	ctx := context.Background()
	eng, closeDB := openSQLiteEngine(t, t.TempDir())
	defer closeDB()

	baked := seedTestAnchor(t, "kameas-ml-release-z")

	// (a) operator/fleet already installed the SAME key under its own id.
	op := baked
	op.AnchorID = "fleet-release-key"
	op.InstalledBy = "operator"
	op.Metadata = nil
	if err := eng.InstallAnchor(ctx, op); err != nil {
		t.Fatal(err)
	}
	mustSeed(t, eng, baked, trust.SeedAlreadyPresent)
	got := listByID(t, eng)
	if len(got) != 1 || got["fleet-release-key"].InstalledBy != "operator" {
		t.Fatalf("operator anchor disturbed or duplicate seeded: %+v", got)
	}

	// (b) operator anchor occupies the baked id with a DIFFERENT key.
	eng2, closeDB2 := openSQLiteEngine(t, t.TempDir())
	defer closeDB2()
	squatter := seedTestAnchor(t, baked.AnchorID)
	squatter.InstalledBy = "operator"
	if err := eng2.InstallAnchor(ctx, squatter); err != nil {
		t.Fatal(err)
	}
	mustSeed(t, eng2, baked, trust.SeedIDTaken)
	if got := listByID(t, eng2)[baked.AnchorID]; got.PublicKey.Fingerprint != squatter.PublicKey.Fingerprint {
		t.Fatal("seed overwrote an operator anchor's key")
	}
}

func TestSeedAnchor_RejectsInconsistentAnchorAndForeignEngines(t *testing.T) {
	eng, err := trust.NewEngine(trust.Config{})
	if err != nil {
		t.Fatal(err)
	}
	a := seedTestAnchor(t, "x")
	a.PublicKey.Fingerprint = "deadbeef"
	if _, err := trust.SeedAnchor(context.Background(), eng, a); err == nil {
		t.Fatal("fingerprint/key mismatch accepted")
	}
	if _, err := trust.SeedAnchor(context.Background(), foreignEngine{}, seedTestAnchor(t, "y")); !errors.Is(err, trust.ErrSeedUnsupported) {
		t.Fatalf("foreign engine err = %v", err)
	}
}

type foreignEngine struct{ trust.TrustEngine }
