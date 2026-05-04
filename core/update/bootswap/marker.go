package bootswap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// marker mirrors update.pendingMarker. Duplicated here (rather than
// imported) so the bootswap package has no dependency on the larger
// update Service — keeps the boot path tiny and the import graph
// acyclic.
type marker struct {
	TargetPath    string `json:"target_path"`
	StagedPath    string `json:"staged_path"`
	Sha256        string `json:"sha256"`
	TargetVersion string `json:"target_version"`
	Platform      string `json:"platform"`
}

// readMarker loads and validates the marker JSON. (zero, false, nil)
// for missing files; (zero, false, err) for any parse / schema
// failure.
func readMarker(path string) (marker, bool, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return marker{}, false, nil
	}
	if err != nil {
		return marker{}, false, fmt.Errorf("read marker %s: %w", path, err)
	}
	var m marker
	if err := json.Unmarshal(body, &m); err != nil {
		return marker{}, false, fmt.Errorf("decode marker: %w", err)
	}
	if m.TargetPath == "" || m.StagedPath == "" || m.Sha256 == "" {
		return marker{}, false, errors.New("marker missing required fields")
	}
	if _, err := os.Stat(m.StagedPath); err != nil {
		return marker{}, false, fmt.Errorf("staged path %s missing: %w", m.StagedPath, err)
	}
	return m, true, nil
}

// verifyFileSha256 re-hashes the staged file and compares against the
// marker's expected digest. Mirror of update.verifyFileSha256 — the
// duplication is intentional (see comment on `marker`).
func verifyFileSha256(path, expected string) error {
	if expected == "" {
		return errors.New("empty expected sha256")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("sha256 mismatch: got %s want %s", got, expected)
	}
	return nil
}
