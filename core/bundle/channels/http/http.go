// Package http implements the http_mirror distribution channel
// (bundle-download-and-verify-01PMZ909, UNIT-6) — the first channel
// that actually reaches a network.
//
// Chosen over git and oci because it needs no external binary and no
// registry protocol, and because Channel.LookupSignatures has an
// obvious meaning for it: a sibling "<path>.sig" resource, matching
// the one signature scheme with working math (ed25519_detached).
//
// Per DIRECTIVE_001 (core/bundle/channels/channel.go), this is a new
// subpackage plus one Register call — and the Register call into the
// PRODUCTION registry is deliberately not in this package. It ships in
// UNIT-7's commit, alongside the Cedar gate that must exist before
// Bundle_Install can reach a URL the caller supplies (plan.md Rule 2).
// This package is safe to import and exercise in isolation: Factory
// and the Channel it returns do nothing until a caller explicitly
// opens one, either directly or through a test-local
// channels.Registry.
package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	bundle "github.com/kameas-ai/kenaz-harness/core/bundle"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// Kind is the registered channel kind id.
const Kind = "http_mirror"

// defaultTimeout bounds every outbound request this channel makes.
// Bundle fetches can legitimately be large; this is a request-level
// (not a whole-Fetch-body) deadline via the client's Timeout field —
// see the doc on http.Client.Timeout for why that's a wall-clock cap
// on the entire round trip including body read. A caller that needs a
// longer bound should pass ctx with its own deadline; ctx cancellation
// always aborts the request regardless of this value.
const defaultTimeout = 60 * time.Second

// consumerID identifies this channel to the secrets resolver (FR-016
// scoping) without ever including bundle- or path-specific detail that
// could leak into resolver-side audit logs.
const consumerID = "bundle:http_mirror"

