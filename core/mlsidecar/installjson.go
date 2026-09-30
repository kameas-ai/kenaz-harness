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
type InstallRecord struct {
	Version      string    `json:"version"`
	EngineSHA256 string    `json:"engine_sha256"`
	Source       string    `json:"source"` // "<channel kind>:<locator>"
	InstalledAt  time.Time `json:"installed_at"`
	Verified     bool      `json:"verified"` // a real positive VerifyManifestSignatures result, never ref-presence (mirrors bundle's lockfile.Verified discipline)
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
	return filepath.Join(l.VersionDir(semver), "kameas-ml", EngineExecutableName(""))
}
