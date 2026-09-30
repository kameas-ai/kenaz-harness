package mlsidecar

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// InstallRequest describes one engine-install attempt (design §6.2 step
// 3: "fetch the engine via core/bundle A-1 channels ... verify signature
// via core/trust ... before first run, strip quarantine post-verify,
// unpack to versions/, flip current, spawn").
type InstallRequest struct {
	// ChannelKind/ChannelURL/ChannelPath select the core/bundle channel
	// exactly as core/rpc/views/bundle.InstallRequest does — "http_mirror"
	// for the real env-specific S3/CloudFront download channel,
	// "local_path" for tests and for the adopt-first zero-download case
	// this struct is never used for (adoption skips Install entirely).
	ChannelKind string
	ChannelURL  string
	ChannelPath string

	Version        string // semver, becomes the versions/<Version> dir name
	ArtifactPath   string // channel-relative path to the engine zip
	ExpectedSHA256 string // "sha256:<hex>" — the pinned digest from the release manifest
	Signature      *manifest.SignatureRef

	// Source is a human-readable provenance string recorded into
	// install.json (e.g. "http_mirror:https://dev.downloads.kameas.ai/...").
	Source string
}

// InstallResult is what a successful Install produced.
type InstallResult struct {
	VersionDir string
	Record     InstallRecord
}

// Install fetches the engine zip via the given core/bundle channel,
// verifies BEFORE unpacking or running it (signature + sha256, reusing
// core/bundle/integrity + core/trust — "no second verifier"), clears
// macOS quarantine ONLY AFTER verification succeeds, unpacks into
// versions/<Version>/, and rename-swaps `current`. It NEVER execs the
// artifact — spawning is a separate, later step the caller controls
// (design §3.5: "never exec-in-place").
//
// Ordering is load-bearing and matches core/rpc/views/bundle.Install's
// own "refusal leaves no residue" discipline: a failure at any step
// removes the staged zip and leaves neither a partially-unpacked version
// directory nor an install.json record.
func Install(ctx context.Context, layout Layout, registry channels.Registry, creds secrets.ResolverAPI, verifier Verifier, req InstallRequest) (InstallResult, error) {
	if req.Version == "" {
		return InstallResult{}, fmt.Errorf("mlsidecar: install requires a version")
	}
	if err := layout.EnsureDirs(); err != nil {
		return InstallResult{}, err
	}

	spec := channels.ChannelSpec{Kind: req.ChannelKind, URL: req.ChannelURL, Path: req.ChannelPath}
	ch, err := registry.Open(spec, creds)
	if err != nil {
		return InstallResult{}, fmt.Errorf("mlsidecar: open channel: %w", err)
	}
	if err := ch.Reachable(ctx); err != nil {
		return InstallResult{}, fmt.Errorf("mlsidecar: channel unreachable: %w", err)
	}

	stagingDir := filepath.Join(layout.Root, ".staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("mlsidecar: mkdir staging: %w", err)
	}
	zipPath := filepath.Join(stagingDir, req.Version+".zip")
	if err := fetchToFile(ctx, ch, req.ArtifactPath, zipPath); err != nil {
		_ = os.Remove(zipPath)
		return InstallResult{}, fmt.Errorf("mlsidecar: fetch engine artifact: %w", err)
	}

	m := EngineManifest(req.Version, req.ArtifactPath, req.ExpectedSHA256, req.Signature)
	resolve := channelSignatureResolver(ctx, ch)
	verified, err := verifier.VerifyEngineArtifact(ctx, m, zipPath, resolve)
	if err != nil {
		_ = os.Remove(zipPath)
		logging.L().Warn("mlsidecar.install.verify_failed", "version", req.Version, "err", err.Error())
		return InstallResult{}, err
	}

	versionDir := layout.VersionDir(req.Version)
	if err := os.RemoveAll(versionDir); err != nil {
		_ = os.Remove(zipPath)
		return InstallResult{}, fmt.Errorf("mlsidecar: clear stale version dir: %w", err)
	}
	if err := unzipTo(zipPath, versionDir); err != nil {
		_ = os.Remove(zipPath)
		_ = os.RemoveAll(versionDir)
		return InstallResult{}, fmt.Errorf("mlsidecar: unpack engine artifact: %w", err)
	}
	_ = os.Remove(zipPath)

	// design F3/§3.7 R3: quarantine is cleared ONLY after verification
	// (already true by this point in the call sequence) — never before.
	//
	// On failure, remove the unpacked version dir — this function's own
	// doc comment promises a refusal "leaves neither a partially-unpacked
	// version directory nor an install.json record"; a quarantine-clear
	// failure is a refusal like any other verification-adjacent failure
	// before `current` has been touched (SetCurrent hasn't run yet).
	if err := clearQuarantine(versionDir); err != nil {
		_ = os.RemoveAll(versionDir)
		return InstallResult{}, fmt.Errorf("mlsidecar: clear quarantine: %w", err)
	}

	// Record the digest of the ACTUAL UNPACKED EXECUTABLE, not the
	// (now-deleted) zip's digest — this is what EvaluateAdoption re-hashes
	// against at adoption time, since the zip no longer exists to re-hash.
	// Same cleanup discipline: `current` still hasn't been touched here.
	exeDigest, err := HashFileSHA256(pathUnderVersionsDir(layout, req.Version))
	if err != nil {
		_ = os.RemoveAll(versionDir)
		return InstallResult{}, fmt.Errorf("mlsidecar: hash unpacked engine executable: %w", err)
	}

	if err := layout.SetCurrent(req.Version); err != nil {
		return InstallResult{}, err
	}

	rec := InstallRecord{
		Version:      req.Version,
		EngineSHA256: exeDigest,
		Source:       req.Source,
		InstalledAt:  time.Now(),
		Verified:     verified,
	}
	if err := WriteInstallJSON(layout, rec); err != nil {
		return InstallResult{}, err
	}
	logging.L().Info("mlsidecar.install.done", "version", req.Version, "verified", verified)
	return InstallResult{VersionDir: versionDir, Record: rec}, nil
}