// Factory is the channels.Factory for http_mirror. Register with:
//
//	registry.Register(http.Kind, http.Factory)
func Factory(spec channels.ChannelSpec, creds secrets.ResolverAPI) (channels.Channel, error) {
	if spec.Kind != Kind {
		return nil, fmt.Errorf("http: spec.Kind=%q want %q", spec.Kind, Kind)
	}
	if spec.URL == "" {
		return nil, fmt.Errorf("http: spec.URL is required")
	}
	u, err := url.Parse(spec.URL)
	if err != nil {
		return nil, fmt.Errorf("http: parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("http: unsupported scheme %q (want http or https)", u.Scheme)
	}
	if creds == nil {
		creds = secrets.NoopResolver{}
	}
	return &httpChannel{
		base:   strings.TrimRight(spec.URL, "/"),
		auth:   spec.Auth,
		creds:  creds,
		client: &http.Client{Timeout: defaultTimeout},
	}, nil
}

type httpChannel struct {
	base   string
	auth   *channels.AuthRef
	creds  secrets.ResolverAPI
	client *http.Client
}

func (c *httpChannel) Kind() string { return Kind }

// Reachable performs a pre-flight HEAD against the mirror root. Any
// completed HTTP round trip — even a 404 or 405 — proves the mirror is
// reachable: "nothing is served at exactly this root path" is a
// different failure than "cannot connect at all", and many realistic
// mirrors (e.g. a bucket root with no index) 404 at "/" while every
// individual artifact fetches fine. Only a transport-level failure
// (DNS, TCP, TLS) or a 5xx (server-side outage) counts as unreachable.
func (c *httpChannel) Reachable(ctx context.Context) error {
	req, err := c.newRequest(ctx, http.MethodHead, c.base)
	if err != nil {
		return fmt.Errorf("%w: build request: %v", bundle.ErrChannelUnreachable, err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", bundle.ErrChannelUnreachable, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: status %d from %s", bundle.ErrChannelUnreachable, resp.StatusCode, c.base)
	}
	return nil
}

// Fetch streams the artifact at ref.Path from the mirror into sink.
// The channel does NOT verify content hashes (channel.go's own
// contract) — the resolver's CAS.Put call is the authoritative check.
func (c *httpChannel) Fetch(ctx context.Context, ref channels.ArtifactCoord, sink io.Writer) (channels.FetchResult, error) {
	target, err := c.resolveURL(ref.Path)
	if err != nil {
		return channels.FetchResult{}, err
	}
	req, err := c.newRequest(ctx, http.MethodGet, target)
	if err != nil {
		return channels.FetchResult{}, fmt.Errorf("http: build request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return channels.FetchResult{}, fmt.Errorf("http: fetch %s: %w", sanitize(target), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return channels.FetchResult{}, fmt.Errorf("http: fetch %s: status %d", sanitize(target), resp.StatusCode)
	}
	n, err := io.Copy(sink, resp.Body)
	if err != nil {
		return channels.FetchResult{}, fmt.Errorf("http: read body from %s: %w", sanitize(target), err)
	}
	// Endpoint is documented as "sanitized endpoint (no credentials)"
	// (channel.go:86) — target never carries the resolved credential
	// (it travels only in the Authorization header, built and
	// discarded inside newRequest), so it is already safe to return
	// as-is. sanitize() is applied anyway for defense in depth against
	// a future caller putting a token in spec.URL's query string.
	return channels.FetchResult{Bytes: n, Endpoint: sanitize(target)}, nil
}

// LookupSignatures probes for a sibling "<path>.sig" resource — the
// http_mirror counterpart to local_path's on-disk convention, chosen
// because it is the shape ed25519_detached (the one working scheme,
// spec D-1) expects.
func (c *httpChannel) LookupSignatures(ctx context.Context, ref channels.ArtifactCoord) ([]channels.SignatureRef, error) {
	target, err := c.resolveURL(ref.Path + ".sig")
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodHead, target)
	if err != nil {
		return nil, fmt.Errorf("http: build request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: probe signature %s: %w", sanitize(target), err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return []channels.SignatureRef{}, nil
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http: probe signature %s: status %d", sanitize(target), resp.StatusCode)
	}
	return []channels.SignatureRef{{
		Kind:    "ed25519_detached",
		Locator: ref.Path + ".sig",
	}}, nil
}

// resolveURL joins c.base with a bundle-relative path, rejecting the
// same traversal shapes localpath.resolveSafe rejects — an artifact or
// signature locator is never allowed to escape the mirror root, even
// though "escape" here means "request a different URL path" rather
// than a filesystem read.
func (c *httpChannel) resolveURL(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("%w: empty path", bundle.ErrPathTraversal)
	}
	if strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("%w: absolute path %q", bundle.ErrPathTraversal, rel)
	}
	cleaned := strings.TrimPrefix(rel, "./")
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: traversal %q", bundle.ErrPathTraversal, rel)
		}
	}
	return c.base + "/" + cleaned, nil
}

// newRequest builds an HTTP request with the resolved credential (if
// any) attached as a Bearer Authorization header. The raw secret bytes
// never leave this function — they are read via Secret.Use, copied
// into the request header, and the Secret is destroyed immediately
// after. No log line, error message, or FetchResult ever sees them.
func (c *httpChannel) newRequest(ctx context.Context, method, target string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	if c.auth == nil || c.auth.Keychain == "" {
		return req, nil
	}
	sec, err := c.creds.Resolve(ctx, secrets.CredentialReference{
		Kind:       secrets.RefKeychain,
		Locator:    c.auth.Keychain,
		ConsumerID: consumerID,
	}, consumerID)
	if err != nil {
		return nil, fmt.Errorf("http: resolve credential: %w", err)
	}
	defer sec.Destroy()
	useErr := sec.Use(func(value []byte) error {
		req.Header.Set("Authorization", "Bearer "+string(value))
		return nil
	})
	if useErr != nil {
		return nil, fmt.Errorf("http: use credential: %w", useErr)
	}
	return req, nil
}

// sanitize strips query and userinfo from target before it can appear
// in an error message or FetchResult.Endpoint — belt-and-suspenders
// alongside newRequest never putting the credential in the URL at all.
func sanitize(target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return "<unparseable-endpoint>"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// Compile-time assertion that httpChannel satisfies channels.Channel.
var _ channels.Channel = (*httpChannel)(nil)
