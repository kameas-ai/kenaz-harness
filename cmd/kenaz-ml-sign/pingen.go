package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
)

// cmdPinGen (WP-H3) rewrites core/mlsidecar/pinned_release_gen.go — the
// harness's build-time engine pin — from the published engine release:
//
//	pin-gen --version 1.2.0 \
//	        --channel-url https://downloads.kameas.ai/kenaz-ml/1.2.0 \
//	        --artifact-path kenaz-ml-1.2.0-darwin-arm64.dmg \
//	        --sha256 sha256:<hex> --size <bytes> --key-id <hex> [--out FILE]
//	pin-gen --zero [--out FILE]     # back to "nothing pinned"
//
// Channel kind is always http_mirror and the signature locator is always
// "<artifact-path>.sig" (the publish contract). Every field is validated
// by mlsidecar.NewHTTPMirrorPin, so a bad input fails the build.
func cmdPinGen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pin-gen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("version", "", "engine semver")
	channelURL := fs.String("channel-url", "", "https://<env downloads domain>/kenaz-ml/<version>")
	artifactPath := fs.String("artifact-path", "", "bare .dmg filename in the channel directory")
	sha := fs.String("sha256", "", "sha256:<lowercase hex> of the .dmg")
	size := fs.Int64("size", 0, ".dmg size in bytes (disclosed to the user before download)")
	keyID := fs.String("key-id", "", "KeyID of the release signing key (64 lowercase hex)")
	zero := fs.Bool("zero", false, "write the zero-value pin (no engine release)")
	out := fs.String("out", mlsidecar.PinnedReleaseGenFile, "file to write (run from the repo root)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	var rel mlsidecar.EngineRelease
	if *zero {
		if *version != "" || *channelURL != "" || *artifactPath != "" || *sha != "" || *size != 0 || *keyID != "" {
			fmt.Fprintln(stderr, "pin-gen: --zero takes no release flags")
			return exitUsage
		}
	} else {
		r, err := mlsidecar.NewHTTPMirrorPin(*version, *channelURL, *artifactPath, *sha, *size, *keyID)
		if err != nil {
			fmt.Fprintf(stderr, "pin-gen: %v\n", err)
			return exitUsage
		}
		rel = r
	}
	src, err := mlsidecar.RenderPinnedReleaseSource(rel)
	if err != nil {
		fmt.Fprintf(stderr, "pin-gen: render: %v\n", err)
		return exitInvalid
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		fmt.Fprintf(stderr, "pin-gen: write %s: %v\n", *out, err)
		return exitInvalid
	}
	if rel.Version == "" {
		fmt.Fprintf(stdout, "pin-gen: wrote zero-value pin to %s\n", *out)
	} else {
		fmt.Fprintf(stdout, "pin-gen: pinned kenaz-ml %s (%s, %d bytes, key %s) in %s\n", rel.Version, rel.ArtifactPath, rel.SizeBytes, *keyID, *out)
	}
	return exitOK
}