func fetchToFile(ctx context.Context, ch channels.Channel, artifactPath, dest string) error {
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create staged file: %w", err)
	}
	_, ferr := ch.Fetch(ctx, channels.ArtifactCoord{Path: artifactPath}, f)
	cerr := f.Close()
	if ferr != nil {
		return ferr
	}
	return cerr
}

// channelSignatureResolver mirrors core/rpc/views/bundle's own resolver
// of the same name: it resolves a manifest SignatureRef.Locator to bytes
// by fetching it through the same channel the artifact came from, so
// this works for any channel kind including one with no local
// filesystem root (http_mirror).
func channelSignatureResolver(ctx context.Context, ch channels.Channel) func(locator string) ([]byte, error) {
	return func(locator string) ([]byte, error) {
		var buf bytes.Buffer
		if _, err := ch.Fetch(ctx, channels.ArtifactCoord{Path: locator}, &buf); err != nil {
			return nil, fmt.Errorf("fetch signature %s: %w", locator, err)
		}
		return buf.Bytes(), nil
	}
}

// unzipTo extracts the zip archive at zipPath into destDir, creating it
// if necessary. Rejects any entry whose cleaned path would escape
// destDir (zip-slip hardening), matching the containment discipline
// core/bundle/channels/localpath applies to filesystem paths.
func unzipTo(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mkdir dest: %w", err)
	}

	for _, f := range r.File {
		targetPath, err := safeJoin(destDir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", targetPath, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("mkdir parent of %s: %w", targetPath, err)
		}
		if err := extractOne(f, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func extractOne(f *zip.File, targetPath string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	defer rc.Close()

	mode := f.Mode()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", targetPath, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("write %s: %w", targetPath, err)
	}
	return nil
}

func safeJoin(base, name string) (string, error) {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("mlsidecar: zip entry %q escapes destination", name)
	}
	return filepath.Join(base, clean), nil
}
