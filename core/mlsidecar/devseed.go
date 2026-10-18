package mlsidecar

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// ErrDeveloperBuildsRefused is returned by SeedDeveloperBuild for a Layout
// that does not accept developer builds: only the dev engine root
// (Layout.DeveloperBuilds, set by the production wiring for
// KENAZ_HARNESS_ENV=dev) ever adopts one, so seeding anywhere else would
// write a record no client could use.
var ErrDeveloperBuildsRefused = errors.New("mlsidecar: this install root does not accept developer builds")

// SeedRequest describes one developer seed (SeedDeveloperBuild).
type SeedRequest struct {
	// Onedir is the kameas-ml onedir to install: a directory holding the
	// launcher (EngineExecutableName), _internal/ and a VERSION file — what
	// `make freeze` in kenaz-ml leaves at dist/kameas-ml, or what the CI
	// "Frozen bundle" artifact unpacks to.
	Onedir string
	// Version overrides the onedir's VERSION file. Optional.
	Version string
	// Source is recorded verbatim in install.json (e.g. "local:<path>" or
	// "gh-artifact:<run id>") so a later reader can tell where the bytes
	// came from. Optional.
	Source string
	// Note is carried in install.json's Verification field. Optional.
	Note string
}

// SeedResult reports what SeedDeveloperBuild did.
type SeedResult struct {
	Record InstallRecord
	// AlreadyCurrent is true when the root already described this exact
	// tree and nothing was changed.
	AlreadyCurrent bool
}

