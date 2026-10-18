package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels/localpath"
	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
	"github.com/kameas-ai/kenaz-harness/core/trust"
)

func noEnv(string) string { return "" }

func runCmd(t *testing.T, getenv func(string) string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, &o, &e, getenv)
	return code, o.String(), e.String()
}

// keygen writes a fresh keypair into a temp dir and returns the paths +
// the KeyID it printed.
func keygen(t *testing.T) (privPath, pubPath, keyID string) {
	t.Helper()
	dir := t.TempDir()
	code, out, errOut := runCmd(t, noEnv, "keygen", "--out-dir", dir)
	if code != exitOK {
		t.Fatalf("keygen exit %d: %s", code, errOut)
	}
	keyID = strings.TrimSpace(out)
	if len(keyID) != 64 || strings.ToLower(keyID) != keyID {
		t.Fatalf("keygen KeyID %q is not 64 lowercase hex chars", keyID)
	}
	return filepath.Join(dir, privKeyFileName), filepath.Join(dir, pubKeyFileName), keyID
}

func writeEngineZip(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("kameas-ml/kameas-ml")
	_, _ = w.Write([]byte("engine launcher bytes"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	sha, err := mlsidecar.HashFileSHA256(p)
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

// TestSignThenHarnessInstallVerifies is the WP-H1 end-to-end pin: the
// publisher (this tool's sign subcommand, exactly the argv the kenaz-ml
// publish job runs) and the verifier (mlsidecar.Install — the real
// sidecar install path: channel fetch → VerifyEngineArtifact →
// integrity.VerifyManifestSignatures under SigningRequired against a
// real core/trust engine whose anchor is the keygen'd public key) agree
// on the canonical bytes. A tampered pin (wrong version) is refused.
func TestSignThenHarnessInstallVerifies(t *testing.T) {
	ctx := context.Background()
	privPath, pubPath, keyID := keygen(t)

	const version = "1.2.0"
	// The publish contract names a .dmg; the install path verifies the
	// downloaded bytes BEFORE choosing an unpacker, so a zip artifact
	// proves the identical signature path without needing hdiutil.
	artifact := "kenaz-ml-" + version + "-darwin-arm64.zip"
	channelRoot := t.TempDir()
	sha := writeEngineZip(t, channelRoot, artifact)
	sigPath := filepath.Join(channelRoot, artifact+".sig")

	code, out, errOut := runCmd(t, noEnv, "sign", "--version", version, "--artifact-path", artifact,
		"--sha256", sha, "--key", privPath, "--out", sigPath)
	if code != exitOK {
		t.Fatalf("sign exit %d: %s", code, errOut)
	}
	var summary map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &summary); err != nil {
		t.Fatalf("sign stdout is not one JSON line: %q (%v)", out, err)
	}
	want := map[string]string{"key_id": keyID, "version": version, "artifact_path": artifact, "sha256": sha}
	if len(summary) != len(want) {
		t.Fatalf("summary keys = %v, want exactly %v", summary, want)
	}
	for k, v := range want {
		if summary[k] != v {
			t.Errorf("summary[%q] = %q, want %q", k, summary[k], v)
		}
	}
	sig, _ := os.ReadFile(sigPath)
	if len(sig) != 64 {
		t.Fatalf("signature file is %d bytes; the contract is RAW 64-byte ed25519", len(sig))
	}

	// The harness side: a real trust engine with the published pubkey
	// installed as an anchor, exactly as the TrustAnchors RPC / baked-key
	// seeding would leave it.
	pubText, _ := os.ReadFile(pubPath)
	pub, err := parsePublicKeyHex(string(pubText))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := trust.NewEngine(trust.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.InstallAnchor(ctx, trust.Anchor{
		AnchorID: "kameas-release", Kind: trust.AnchorRawPublicKey, Algorithm: trust.AlgEd25519,
		PublicKey: trust.PublicKey{Algorithm: trust.AlgEd25519, Bytes: pub, Fingerprint: trust.ComputeFingerprint(pub)},
	}); err != nil {
		t.Fatal(err)
	}
	tv, err := trust.NewEngineVerifier(eng)
	if err != nil {
		t.Fatal(err)
	}
	anchors, _ := eng.ListAnchors(ctx)
	verifier := mlsidecar.DefaultEngineVerifier(tv, anchors)
	if verifier.Policy != integrity.SigningRequired {
		t.Fatal("the production engine verifier must be SigningRequired")
	}

	reg := channels.NewRegistry()
	_ = reg.Register(localpath.Kind, localpath.Factory)
	req := mlsidecar.InstallRequest{
		ChannelKind: localpath.Kind, ChannelPath: channelRoot,
		Version: version, ArtifactPath: artifact, ExpectedSHA256: sha,
		Signature: &manifest.SignatureRef{Kind: "ed25519_detached", Locator: artifact + ".sig", Algorithm: "ed25519", KeyID: keyID},
		Source:    "local_path:" + channelRoot,
	}
	res, err := mlsidecar.Install(ctx, mlsidecar.NewLayout(t.TempDir()), reg, secrets.NoopResolver{}, verifier, req)
	if err != nil {
		t.Fatalf("harness install refused a kenaz-ml-sign signature: %v", err)
	}
	if !res.Record.Verified || res.Record.Provenance != mlsidecar.ProvenanceChannelManifest {
		t.Fatalf("install record = %+v; want a positively verified channel-manifest install", res.Record)
	}

	// Negative control: the same signature over a DIFFERENT pin (version
	// bumped without re-signing) must be refused by the same path.
	bad := req
	bad.Version = "1.2.1"
	if _, err := mlsidecar.Install(ctx, mlsidecar.NewLayout(t.TempDir()), reg, secrets.NoopResolver{}, verifier, bad); !errors.Is(err, mlsidecar.ErrVerificationFailed) {
		t.Fatalf("install with a re-pinned version err = %v, want ErrVerificationFailed", err)
	}

	// And the CI self-check agrees in both directions.
	if code, _, e := runCmd(t, noEnv, "verify", "--pubkey", pubPath, "--sig", sigPath,
		"--version", version, "--artifact-path", artifact, "--sha256", sha); code != exitOK {
		t.Fatalf("verify exit %d on a good signature: %s", code, e)
	}
	if code, _, _ := runCmd(t, noEnv, "verify", "--pubkey", pubPath, "--sig", sigPath,
		"--version", "1.2.1", "--artifact-path", artifact, "--sha256", sha); code != exitInvalid {
		t.Fatalf("verify exit %d on a mismatched version, want %d", code, exitInvalid)
	}
}

func TestSign_KeyFromEnv(t *testing.T) {
	privPath, pubPath, keyID := keygen(t)
	keyText, _ := os.ReadFile(privPath)
	env := func(k string) string {
		if k == SigningKeyEnv {
			return string(keyText)
		}
		return ""
	}
	dir := t.TempDir()
	sigPath := filepath.Join(dir, "a.dmg.sig")
	sha := "sha256:" + strings.Repeat("ab", 32)
	code, out, e := runCmd(t, env, "sign", "--version", "0.1.0", "--artifact-path", "a.dmg", "--sha256", sha, "--out", sigPath)
	if code != exitOK {
		t.Fatalf("sign via env exit %d: %s", code, e)
	}
	if !strings.Contains(out, keyID) {
		t.Fatalf("stdout %q lacks key id %s", out, keyID)
	}
	if code, _, e := runCmd(t, noEnv, "verify", "--pubkey", pubPath, "--sig", sigPath, "--version", "0.1.0", "--artifact-path", "a.dmg", "--sha256", sha); code != exitOK {
		t.Fatalf("verify exit %d: %s", code, e)
	}
}

func TestSign_RejectsContractViolations(t *testing.T) {
	privPath, _, _ := keygen(t)
	out := filepath.Join(t.TempDir(), "x.sig")
	good := "sha256:" + strings.Repeat("0f", 32)
	cases := map[string][]string{
		"no key":             {"sign", "--version", "1.0.0", "--artifact-path", "a.dmg", "--sha256", good, "--out", out},
		"path with dir":      {"sign", "--version", "1.0.0", "--artifact-path", "kenaz-ml/1.0.0/a.dmg", "--sha256", good, "--key", privPath, "--out", out},
		"uppercase sha":      {"sign", "--version", "1.0.0", "--artifact-path", "a.dmg", "--sha256", strings.ToUpper(good), "--key", privPath, "--out", out},
		"bare hex sha":       {"sign", "--version", "1.0.0", "--artifact-path", "a.dmg", "--sha256", strings.Repeat("0f", 32), "--key", privPath, "--out", out},
		"missing version":    {"sign", "--artifact-path", "a.dmg", "--sha256", good, "--key", privPath, "--out", out},
		"missing out":        {"sign", "--version", "1.0.0", "--artifact-path", "a.dmg", "--sha256", good, "--key", privPath},
		"unknown subcommand": {"publish"},
	}
	for name, args := range cases {
		if code, _, _ := runCmd(t, noEnv, args...); code != exitUsage {
			t.Errorf("%s: exit %d, want %d", name, code, exitUsage)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a refused sign must not write a signature file")
	}
}

func TestKeygen_RefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	if code, _, e := runCmd(t, noEnv, "keygen", "--out-dir", dir); code != exitOK {
		t.Fatalf("first keygen: %d %s", code, e)
	}
	before, _ := os.ReadFile(filepath.Join(dir, privKeyFileName))
	if code, _, _ := runCmd(t, noEnv, "keygen", "--out-dir", dir); code == exitOK {
		t.Fatal("second keygen into the same dir must refuse")
	}
	after, _ := os.ReadFile(filepath.Join(dir, privKeyFileName))
	if !bytes.Equal(before, after) {
		t.Fatal("refused keygen changed the existing private key")
	}
	if st, err := os.Stat(filepath.Join(dir, privKeyFileName)); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode = %v (%v), want 0600", st.Mode().Perm(), err)
	}
}

func TestParsePrivateKey_SeedFormAndCorruption(t *testing.T) {
	privPath, _, _ := keygen(t)
	text, _ := os.ReadFile(privPath)
	full, err := parsePrivateKey(string(text))
	if err != nil {
		t.Fatal(err)
	}
	// A seed-only base64 value derives the same key.
	seedOnly, err := parsePrivateKey(b64(full.Seed()))
	if err != nil || !seedOnly.Equal(full) {
		t.Fatalf("seed form: %v", err)
	}
	// A 64-byte value whose public half does not match is refused.
	corrupt := append([]byte(nil), full...)
	corrupt[63] ^= 0xff
	if _, err := parsePrivateKey(b64(corrupt)); err == nil {
		t.Fatal("inconsistent private key accepted")
	}
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
