// Command kenaz-ml-sign is the publisher half of the Kameas ML engine
// release channel (mission engine-publication-01ENPUB01, WP-H1).
//
// It exists so the bytes the kenaz-ml publish job SIGNS are, by
// construction, the bytes the harness VERIFIES: both sides call
// mlsidecar.EngineManifest(version, artifactPath, sha256, …)
// .SigningPayload() from this one Go module. A signer written in any
// other language would have to re-implement manifest.writeCanonical and
// would drift silently — which is why this tool must be Go.
//
// Subcommands (the flag set is a CONTRACT the kenaz-ml publish job is
// built against — do not rename or reshape without changing that job):
//
//	keygen  --out-dir <dir>
//	    Writes <dir>/kenaz-ml-release.key (private, 0600) and
//	    <dir>/kenaz-ml-release.pub (public, 0644); refuses to overwrite.
//	    Prints the KeyID (lowercase hex SHA-256 of the raw 32-byte public
//	    key — core/trust's fingerprint) on stdout.
//
//	sign    --version <semver> --artifact-path <bare filename>
//	        --sha256 sha256:<lowercase hex> [--key <privkey file>] --out <path>
//	    Signs the canonical SigningPayload and writes the RAW 64-byte
//	    detached ed25519 signature to --out (no PEM, no base64). The key
//	    comes from --key, or — when --key is omitted — from the
//	    KENAZ_ML_RELEASE_SIGNING_KEY environment variable (the GitHub
//	    environment secret on kenaz-ml). Prints one line of JSON on
//	    stdout: {"key_id","version","artifact_path","sha256"}.
//
//	verify  --pubkey <pubkey file> --sig <sig file> --version <semver>
//	        --artifact-path <bare filename> --sha256 sha256:<hex>
//	    The CI self-check. Recomputes the payload and verifies it through
//	    the SAME path the harness install uses (a core/trust engine with
//	    the key installed as an anchor, core/bundle/integrity.
//	    VerifyManifestSignatures under SigningRequired). Exit 0 = valid,
//	    1 = invalid, 2 = usage error.
//
//	pin-gen (WP-H3)
//	    Rewrites core/mlsidecar/pinned_release_gen.go, the harness's
//	    build-time engine pin; see pingen.go.
//
// KEY FILE FORMATS (decided here, WP-H1):
//
//   - Private key: ONE line of standard base64 encoding the 64-byte
//     ed25519 private key (seed ‖ public key, Go's ed25519.PrivateKey
//     layout). The KENAZ_ML_RELEASE_SIGNING_KEY secret holds exactly this
//     text. A base64 32-byte seed is also accepted. Base64 (not PEM, not
//     raw bytes) because the value has to round-trip through a GitHub
//     secret / environment variable intact.
//   - Public key: ONE line of 64 lowercase hex characters (the raw
//     32-byte key) — the same format core/mlsidecar's baked release key
//     file and its -ldflags override use (and core/fleet's
//     FLEET_SIGNING_PUBKEY), so the .pub file can be dropped into the
//     harness build verbatim.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
	"github.com/kameas-ai/kenaz-harness/core/trust"
)

// SigningKeyEnv is the environment variable sign reads the private key
// from when --key is omitted (owner decision 2026-10-03 #1).
const SigningKeyEnv = "KENAZ_ML_RELEASE_SIGNING_KEY"

const (
	privKeyFileName = "kenaz-ml-release.key"
	pubKeyFileName  = "kenaz-ml-release.pub"
)

// exit codes
const (
	exitOK      = 0
	exitInvalid = 1
	exitUsage   = 2
)

var sha256Re = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

