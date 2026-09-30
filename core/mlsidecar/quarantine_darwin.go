//go:build darwin

package mlsidecar

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// quarantineAttr is the macOS extended attribute Gatekeeper stamps on
// downloaded files (design F3/§3.7 R3): "the installer, after A-1
// signature + sha256 verification, removes com.apple.quarantine
// recursively on the unpacked onedir before first spawn ... the removal
// is idempotent and post-verification, so it weakens nothing."
const quarantineAttr = "com.apple.quarantine"

// clearQuarantine recursively removes the com.apple.quarantine xattr
// from every file and directory under root. Removal happens ENTIRELY IN
// GO (no shelling out to /usr/bin/xattr, so there is no dependency on
// that binary's presence or PATH), and is idempotent: a file with no
// quarantine attribute is left untouched, not treated as an error.
//
// Callers MUST invoke this only AFTER VerifyEngineArtifact has already
// passed (install.go enforces the ordering) — removing quarantine
// BEFORE verification would let a corrupted or malicious unpacked
// artifact defeat Gatekeeper before this client has even checked its
// signature or digest.
func clearQuarantine(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rerr := unix.Removexattr(path, quarantineAttr)
		if rerr != nil && !errors.Is(rerr, unix.ENOATTR) && !errors.Is(rerr, unix.ENOENT) {
			return fmt.Errorf("mlsidecar: remove quarantine xattr on %s: %w", path, rerr)
		}
		return nil
	})
}

// quarantineAttrPresent reports whether path currently carries the
// quarantine xattr. Used only by tests to prove clearQuarantine actually
// did something, not by production logic (which is idempotent by
// design and does not need to know the prior state).
func quarantineAttrPresent(path string) (bool, error) {
	// A real quarantine value is well under 256 bytes; ERANGE (buffer too
	// small) would incorrectly read as "absent" or as an error if the
	// buffer were sized to fit only a probe byte, so this sizes generously
	// rather than doing a two-call size-then-read dance for a test helper.
	buf := make([]byte, 512)
	_, err := unix.Getxattr(path, quarantineAttr, buf)
	if err != nil {
		if errors.Is(err, unix.ENOATTR) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// setQuarantineAttrForTest stamps path with a quarantine xattr so tests
// can exercise clearQuarantine against a realistic starting state. Test
// helper only (unused build warnings are avoided because it is called
// from quarantine_darwin_test.go, which shares this build tag).
func setQuarantineAttrForTest(path string) error {
	// A real quarantine value looks like
	// "0081;<timestamp-hex>;Safari;<uuid>" — the exact value never
	// matters to clearQuarantine, which removes the attribute
	// unconditionally.
	value := []byte("0081;00000000;kenaz-harness-test;00000000-0000-0000-0000-000000000000")
	return unix.Setxattr(path, quarantineAttr, value, 0)
}
