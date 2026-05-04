package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
)

// errSha256Mismatch is returned when the streamed (or on-disk) sha256
// digest does not match the value advertised in the manifest. Caller
// hits this on a corrupted CDN object, an MITM, or a stale staged
// file. Either way, the swap MUST NOT proceed.
var errSha256Mismatch = errors.New("update: sha256 mismatch")

// errManifestNotFound is the sentinel for HTTP 404 on a manifest
// fetch. The Service.Check path uses errors.Is to detect a 404 and
// fall back from prerelease to stable.
var errManifestNotFound = errors.New("update: manifest not found")

// sha256Writer is an io.Writer that maintains a running sha256
// digest. Used during Service.Download so the file isn't rehashed
// after staging.
type sha256Writer struct {
	h hash.Hash
}

func newSha256Writer() *sha256Writer { return &sha256Writer{h: sha256.New()} }

func (w *sha256Writer) Write(p []byte) (int, error) { return w.h.Write(p) }

func (w *sha256Writer) Sum() string { return hex.EncodeToString(w.h.Sum(nil)) }

// verifyFileSha256 hashes the file at path and returns errSha256Mismatch
// if the computed digest doesn't match expected (case-insensitive). A
// missing file or read error returns the wrapped underlying error.
//
// The expected digest is matched lowercase; manifest authors who
// uppercase by accident don't break the build.
func verifyFileSha256(path, expected string) error {
	if expected == "" {
		return fmt.Errorf("update: empty expected sha256")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("update: open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("update: hash %s: %w", path, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("%w: got %s want %s", errSha256Mismatch, got, expected)
	}
	return nil
}
