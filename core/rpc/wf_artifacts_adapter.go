package rpc

// wf_artifacts_adapter.go — automation-actually-runs-01PMZ404 UNIT-5.
//
// wfArtifactsAdapter is the production implementation of
// corewf.ArtifactsReadWriter. Before this unit, corewf.Deps.Artifacts was
// never assigned (core/rpc/api.go's sole wfDeps literal), so every
// read_artifact / write_artifact step errored with "no Artifacts wired" —
// including the shipped doc_generator builtin, which burns a full
// README-synthesis model turn before failing on its final
// kind: write_artifact step.
//
// The interface is NOT satisfied by *artifacts.Manager alone — Manager
// exposes Config/SetCaptureConfig/WriteVersion/Capture but no read path.
// The read half copies the precedent at
// core/rpc/views/artifacts/impl.go:87 (API.Get): resolve metadata via
// coreart.Store.Get, then the bytes via the shared MediaStore keyed on
// ContentHash. The write half uses Manager.Capture, which does
// media.Put + store.Insert and inherits the session→project rollup —
// the same path chat's own artifact capture uses.
//
// Source is always coreart.SourceModelOutput (D-7, spec §5.5): adding a
// new source value would require a table-rebuild migration on the
// artifacts table (a SQL CHECK constraint enumerates the four accepted
// values), the same destructive migration family that produced the
// sessions/0327 data-loss incident. Not worth it for a label.

import (
	"context"
	"fmt"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	coreatt "github.com/kameas-ai/kenaz-harness/core/attachments"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

// wfArtifactsAdapter implements corewf.ArtifactsReadWriter over the same
// Store + Manager + MediaStore instances the artifacts RPC surface uses.
type wfArtifactsAdapter struct {
	store coreart.Store
	mgr   *coreart.Manager
	media coreatt.MediaStore
}

// Read implements corewf.ArtifactsReadWriter.
func (a *wfArtifactsAdapter) Read(ctx context.Context, id string) (corewf.ArtifactView, error) {
	if a == nil || a.store == nil {
		return corewf.ArtifactView{}, fmt.Errorf("workflows: artifacts store not wired")
	}
	row, err := a.store.Get(ctx, id)
	if err != nil {
		return corewf.ArtifactView{}, err
	}
	if a.media == nil {
		return corewf.ArtifactView{}, fmt.Errorf("workflows: artifacts media store not wired")
	}
	_, body, err := a.media.GetByHash(ctx, row.ContentHash)
	if err != nil {
		return corewf.ArtifactView{}, fmt.Errorf("workflows: load artifact bytes: %w", err)
	}
	return corewf.ArtifactView{
		ID:       row.ID,
		Title:    row.Title,
		MimeType: row.MimeType,
		Content:  body,
	}, nil
}

// Write implements corewf.ArtifactsReadWriter. Always captures with
// Source: SourceModelOutput (D-7 — see this file's package doc).
func (a *wfArtifactsAdapter) Write(ctx context.Context, in corewf.ArtifactWrite) (string, error) {
	if a == nil || a.mgr == nil {
		return "", fmt.Errorf("workflows: artifacts manager not wired")
	}
	if in.SessionID == "" {
		return "", fmt.Errorf("workflows: artifact write requires a session id")
	}
	rows, err := a.mgr.Capture(ctx, []coreart.CaptureCandidate{{
		Title:    in.Title,
		MimeType: in.MimeType,
		Bytes:    in.Content,
		Source:   coreart.SourceModelOutput,
	}}, in.SessionID)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("workflows: artifact capture returned no rows")
	}
	return rows[0].ID, nil
}

var _ corewf.ArtifactsReadWriter = (*wfArtifactsAdapter)(nil)
