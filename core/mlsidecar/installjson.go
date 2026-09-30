package mlsidecar

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// InstallRecord is install.json's shape (design §3.4: "who installed
// what, when, from which manifest"). It is the client-side memory of
// which artifact digest was verified at install time — VERIFIED
// adoption (adopt.go) re-verifies the ON-DISK bytes against this record
// rather than trusting a running process's self-report.
//
// install.json is SHARED with Kenaz (design §3.4 + Amendment A5): both
// clients read and write this one file, so the field names below are a
// cross-repo interface identical to Kenaz's internal/ml InstallRecord.
//
// Acceptance (A5(4), cross-client trust) is Provenance + TreeSHA256, NOT
// Verified: a record is adoptable when its provenance is one of the two
// known values AND the on-disk tree matches TreeSHA256 AND Version names
// exactly the directory `current` points to (VerifyInstalled). Verified
// stays an honest "signature-verified by the harness's A-1 path" flag —
// neither required (a Kenaz seed never sets it) nor sufficient (a record
// without a known provenance and a tree digest is refused even with it).
type InstallRecord struct {
	Version      string    `json:"version"`       // versions/<label> directory name
	EngineSHA256 string    `json:"engine_sha256"` // "sha256:<hex>" of the launcher — the cheap cross-check
	Source       string    `json:"source"`        // "<channel kind>:<locator>"
	InstalledAt  time.Time `json:"installed_at"`
	Verified     bool      `json:"verified"` // a real positive VerifyManifestSignatures result, never ref-presence (mirrors bundle's lockfile.Verified discipline)

	// Design Amendment A5 fields (shared with Kenaz).
	TreeSHA256   string `json:"tree_sha256,omitempty"`  // whole-onedir digest (tree.go)
	Provenance   string `json:"provenance,omitempty"`   // ProvenanceChannelManifest | ProvenanceKenazBundle
	InstalledBy  string `json:"installed_by,omitempty"` // "harness" | "kenaz"
	Verification string `json:"verification,omitempty"` // Kenaz's note on how its digest was established; carried, not interpreted
}

// Provenance values (design Amendment A5(4)).
const (
	// ProvenanceChannelManifest: the harness installed the engine through
	// its A-1 channel-manifest path (signature-verified artifact).
	ProvenanceChannelManifest = "kameas-channel-manifest"
	// ProvenanceKenazBundle: Kenaz seeded the engine from the bytes shipped
	// inside its own code-signed app bundle (digest pinning).
	ProvenanceKenazBundle = "kenaz-bundle-digest"
)

func knownProvenance(p string) bool {
	return p == ProvenanceChannelManifest || p == ProvenanceKenazBundle
}

// ReadInstallJSON reads and parses install.json. A missing file is not
// an error — a fresh install root has none yet — and returns the zero
// InstallRecord with ok=false.
func ReadInstallJSON(l Layout) (InstallRecord, bool, error) {
	b, err := os.ReadFile(l.InstallJSONPath())
	if err != nil {
		if os.IsNotExist(err) {
			return InstallRecord{}, false, nil
		}
		return InstallRecord{}, false, fmt.Errorf("mlsidecar: read install.json: %w", err)
	}
	var rec InstallRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return InstallRecord{}, false, fmt.Errorf("mlsidecar: parse install.json: %w", err)
	}
	return rec, true, nil
}

// WriteInstallJSON writes rec atomically (tmp + rename), mirroring
// core/update's persistSkipped / core/rpc/views/bundle's WriteLockfile
// discipline — a torn write must never leave a corrupt install.json a
// later adoption attempt could misread as a verified digest.
func WriteInstallJSON(l Layout, rec InstallRecord) error {
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		return fmt.Errorf("mlsidecar: mkdir install root: %w", err)
	}
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("mlsidecar: marshal install.json: %w", err)
	}
	tmp, err := os.CreateTemp(l.Root, ".install.json.tmp-*")
	if err != nil {
		return fmt.Errorf("mlsidecar: create tmp install.json: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("mlsidecar: write tmp install.json: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("mlsidecar: close tmp install.json: %w", err)
	}
	if err := os.Rename(tmpPath, l.InstallJSONPath()); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("mlsidecar: rename install.json: %w", err)
	}
	return nil
}

// pathUnderVersionsDir joins a version's directory with the engine
// executable's relative in-onedir path.
func pathUnderVersionsDir(l Layout, semver string) string {
	return filepath.Join(l.OnedirPath(semver), EngineExecutableName(""))
}

// OnedirPath is a version's kameas-ml onedir — the tree TreeSHA256 covers.
func (l Layout) OnedirPath(label string) string {
	return filepath.Join(l.VersionDir(label), "kameas-ml")
}
