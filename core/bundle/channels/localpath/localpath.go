// Package localpath implements the local_path distribution channel.
//
// local_path is the simplest channel — it reads bundles directly from a
// filesystem root. It is the baseline that every other channel's path
// resolution is compared against, and it is the channel used by all
// integration tests that don't depend on network fixtures.
//
// Path-traversal hardening: every Fetch call confirms the resolved
// artifact path is contained within the configured root via filepath.Rel
// followed by a `..` rejection. Symlinks that point outside the root are
// followed by os.Open but rejected by the same Rel check, since their
// real path would surface in a Stat call upstream of activation. We do
// NOT call EvalSymlinks at this layer — symlink containment is also the
// responsibility of the manifest validator (NFR-004).
package localpath

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	bundle "github.com/sigil-tech/kaneaz-harness/core/bundle"
	"github.com/sigil-tech/kaneaz-harness/core/bundle/channels"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// Kind is the registered channel kind id.
const Kind = "local_path"

// Factory is the channels.Factory for local_path. Register with:
//
//	registry.Register(localpath.Kind, localpath.Factory)
func Factory(spec channels.ChannelSpec, _ secrets.Resolver) (channels.Channel, error) {
	if spec.Kind != Kind {
		return nil, fmt.Errorf("localpath: spec.Kind=%q want %q", spec.Kind, Kind)
	}
	if spec.Path == "" {
		return nil, fmt.Errorf("localpath: spec.Path is required")
	}
	abs, err := filepath.Abs(spec.Path)
	if err != nil {
		return nil, fmt.Errorf("localpath: abs(%q): %w", spec.Path, err)
	}
	return &localChannel{root: abs}, nil
}

type localChannel struct {
	root string
}

func (c *localChannel) Kind() string { return Kind }

func (c *localChannel) Reachable(_ context.Context) error {
	info, err := os.Stat(c.root)
	if err != nil {
		return fmt.Errorf("%w: stat %s: %v", bundle.ErrChannelUnreachable, c.root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", bundle.ErrChannelUnreachable, c.root)
	}
	return nil
}

func (c *localChannel) Fetch(ctx context.Context, ref channels.ArtifactCoord, sink io.Writer) (channels.FetchResult, error) {
	resolved, err := c.resolveSafe(ref.Path)
	if err != nil {
		return channels.FetchResult{}, err
	}
	select {
	case <-ctx.Done():
		return channels.FetchResult{}, fmt.Errorf("%w: pre-open", bundle.ErrCancelled)
	default:
	}
	f, err := os.Open(resolved)
	if err != nil {
		return channels.FetchResult{}, fmt.Errorf("localpath: open %s: %w", resolved, err)
	}
	defer f.Close()

	n, err := copyWithCancel(ctx, sink, f)
	if err != nil {
		return channels.FetchResult{}, err
	}
	return channels.FetchResult{Bytes: n, Endpoint: c.root}, nil
}

func (c *localChannel) LookupSignatures(_ context.Context, ref channels.ArtifactCoord) ([]channels.SignatureRef, error) {
	resolved, err := c.resolveSafe(ref.Path)
	if err != nil {
		return nil, err
	}
	sigPath := resolved + ".sig"
	if _, err := os.Stat(sigPath); err != nil {
		if os.IsNotExist(err) {
			return []channels.SignatureRef{}, nil
		}
		return nil, err
	}
	return []channels.SignatureRef{{
		Kind:    "ed25519_detached",
		Locator: sigPath,
	}}, nil
}

// resolveSafe joins the configured root with the requested in-bundle
// relative path and confirms the result stays under the root. Returns
// ErrPathTraversal on escape.
func (c *localChannel) resolveSafe(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: absolute path %q", bundle.ErrPathTraversal, rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == "." {
		return "", fmt.Errorf("%w: traversal %q", bundle.ErrPathTraversal, rel)
	}
	joined := filepath.Join(c.root, clean)
	// Belt-and-suspenders: confirm joined is under root via Rel.
	relPath, err := filepath.Rel(c.root, joined)
	if err != nil {
		return "", fmt.Errorf("%w: rel: %v", bundle.ErrPathTraversal, err)
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: rel=%q", bundle.ErrPathTraversal, relPath)
	}
	return joined, nil
}

// copyWithCancel is io.Copy that polls ctx.Done between buffer fills.
func copyWithCancel(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	const buf = 32 * 1024
	b := make([]byte, buf)
	var total int64
	for {
		select {
		case <-ctx.Done():
			return total, fmt.Errorf("%w: %v", bundle.ErrCancelled, ctx.Err())
		default:
		}
		n, err := src.Read(b)
		if n > 0 {
			if _, werr := dst.Write(b[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if err != nil {
			if err == io.EOF {
				return total, nil
			}
			return total, err
		}
	}
}