// SeedDeveloperBuild installs a locally built (or locally fetched) engine
// onedir into layout as a ProvenanceDeveloperBuild record, so the
// Manager's ordinary adopt-or-spawn path launches it exactly as it would
// a released engine. It is the developer-machine stand-in for Install():
// same version directory layout, same whole-tree digest, same
// spawn-locked `current` + install.json flip (M1) — but no download and
// no signature, which is why the record carries a provenance only the
// dev root accepts.
//
// Idempotent: if install.json already describes a developer build whose
// tree digest equals the source onedir's, nothing is written and
// AlreadyCurrent is reported. Fails honestly with ErrSpawnInProgress
// (wrapped) when another client holds the spawn lock for the whole
// bounded wait, rather than flipping underneath it.
func SeedDeveloperBuild(layout Layout, req SeedRequest) (SeedResult, error) {
	if !layout.DeveloperBuilds {
		return SeedResult{}, ErrDeveloperBuildsRefused
	}
	onedir, err := filepath.Abs(req.Onedir)
	if err != nil {
		return SeedResult{}, fmt.Errorf("mlsidecar: resolve onedir: %w", err)
	}
	if err := checkOnedir(onedir); err != nil {
		return SeedResult{}, err
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		b, rerr := os.ReadFile(filepath.Join(onedir, "VERSION"))
		if rerr != nil {
			return SeedResult{}, fmt.Errorf("mlsidecar: onedir has no VERSION file and no --version was given: %w", rerr)
		}
		version = strings.TrimSpace(string(b))
	}
	if version == "" || strings.ContainsAny(version, `/\`) || strings.HasPrefix(version, ".") {
		return SeedResult{}, fmt.Errorf("mlsidecar: %q is not a usable version label", version)
	}

	// Digest the SOURCE first: it decides idempotency, and it is also what
	// the installed copy must re-hash to, so a copy error shows up as a
	// digest mismatch below rather than being recorded.
	srcTree, err := TreeDigest(onedir)
	if err != nil {
		return SeedResult{}, err
	}
	if rec, ok, rerr := ReadInstallJSON(layout); rerr == nil && ok &&
		rec.Provenance == ProvenanceDeveloperBuild && rec.Version == version && digestsEqual(rec.TreeSHA256, srcTree) {
		if _, verr := VerifyInstalled(layout, version, nil); verr == nil {
			return SeedResult{Record: rec, AlreadyCurrent: true}, nil
		}
	}

	if err := layout.EnsureDirs(); err != nil {
		return SeedResult{}, err
	}
	versionDir := layout.VersionDir(version)
	// Stage next to the final location and rename in, so a half-copied
	// tree is never what `current` can point at.
	staging := versionDir + ".seeding"
	_ = os.RemoveAll(staging)
	if err := copyTree(onedir, filepath.Join(staging, "kameas-ml")); err != nil {
		_ = os.RemoveAll(staging)
		return SeedResult{}, fmt.Errorf("mlsidecar: copy onedir: %w", err)
	}
	if err := clearQuarantine(staging); err != nil {
		_ = os.RemoveAll(staging)
		return SeedResult{}, fmt.Errorf("mlsidecar: clear quarantine: %w", err)
	}
	installedTree, err := TreeDigest(filepath.Join(staging, "kameas-ml"))
	if err != nil {
		_ = os.RemoveAll(staging)
		return SeedResult{}, err
	}
	if !digestsEqual(installedTree, srcTree) {
		_ = os.RemoveAll(staging)
		return SeedResult{}, fmt.Errorf("mlsidecar: copied onedir digest %s != source %s", installedTree, srcTree)
	}
	exeDigest, err := HashFileSHA256(filepath.Join(staging, "kameas-ml", EngineExecutableName("")))
	if err != nil {
		_ = os.RemoveAll(staging)
		return SeedResult{}, fmt.Errorf("mlsidecar: hash launcher: %w", err)
	}

	// The same M1 discipline Install() follows: `current` and install.json
	// change together under the spawn lock, so the other client can never
	// observe one without the other.
	flipLock, lockErr := acquireSpawnLockBounded(layout, flipLockAttempts, flipLockPause)
	if lockErr != nil {
		_ = os.RemoveAll(staging)
		return SeedResult{}, fmt.Errorf("mlsidecar: seed flip: %w", lockErr)
	}
	defer flipLock.Release()

	if err := os.RemoveAll(versionDir); err != nil {
		_ = os.RemoveAll(staging)
		return SeedResult{}, fmt.Errorf("mlsidecar: clear previous %s: %w", versionDir, err)
	}
	if err := os.Rename(staging, versionDir); err != nil {
		_ = os.RemoveAll(staging)
		return SeedResult{}, fmt.Errorf("mlsidecar: move seeded version into place: %w", err)
	}
	if err := layout.SetCurrent(version); err != nil {
		return SeedResult{}, err
	}
	source := req.Source
	if source == "" {
		source = "local:" + onedir
	}
	rec := InstallRecord{
		Version:      version,
		EngineSHA256: exeDigest,
		Source:       source,
		InstalledAt:  time.Now(),
		Verified:     false, // no signature was checked; never claim one
		TreeSHA256:   installedTree,
		Provenance:   ProvenanceDeveloperBuild,
		InstalledBy:  "harness-dev",
		Verification: strings.TrimSpace("developer build; tree digest computed at seed time. " + req.Note),
	}
	if err := WriteInstallJSON(layout, rec); err != nil {
		return SeedResult{}, err
	}
	logging.L().Info("mlsidecar.devseed.done", "version", version, "root", layout.Root, "source", source)
	return SeedResult{Record: rec}, nil
}

// checkOnedir refuses anything that is not a complete kameas-ml onedir
// before any copying starts.
func checkOnedir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("mlsidecar: %q is not a directory", dir)
	}
	exe := filepath.Join(dir, EngineExecutableName(""))
	if fi, err := os.Stat(exe); err != nil || fi.IsDir() {
		return fmt.Errorf("mlsidecar: %q has no %s launcher", dir, EngineExecutableName(""))
	}
	if fi, err := os.Stat(filepath.Join(dir, "_internal")); err != nil || !fi.IsDir() {
		return fmt.Errorf("mlsidecar: %q has no _internal/ (not a PyInstaller onedir)", dir)
	}
	return nil
}