// run dispatches a subcommand. Split from main so tests drive the exact
// argv the publish job uses.
func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "keygen":
		return cmdKeygen(args[1:], stdout, stderr)
	case "sign":
		return cmdSign(args[1:], stdout, stderr, getenv)
	case "verify":
		return cmdVerify(args[1:], stdout, stderr)
	case "pin-gen":
		return cmdPinGen(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "kenaz-ml-sign: unknown subcommand %q\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: kenaz-ml-sign <subcommand> [flags]

  keygen  --out-dir DIR
  sign    --version V --artifact-path FILE --sha256 sha256:HEX [--key FILE] --out SIGFILE
  verify  --pubkey FILE --sig SIGFILE --version V --artifact-path FILE --sha256 sha256:HEX
  pin-gen --version V --channel-url URL --artifact-path FILE --sha256 sha256:HEX
          --size BYTES --key-id HEX [--out FILE]   |   pin-gen --zero [--out FILE]
`)
}

// ---- keygen ---------------------------------------------------------------

func cmdKeygen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outDir := fs.String("out-dir", "", "directory to write "+privKeyFileName+" and "+pubKeyFileName+" into")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *outDir == "" {
		fmt.Fprintln(stderr, "keygen: --out-dir is required")
		return exitUsage
	}
	if err := os.MkdirAll(*outDir, 0o700); err != nil {
		fmt.Fprintf(stderr, "keygen: %v\n", err)
		return exitInvalid
	}
	privPath := filepath.Join(*outDir, privKeyFileName)
	pubPath := filepath.Join(*outDir, pubKeyFileName)
	for _, p := range []string{privPath, pubPath} {
		if _, err := os.Stat(p); err == nil {
			fmt.Fprintf(stderr, "keygen: %s already exists; refusing to overwrite a signing key\n", p)
			return exitInvalid
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(stderr, "keygen: generate: %v\n", err)
		return exitInvalid
	}
	if err := writeExclusive(privPath, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		fmt.Fprintf(stderr, "keygen: %v\n", err)
		return exitInvalid
	}
	if err := writeExclusive(pubPath, []byte(hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
		_ = os.Remove(privPath)
		fmt.Fprintf(stderr, "keygen: %v\n", err)
		return exitInvalid
	}
	fmt.Fprintf(stderr, "keygen: wrote %s (PRIVATE — set as the %s secret, never commit) and %s\n", privPath, SigningKeyEnv, pubPath)
	fmt.Fprintln(stdout, trust.ComputeFingerprint(pub))
	return exitOK
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

// ---- sign -----------------------------------------------------------------

// signSummary is the one-line JSON sign prints (publish-job contract).
type signSummary struct {
	KeyID        string `json:"key_id"`
	Version      string `json:"version"`
	ArtifactPath string `json:"artifact_path"`
	SHA256       string `json:"sha256"`
}

func cmdSign(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("version", "", "engine semver (e.g. 1.2.0)")
	artifactPath := fs.String("artifact-path", "", "channel-relative BARE filename of the published artifact")
	sha := fs.String("sha256", "", "sha256:<lowercase hex> of the artifact bytes as published")
	keyFile := fs.String("key", "", "private key file (default: $"+SigningKeyEnv+")")
	out := fs.String("out", "", "path to write the raw 64-byte detached signature")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *out == "" {
		fmt.Fprintln(stderr, "sign: --out is required")
		return exitUsage
	}
	if err := validateTriple(*version, *artifactPath, *sha); err != nil {
		fmt.Fprintf(stderr, "sign: %v\n", err)
		return exitUsage
	}
	var keyText string
	if *keyFile != "" {
		b, err := os.ReadFile(*keyFile)
		if err != nil {
			fmt.Fprintf(stderr, "sign: read key: %v\n", err)
			return exitUsage
		}
		keyText = string(b)
	} else {
		keyText = getenv(SigningKeyEnv)
		if strings.TrimSpace(keyText) == "" {
			fmt.Fprintf(stderr, "sign: no key: pass --key or set %s\n", SigningKeyEnv)
			return exitUsage
		}
	}
	priv, err := parsePrivateKey(keyText)
	if err != nil {
		fmt.Fprintf(stderr, "sign: %v\n", err)
		return exitUsage
	}
	sig := ed25519.Sign(priv, signingPayload(*version, *artifactPath, *sha))
	if err := os.WriteFile(*out, sig, 0o644); err != nil {
		fmt.Fprintf(stderr, "sign: write signature: %v\n", err)
		return exitInvalid
	}
	pub := priv.Public().(ed25519.PublicKey)
	line, _ := json.Marshal(signSummary{
		KeyID:        trust.ComputeFingerprint(pub),
		Version:      *version,
		ArtifactPath: *artifactPath,
		SHA256:       *sha,
	})
	fmt.Fprintln(stdout, string(line))
	return exitOK
}

// signingPayload is THE canonical byte string both sides agree on. It
// deliberately calls the harness's own EngineManifest + SigningPayload
// rather than re-deriving anything (SigningPayload excludes the
// Signatures list, so the signature ref passed here is irrelevant).
func signingPayload(version, artifactPath, sha string) []byte {
	return mlsidecar.EngineManifest(version, artifactPath, sha, nil).SigningPayload()
}

// validateTriple enforces the publish contract on the three signed
// fields: a version, a BARE channel-relative filename (the signature
// locator is "<artifact-path>.sig" in the same channel directory), and a
// lowercase sha256:<hex> digest in core/bundle/integrity's format.
func validateTriple(version, artifactPath, sha string) error {
	if strings.TrimSpace(version) == "" {
		return errors.New("--version is required")
	}
	if artifactPath == "" {
		return errors.New("--artifact-path is required")
	}
	if strings.ContainsAny(artifactPath, `/\`) || artifactPath == "." || artifactPath == ".." {
		return fmt.Errorf("--artifact-path %q must be a bare filename (channel-relative, no directories)", artifactPath)
	}
	if !sha256Re.MatchString(sha) {
		return fmt.Errorf("--sha256 %q must be sha256:<64 lowercase hex chars>", sha)
	}
	return nil
}

func parsePrivateKey(text string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil {
		return nil, fmt.Errorf("private key is not base64: %w", err)
	}
	switch len(raw) {
	case ed25519.PrivateKeySize:
		priv := ed25519.PrivateKey(raw)
		// Guard against a corrupted secret: the embedded public half must
		// match the one the seed derives.
		derived := ed25519.NewKeyFromSeed(priv.Seed())
		if !derived.Equal(priv) {
			return nil, errors.New("private key is inconsistent (public half does not match seed)")
		}
		return priv, nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	default:
		return nil, fmt.Errorf("private key decodes to %d bytes; want %d (seed‖pub) or %d (seed)", len(raw), ed25519.PrivateKeySize, ed25519.SeedSize)
	}
}

// parsePublicKeyHex reads a public key file: one line of 64 hex chars;
// lines starting with '#' are ignored, so the harness's
// core/mlsidecar/release_signing_key.pub (which carries a comment
// header) can be passed to verify directly.
func parsePublicKeyHex(text string) (ed25519.PublicKey, error) {
	var keyLines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			keyLines = append(keyLines, line)
		}
	}
	if len(keyLines) != 1 {
		return nil, fmt.Errorf("public key file must hold exactly one key line, found %d", len(keyLines))
	}
	raw, err := hex.DecodeString(keyLines[0])
	if err != nil {
		return nil, fmt.Errorf("public key is not hex: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key decodes to %d bytes; want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// ---- verify ---------------------------------------------------------------

func cmdVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pubFile := fs.String("pubkey", "", "public key file (64 lowercase hex chars)")
	sigFile := fs.String("sig", "", "raw 64-byte detached signature file")
	version := fs.String("version", "", "engine semver")
	artifactPath := fs.String("artifact-path", "", "channel-relative bare filename")
	sha := fs.String("sha256", "", "sha256:<lowercase hex>")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *pubFile == "" || *sigFile == "" {
		fmt.Fprintln(stderr, "verify: --pubkey and --sig are required")
		return exitUsage
	}
	if err := validateTriple(*version, *artifactPath, *sha); err != nil {
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return exitUsage
	}
	pubText, err := os.ReadFile(*pubFile)
	if err != nil {
		fmt.Fprintf(stderr, "verify: read pubkey: %v\n", err)
		return exitUsage
	}
	pub, err := parsePublicKeyHex(string(pubText))
	if err != nil {
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return exitUsage
	}
	sig, err := os.ReadFile(*sigFile)
	if err != nil {
		fmt.Fprintf(stderr, "verify: read sig: %v\n", err)
		return exitUsage
	}
	if err := verifyLikeTheHarness(context.Background(), pub, sig, *version, *artifactPath, *sha); err != nil {
		fmt.Fprintf(stderr, "verify: INVALID: %v\n", err)
		return exitInvalid
	}
	fmt.Fprintf(stdout, "verify: OK key_id=%s\n", trust.ComputeFingerprint(pub))
	return exitOK
}

// verifyLikeTheHarness runs the signature through the harness's own
// verification stack: a core/trust engine holding pub as a raw-public-
// key anchor, wrapped in trust.EngineVerifier, called by
// integrity.VerifyManifestSignatures under SigningRequired over the
// manifest mlsidecar.Install builds — so "verify passes in CI" means
// "the harness will accept this signature", not "some ed25519 library
// agreed".
func verifyLikeTheHarness(ctx context.Context, pub ed25519.PublicKey, sig []byte, version, artifactPath, sha string) error {
	keyID := trust.ComputeFingerprint(pub)
	eng, err := trust.NewEngine(trust.Config{})
	if err != nil {
		return err
	}
	if err := eng.InstallAnchor(ctx, trust.Anchor{
		AnchorID:  "kenaz-ml-sign-verify",
		Kind:      trust.AnchorRawPublicKey,
		Algorithm: trust.AlgEd25519,
		PublicKey: trust.PublicKey{Algorithm: trust.AlgEd25519, Bytes: pub, Fingerprint: keyID},
	}); err != nil {
		return err
	}
	tv, err := trust.NewEngineVerifier(eng)
	if err != nil {
		return err
	}
	anchors, err := eng.ListAnchors(ctx)
	if err != nil {
		return err
	}
	locator := artifactPath + ".sig"
	m := mlsidecar.EngineManifest(version, artifactPath, sha, &manifest.SignatureRef{
		Kind: "ed25519_detached", Locator: locator, Algorithm: "ed25519", KeyID: keyID,
	})
	ok, err := integrity.VerifyManifestSignatures(ctx, m, tv, anchors, integrity.SigningRequired, func(loc string) ([]byte, error) {
		if loc != locator {
			return nil, fmt.Errorf("unexpected locator %q", loc)
		}
		return sig, nil
	})
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("no signature verified")
	}
	return nil
}
